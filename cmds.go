package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "muster:", err)
	return 1
}

// ---- spawn ----------------------------------------------------------------

func cmdSpawn(args []string) int {
	fs := flag.NewFlagSet("spawn", flag.ExitOnError)
	dir := fs.String("C", "", "directory to run in (default: cwd)")
	repo := fs.String("repo", "", "repo to create a worktree from (with -b)")
	branch := fs.String("b", "", "branch/worktree name (with --repo)")
	role := fs.String("role", "", "the agent's charter, e.g. 'reviewer' — teammates learn it")
	var envFlags multiFlag
	fs.Var(&envFlags, "e", "extra env K=V (repeatable)")
	noPpz := fs.Bool("no-ppz", false, "don't wrap in ppz terminal share")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: muster spawn <name> [-C dir | --repo dir -b branch] [-e K=V]... [--] [command...]")
		fs.PrintDefaults()
	}
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		fs.Usage()
		return 2
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if err := validName(name); err != nil {
		return fail(err)
	}
	if _, err := loadSpec(name); err == nil {
		return fail(errf("agent %q already exists (muster kill %s first, or pick a new name)", name, name))
	}
	if tmuxHasSession(tmuxSession(name)) {
		return fail(errf("tmux session %s already exists", tmuxSession(name)))
	}

	argv := fs.Args()
	if len(argv) == 0 {
		if def := os.Getenv("MUSTER_DEFAULT_CMD"); def != "" {
			argv = strings.Fields(def)
		} else {
			argv = []string{"claude"}
		}
	}

	spec := &AgentSpec{
		Name:        name,
		Role:        *role,
		Argv:        argv,
		Env:         map[string]string{},
		TmuxSession: tmuxSession(name),
		CreatedAt:   time.Now(),
	}
	for _, kv := range envFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fail(errf("bad -e %q, want K=V", kv))
		}
		spec.Env[k] = v
	}

	// working directory: worktree beats -C beats cwd
	setup := "" // .muster/setup runs in the pane for FRESH worktrees only
	switch {
	case *repo != "" && *branch != "":
		wt, fb, err := worktreeAdd(*repo, *branch)
		if err != nil {
			return fail(err)
		}
		abs, _ := filepath.Abs(*repo)
		spec.Repo, spec.Worktree, spec.Branch, spec.Dir = abs, wt, fb, wt
		fmt.Printf("worktree %s (branch %s)\n", wt, fb)
		if setup = setupScript(abs); setup != "" {
			fmt.Printf("setup %s runs in the pane before the agent\n", collapseHome(setup))
		}
		_, _ = addProject(abs, "") // idempotent: repos you spawn into show up in the UI
	case *repo != "" || *branch != "":
		return fail(errf("--repo and -b go together"))
	case *dir != "":
		abs, err := filepath.Abs(*dir)
		if err != nil {
			return fail(err)
		}
		spec.Dir = abs
	default:
		spec.Dir, _ = os.Getwd()
	}

	spec.Harness = detectHarness(argv)
	if spec.Harness == "claude" {
		if adopted, rest := adoptSessionID(argv); adopted != "" {
			spec.SessionUUID, spec.Argv = adopted, rest
		} else {
			spec.SessionUUID = newUUID()
		}
	}

	return launch(spec, false, *noPpz, setup)
}

// launch starts the tmux session for spec, wrapping in ppz terminal share
// when the mesh is available. Shared by spawn and resume. setup (fresh
// worktree spawns only) runs in the pane before everything else — composed
// here, never stored in Argv.
func launch(spec *AgentSpec, resume, noPpz bool, setup string) int {
	env := map[string]string{"MUSTER_AGENT": spec.Name}
	for k, v := range spec.Env {
		env[k] = v
	}

	// decide mesh membership BEFORE composing argv — the claude system
	// prompt briefing depends on it
	usePpz := !noPpz && ppzReady()
	if usePpz {
		spec.PpzHandle = spec.Name
	} else {
		spec.PpzHandle = ""
	}
	// settings are regenerated every launch so binary upgrades (new hooks,
	// statusLine) reach agents without re-running init
	if _, err := writeHooksSettings(); err != nil {
		fmt.Fprintf(os.Stderr, "muster: hooks settings: %v (continuing without)\n", err)
	}
	compose := composeSpawn
	if resume {
		compose = composeResume
	}
	inner := shJoin(compose(spec, hooksSettingsIfPresent()))

	cmd := inner
	if usePpz {
		env["PPZ_SESSION"] = spec.Name
		if spec.Harness != "" {
			// right submit-key for ppz's subs-alert injection + heartbeat hint
			env["PPZ_AGENT_HARNESS"] = spec.Harness
		}
		// the wrapped agent must find `ppz` on PATH (subs read, send)
		env["PATH"] = filepath.Dir(ppzBin()) + ":" + os.Getenv("PATH")
		ppzq := shQuote(ppzBin())
		if ppzSourceExists(spec.PpzHandle) {
			// handle survives restarts (keeps inbox history + schedules);
			// share's bare form reuses it: set current, then share.
			cmd = ppzq + " set handle " + spec.PpzHandle + " && " + ppzq + " terminal share -- " + inner
		} else {
			cmd = ppzq + " terminal share " + spec.PpzHandle + " -- " + inner
		}
	}
	cmd = withSetup(cmd, setup)
	// keep the pane alive on failure long enough to read the error
	cmd += `; rc=$?; [ $rc -ne 0 ] && { echo; echo "muster: agent exited rc=$rc — pane closes in 60s"; sleep 60; }`

	if spec.SessionUUID != "" {
		clearStatus(spec.SessionUUID)
	}
	if err := tmuxNewSession(spec.TmuxSession, spec.Dir, cmd, env); err != nil {
		return fail(err)
	}
	// no status bar in agent sessions — they render inside the muster
	// workspace pane, where a second bar is just noise. (=name: — bare
	// =name is rejected by set-option, same gotcha as send-keys.)
	_, _ = tmuxRun("set-option", "-t", "="+spec.TmuxSession+":", "status", "off")
	if err := saveSpec(spec); err != nil {
		_ = tmuxKillSession(spec.TmuxSession)
		return fail(err)
	}
	mesh := "no mesh"
	if usePpz {
		mesh = "ppz:" + spec.PpzHandle
	}
	fmt.Printf("spawned %s  tmux:%s  %s  dir:%s\n  cmd: %s\n", spec.Name, spec.TmuxSession, mesh, spec.Dir, inner)
	return 0
}

// ---- ls -------------------------------------------------------------------

type lsRow struct {
	Name    string  `json:"name"`
	Role    string  `json:"role,omitempty"`
	State   string  `json:"state"`
	Reason  string  `json:"reason,omitempty"`
	Harness string  `json:"harness,omitempty"`
	Dir     string  `json:"dir"`
	Repo    string  `json:"repo,omitempty"`
	Branch  string  `json:"branch,omitempty"`
	Tmux    string  `json:"tmux"`
	Ppz     string  `json:"ppz,omitempty"`
	Unread  int     `json:"unread"`
	Age     string  `json:"age"`
	Cmd     string  `json:"cmd"`
	Model   string  `json:"model,omitempty"`   // from claude statusline
	CtxPct  float64 `json:"ctx_pct,omitempty"` // context window used %
	FivePct float64 `json:"five_pct,omitempty"`
	FiveEnd string  `json:"five_end,omitempty"` // HH:MM reset time
}

func gatherRows() ([]lsRow, error) {
	specs, err := listSpecs()
	if err != nil {
		return nil, err
	}
	hb := ppzWho()
	unread := ppzUnreadCounts()
	var rows []lsRow
	for _, s := range specs {
		state, reason := liveState(s, hb)
		r := lsRow{
			Name: s.Name, Role: s.Role, State: state, Reason: reason, Harness: s.Harness,
			Dir: s.Dir, Repo: s.Repo, Branch: s.Branch, Tmux: s.TmuxSession, Ppz: s.PpzHandle,
			Unread: unread[s.PpzHandle], Age: fmtAge(s.CreatedAt), Cmd: shJoin(s.Argv),
		}
		if s.SessionUUID != "" {
			if u := loadUsage(s.SessionUUID); u != nil {
				r.Model, r.CtxPct, r.FivePct = u.Model, u.CtxPct, u.FiveHrPct
				if !u.FiveHrReset.IsZero() {
					r.FiveEnd = u.FiveHrReset.Local().Format("15:04")
				}
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func printRows(rows []lsRow) {
	if len(rows) == 0 {
		fmt.Println("no agents. spawn one: muster spawn <name> [--] <command>")
		return
	}
	w := 4
	for _, r := range rows {
		if len(r.Name) > w {
			w = len(r.Name)
		}
	}
	fmt.Printf("%-*s  %-2s %-8s %-9s %-6s %-4s %s\n", w, "NAME", "", "STATE", "HARNESS", "UNREAD", "AGE", "DIR")
	for _, r := range rows {
		reason := ""
		if r.Reason != "" && r.Reason != "heartbeat" {
			reason = " (" + truncate(r.Reason, 30) + ")"
		}
		h := r.Harness
		if h == "" {
			h = "-"
		}
		fmt.Printf("%-*s  %-2s %-8s %-9s %-6d %-4s %s%s\n",
			w, r.Name, stateGlyph(r.State), r.State, h, r.Unread, r.Age, collapseHome(r.Dir), reason)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func collapseHome(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func cmdLs(args []string) int {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	watch := fs.Bool("watch", false, "refresh every 2s")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	for {
		rows, err := gatherRows()
		if err != nil {
			return fail(err)
		}
		if *asJSON {
			b, _ := json.MarshalIndent(rows, "", "  ")
			fmt.Println(string(b))
		} else {
			if *watch {
				fmt.Print("\033[2J\033[H") // clear; fine for a poll loop
			}
			printRows(rows)
		}
		if !*watch {
			return 0
		}
		time.Sleep(2 * time.Second)
	}
}

// ---- attach / kill / resume -------------------------------------------------

func cmdAttach(args []string) int {
	if len(args) != 1 {
		return fail(errf("usage: muster attach <name>"))
	}
	s, err := loadSpec(args[0])
	if err != nil {
		return fail(errf("no such agent %q", args[0]))
	}
	if !tmuxHasSession(s.TmuxSession) {
		return fail(errf("agent %q is dead — muster resume %s", s.Name, s.Name))
	}
	tmuxPath, target := tmuxBin(), "="+s.TmuxSession
	if os.Getenv("TMUX") != "" {
		if out, err := tmuxRun("switch-client", "-t", target); err != nil {
			return fail(errf("switch-client: %s", out))
		}
		return 0
	}
	// outside tmux: replace ourselves with the attach client
	argv := []string{tmuxPath}
	if extra := os.Getenv("MUSTER_TMUX_ARGS"); extra != "" {
		argv = append(argv, strings.Fields(extra)...)
	}
	argv = append(argv, "attach", "-t", target)
	path, err := lookPath(tmuxPath)
	if err != nil {
		return fail(err)
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		return fail(errf("exec tmux: %v", err))
	}
	return 0 // unreachable
}

func lookPath(bin string) (string, error) {
	if strings.Contains(bin, "/") {
		return bin, nil
	}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(d, bin)
		if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errf("%s not found in PATH", bin)
}

func cmdKill(args []string) int {
	fs := flag.NewFlagSet("kill", flag.ExitOnError)
	rm := fs.Bool("rm", false, "also remove worktree + spec (refuses if dirty)")
	force := fs.Bool("force", false, "remove even with uncommitted/unpushed work")
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return fail(errf("usage: muster kill <name> [--rm] [--force]"))
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no such agent %q", name))
	}
	if tmuxHasSession(s.TmuxSession) {
		if err := tmuxKillSession(s.TmuxSession); err != nil {
			return fail(err)
		}
		fmt.Println("killed tmux session", s.TmuxSession)
	}
	if !*rm {
		fmt.Printf("spec kept — `muster resume %s` restarts it exactly; `muster kill %s --rm` deletes\n", name, name)
		return 0
	}
	if s.Worktree != "" {
		if why := worktreeDirty(s.Worktree); why != "" && !*force {
			return fail(errf("refusing to remove worktree %s: %s (use --force)", s.Worktree, why))
		}
		if err := worktreeRemove(s.Repo, s.Worktree); err != nil {
			if !*force {
				return fail(err)
			}
			_ = os.RemoveAll(s.Worktree)
		}
		fmt.Println("removed worktree", s.Worktree)
	}
	if s.SessionUUID != "" {
		clearStatus(s.SessionUUID)
		clearEvents(s.SessionUUID)
	}
	if err := deleteSpec(name); err != nil {
		return fail(err)
	}
	fmt.Println("removed agent", name)
	return 0
}

func cmdResume(args []string) int {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	all := fs.Bool("all", false, "resume every dead agent")
	noPpz := fs.Bool("no-ppz", false, "don't wrap in ppz terminal share")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var targets []*AgentSpec
	if *all {
		specs, err := listSpecs()
		if err != nil {
			return fail(err)
		}
		for _, s := range specs {
			if !tmuxHasSession(s.TmuxSession) {
				targets = append(targets, s)
			}
		}
		if len(targets) == 0 {
			fmt.Println("nothing to resume — all agents alive")
			return 0
		}
	} else {
		if fs.NArg() != 1 {
			return fail(errf("usage: muster resume <name> | muster resume --all"))
		}
		s, err := loadSpec(fs.Arg(0))
		if err != nil {
			return fail(errf("no such agent %q", fs.Arg(0)))
		}
		if tmuxHasSession(s.TmuxSession) {
			return fail(errf("agent %q is already running — muster attach %s", s.Name, s.Name))
		}
		targets = []*AgentSpec{s}
	}
	rc := 0
	for _, s := range targets {
		if _, err := os.Stat(s.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "muster: %s: dir %s is gone, skipping\n", s.Name, s.Dir)
			rc = 1
			continue
		}
		s.ResumedAt = time.Now()
		if launch(s, true, *noPpz, "") != 0 {
			rc = 1
		}
	}
	return rc
}

// ---- messaging / cron -------------------------------------------------------

func requirePpz() error {
	if ppzBin() == "" {
		return errf("ppz CLI not found (install it or set MUSTER_PPZ=/path/to/ppz)")
	}
	if !ppzReady() {
		return errf("ppz daemon not running or not logged in — ppz daemon start && ppz login <server>")
	}
	return nil
}

func agentHandle(name string) (string, error) {
	s, err := loadSpec(name)
	if err != nil {
		return "", errf("no such agent %q", name)
	}
	if s.PpzHandle == "" {
		return "", errf("agent %q is not on the mesh (spawned without ppz)", name)
	}
	return s.PpzHandle, nil
}

func cmdSend(args []string) int {
	if err := requirePpz(); err != nil {
		return fail(err)
	}
	if len(args) < 2 {
		return fail(errf("usage: muster send <name> <text...>"))
	}
	h, err := agentHandle(args[0])
	if err != nil {
		return fail(err)
	}
	if err := ppzSend(h, strings.Join(args[1:], " "), "--request-ack"); err != nil {
		return fail(err)
	}
	fmt.Printf("sent to %s — ppz nudges the agent when it goes idle; ack lands in mstrctl inbox on read\n", args[0])
	return 0
}

func cmdBroadcast(args []string) int {
	if err := requirePpz(); err != nil {
		return fail(err)
	}
	if len(args) < 1 {
		return fail(errf("usage: muster broadcast <text...>"))
	}
	specs, err := listSpecs()
	if err != nil {
		return fail(err)
	}
	text := strings.Join(args, " ")
	n := 0
	for _, s := range specs {
		if s.PpzHandle == "" || !tmuxHasSession(s.TmuxSession) {
			continue
		}
		if err := ppzSend(s.PpzHandle, text); err != nil {
			fmt.Fprintf(os.Stderr, "muster: %s: %v\n", s.Name, err)
			continue
		}
		n++
	}
	fmt.Printf("broadcast to %d live agents\n", n)
	return 0
}

func cmdInbox(args []string) int {
	if err := requirePpz(); err != nil {
		return fail(err)
	}
	if len(args) != 1 {
		return fail(errf("usage: muster inbox <name>"))
	}
	h, err := agentHandle(args[0])
	if err != nil {
		return fail(err)
	}
	out, err := ppzCmd(ctlSession, "reread", h+".inbox").CombinedOutput()
	if err != nil && len(strings.TrimSpace(string(out))) == 0 {
		fmt.Println("(empty)")
		return 0
	}
	fmt.Print(string(out))
	return 0
}

func cmdCron(args []string) int {
	if err := requirePpz(); err != nil {
		return fail(err)
	}
	if len(args) < 1 {
		return fail(errf("usage: muster cron add <name> (--every D|--cron E|--at T) <prompt...> | muster cron ls | muster cron rm <id>"))
	}
	switch args[0] {
	case "ls":
		out, _ := ppzCmd(ctlSession, "schedule", "ls").CombinedOutput()
		fmt.Print(string(out))
		return 0
	case "rm":
		if len(args) != 2 {
			return fail(errf("usage: muster cron rm <id>"))
		}
		out, err := ppzCmd(ctlSession, "schedule", "rm", args[1]).CombinedOutput()
		fmt.Print(string(out))
		if err != nil {
			return 1
		}
		return 0
	case "add":
		fs := flag.NewFlagSet("cron add", flag.ExitOnError)
		every := fs.String("every", "", "interval (e.g. 4h)")
		cronExpr := fs.String("cron", "", "5-field cron expr")
		at := fs.String("at", "", "one-shot time (RFC3339 or +dur)")
		if len(args) < 2 {
			return fail(errf("usage: muster cron add <name> (--every D|--cron E|--at T) <prompt...>"))
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		h, err := agentHandle(name)
		if err != nil {
			return fail(err)
		}
		prompt := strings.Join(fs.Args(), " ")
		if prompt == "" {
			return fail(errf("empty prompt"))
		}
		var extra []string
		switch {
		case *every != "":
			extra = []string{"--every", *every}
		case *cronExpr != "":
			extra = []string{"--cron", *cronExpr}
		case *at != "":
			extra = []string{"--at", *at}
		default:
			return fail(errf("need one of --every/--cron/--at"))
		}
		if err := ppzSend(h, prompt, extra...); err != nil {
			return fail(err)
		}
		fmt.Printf("scheduled for %s — fires server-side even while this machine sleeps;\nppz nudges the agent to read it next time it's idle\n", name)
		return 0
	}
	return fail(errf("unknown cron subcommand %q", args[0]))
}

// ---- standup ------------------------------------------------------------------

// cmdStandup asks every live mesh agent for a status report. Just a
// broadcast with a good prompt — replies land in the mstrctl inbox like any
// other team traffic and show in the mesh view (M).
func cmdStandup(args []string) int {
	if err := requirePpz(); err != nil {
		return fail(err)
	}
	specs, err := listSpecs()
	if err != nil {
		return fail(err)
	}
	prompt := "STANDUP: reply to mstrctl now (ppz send mstrctl '...') with max 5 lines: " +
		"current task, progress, blockers, what's next."
	n := 0
	for _, s := range specs {
		if s.PpzHandle == "" || !tmuxHasSession(s.TmuxSession) {
			continue
		}
		if err := ppzSend(s.PpzHandle, prompt); err != nil {
			fmt.Fprintf(os.Stderr, "muster: %s: %v\n", s.Name, err)
			continue
		}
		n++
	}
	fmt.Printf("standup requested from %d agents — idle agents answer right away, busy ones when they finish a step.\nreplies: muster ui mesh view (M), or: ppz reread mstrctl.inbox --since 1h\n", n)
	return 0
}

// ---- menu / init / doctor ---------------------------------------------------

func cmdMenu(args []string) int {
	rows, err := gatherRows()
	if err != nil {
		return fail(err)
	}
	if len(rows) == 0 {
		_, _ = tmuxRun("display-message", "muster: no agents")
		return 0
	}
	menu := []string{"display-menu", "-T", " muster ", "-x", "C", "-y", "C"}
	keys := "1234567890abcdefghij"
	for i, r := range rows {
		if i >= len(keys) {
			break
		}
		label := fmt.Sprintf("%s %-12s %s", stateGlyph(r.State), r.Name, r.State)
		act := "switch-client -t =" + r.Tmux
		if r.State == "dead" {
			act = fmt.Sprintf("run-shell '%s resume %s'", selfExe(), r.Name)
			label = fmt.Sprintf("%s %-12s resume?", stateGlyph(r.State), r.Name)
		}
		menu = append(menu, label, string(keys[i]), act)
	}
	if out, err := tmuxRun(menu...); err != nil {
		return fail(errf("display-menu (run inside tmux): %s", out))
	}
	return 0
}

func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return "muster"
	}
	return exe
}

func cmdInit(args []string) int {
	p, err := writeHooksSettings()
	if err != nil {
		return fail(err)
	}
	fmt.Println("wrote", p)
	writeDefaultConfig()
	fmt.Println("config:", configPath(), "— skip_permissions, repo_roots, repo_depth")
	// agents must find `ppz` themselves (subs read / send); shell profiles
	// rebuild PATH (macOS path_helper), so a symlink in a standard dir
	// beats env injection
	if pz := ppzBin(); pz != "" {
		if _, err := exec.LookPath("ppz"); err != nil {
			home, _ := os.UserHomeDir()
			link := filepath.Join(home, ".local", "bin", "ppz")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err == nil {
				if err := os.Symlink(pz, link); err == nil {
					fmt.Println("symlinked", pz, "->", link, "(agents need ppz on PATH)")
				}
			}
		}
	}
	fmt.Println("(passed via --settings to every claude agent muster spawns — your own settings are untouched)")
	fmt.Println()
	fmt.Println("add to your tmux.conf for the picker:")
	fmt.Printf("  bind-key g run-shell '%s menu'\n", selfExe())
	fmt.Println()
	fmt.Println("optional env (shell profile):")
	fmt.Println(`  export MUSTER_DEFAULT_CMD="claude"      # spawn default command`)
	fmt.Println(`  export MUSTER_PPZ=/path/to/ppz          # if not in PATH`)
	fmt.Println(`  export MUSTER_SKIP_PERMISSIONS=0        # re-enable permission prompts`)
	return 0
}

func cmdDoctor(args []string) int {
	ok := func(b bool) string {
		if b {
			return "ok"
		}
		return "MISSING"
	}
	_, tmuxErr := lookPath(tmuxBin())
	fmt.Printf("tmux           %s\n", ok(tmuxErr == nil))
	_, claudeErr := lookPath("claude")
	fmt.Printf("claude         %s\n", ok(claudeErr == nil))
	fmt.Printf("ppz binary     %s (%s)\n", ok(ppzBin() != ""), ppzBin())
	fmt.Printf("ppz mesh       %s\n", ok(ppzReady()))
	fmt.Printf("hooks settings %s (%s)\n", ok(hooksSettingsIfPresent() != ""), hooksSettingsPath())
	fmt.Printf("state dir      %s\n", dataDir())
	specs, _ := listSpecs()
	fmt.Printf("agents         %d\n", len(specs))
	if tmuxErr != nil || claudeErr != nil {
		return 1
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }
