package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Opening a file an agent mentions ("review docs/PLAN.md") used to mean: new
// terminal, cd, open an editor. Now: v (or right-click → open a file…) scrapes
// path-looking tokens off the agent's screen, offers the ones that exist, and
// opens the pick in a pager split beside the agent — markdown rendered (glow/
// bat), q closes the split, prefix+z fullscreens it. Chat stays usable.

var fileTokenRe = regexp.MustCompile(`[~A-Za-z0-9_./\-]+`)

// screenFiles scrapes existing files from the agent's visible terminal (plus
// a little scrollback), most recently mentioned first.
func screenFiles(sess, dir string) []string {
	out, err := tmuxRun("capture-pane", "-p", "-t", "="+sess+":", "-S", "-300")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var files []string
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		for _, tok := range fileTokenRe.FindAllString(lines[i], -1) {
			if !strings.ContainsAny(tok, "/.") {
				continue
			}
			tok = strings.TrimRight(tok, ".") // sentence-ending dot
			p := tok
			switch {
			case strings.HasPrefix(p, "~"):
				p = expandHome(p)
			case !filepath.IsAbs(p):
				p = filepath.Join(dir, p)
			}
			p = filepath.Clean(p)
			if seen[p] {
				continue
			}
			seen[p] = true
			if fi, err := os.Stat(p); err != nil || fi.IsDir() {
				continue
			}
			files = append(files, p)
			if len(files) >= 12 {
				return files
			}
		}
	}
	return files
}

// viewerCmd picks the renderer: glow for markdown when installed, bat when
// installed, else less. Images/PDFs open in the OS viewer on macOS.
func viewerCmd(path string) string {
	q := shQuote(path)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".pdf":
		if runtime.GOOS == "darwin" {
			return "open " + q
		}
	case ".md", ".markdown":
		if _, err := exec.LookPath("glow"); err == nil {
			return "glow -p " + q
		}
	}
	if _, err := exec.LookPath("bat"); err == nil {
		return "bat --paging=always " + q
	}
	return "less -R " + q
}

// openFile renders path per viewerCmd: a pager split beside pane, or the OS
// viewer for images.
func openFile(pane, path string) {
	v := viewerCmd(path)
	if strings.HasPrefix(v, "open ") {
		_, _ = tmuxRun("run-shell", "-b", v)
		return
	}
	_, _ = tmuxRun("split-window", "-h", "-t", pane, v)
}

// fileMenu shows the scraped files for agent `name`; the chosen one opens in
// a split of pane (or the OS viewer for images).
func fileMenu(name, pane string) {
	s, err := loadSpec(name)
	if err != nil {
		return
	}
	files := screenFiles(s.TmuxSession, s.Dir)
	if len(files) == 0 {
		_, _ = tmuxRun("display-message", "muster: no file paths on "+name+"'s screen")
		return
	}
	menu := []string{"display-menu", "-T", " open · " + name + " ", "-x", "C", "-y", "C"}
	keys := "123456789abc"
	for i, f := range files {
		label := collapseHome(f)
		if len(label) > 50 {
			label = "…" + label[len(label)-49:]
		}
		v := viewerCmd(f)
		act := "split-window -h -t " + pane + " " + shQuote(v)
		if strings.HasPrefix(v, "open ") {
			act = "run-shell -b " + shQuote(v)
		}
		menu = append(menu, label, string(keys[i]), act)
	}
	_, _ = tmuxRun(menu...)
}

// cmdFmenu: `muster fmenu <agent> <pane>` — the file menu, callable from
// tmux menu items (the sidebar's v key calls fileMenu directly).
func cmdFmenu(args []string) int {
	if len(args) >= 2 {
		fileMenu(args[0], args[1])
	}
	return 0
}

// cmdRmenu: `muster rmenu <pane> <x> <y>` — the agent context menu for
// right-clicks on live-agent panes (bound to MouseUp3Pane at bootstrap).
// The pane runs a nested tmux client; its tty identifies the agent session.
// Actions use native tmux prompts/popups so no sidebar roundtrip is needed.
func cmdRmenu(args []string) int {
	if len(args) < 1 {
		return 0
	}
	pane := args[0]
	out, err := tmuxRun("display-message", "-p", "-t", pane, "#{pane_tty}\t#{pane_left}\t#{pane_top}")
	if err != nil {
		return 0
	}
	tty, rest, _ := strings.Cut(out, "\t")
	l, t, _ := strings.Cut(rest, "\t")
	left, _ := strconv.Atoi(l)
	top, _ := strconv.Atoi(t)
	// #{mouse_x}/#{mouse_y} are pane-relative but display-menu -x/-y are
	// client-absolute — unshifted, the menu opens sidebar-widths left of the
	// pointer. +1 y sits the menu just under the pointer (numeric -y anchors
	// the menu's bottom edge).
	x, y := "C", "C" // fall back to centered if mouse coords didn't expand
	if len(args) >= 3 {
		if mx, err := strconv.Atoi(args[1]); err == nil {
			x = strconv.Itoa(left + mx)
		}
		if my, err := strconv.Atoi(args[2]); err == nil {
			y = strconv.Itoa(top + my + 1)
		}
	}
	sess := ""
	if out, err := tmuxRun("list-clients", "-F", "#{client_tty}\t#{client_session}"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if t, s, ok := strings.Cut(l, "\t"); ok && t == tty {
				sess = s
				break
			}
		}
	}
	if !strings.HasPrefix(sess, sessPrefix) {
		return 0 // welcome/setup/room pane — nothing to act on
	}
	name := strings.TrimPrefix(sess, sessPrefix)
	s, err := loadSpec(name)
	if err != nil {
		return 0
	}
	self := shQuote(selfExe())
	run := func(argv string) string { return "run-shell -b " + shQuote(self+" "+argv) }
	popup := func(argv string) string {
		return "display-popup -E -w 80% -h 70% " + shQuote("sh -c "+shQuote(self+" "+argv+`; printf '\n[enter to close] '; read -r _`))
	}
	menu := []string{"display-menu", "-T", " " + name + " ", "-x", x, "-y", y,
		"recap", "e", popup("recap " + name),
		"send message…", "s", "command-prompt -p '→ " + name + ":' " + shQuote(run("send "+name+" \"%%\"")),
		"open a file…", "v", run("fmenu " + name + " " + pane),
		"terminal here", "t", "split-window -v -l 12 -t " + pane + " -c " + shQuote(s.Dir),
		"", "", "",
		"split right", "l", "split-window -h -t " + pane + " -c " + shQuote(s.Dir),
		"split down", "j", "split-window -v -t " + pane + " -c " + shQuote(s.Dir),
		"split up", "u", "split-window -v -b -t " + pane + " -c " + shQuote(s.Dir),
		"split left", "h", "split-window -h -b -t " + pane + " -c " + shQuote(s.Dir),
		"zoom", "z", "resize-pane -Z -t " + pane,
		"", "", "",
		"inbox", "i", popup("inbox " + name),
		"schedule…", "c", "command-prompt -p 'cron " + name + ":' " + shQuote(run("cron add "+name+" %%")),
		"", "", ""}
	menu = append(menu, "review handoff", "w", run("review "+name))
	if s.Branch != "" {
		menu = append(menu, "done (merge & clean)", "D",
			"confirm-before -p 'merge "+s.Branch+" back & retire "+name+"? (y/n)' "+shQuote(run("done "+name)))
	}
	menu = append(menu,
		"kill (spec kept)", "k", "confirm-before -p 'kill "+name+"? (y/n)' "+shQuote(run("kill "+name)),
	)
	_, _ = tmuxRun(menu...)
	return 0
}
