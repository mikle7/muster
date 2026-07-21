package main

// Context packs: the seed a fresh context starts from, so nobody retypes
// "the basic bits about how the project is connected" ever again.
//
// Two layers, deliberately separate:
//   - PRIMER  (.muster/primer.md at the repo root, committed): the durable
//     orientation — how the project is wired, services/ports, where things
//     live. Human-authored, agent-maintainable, travels with the repo.
//   - LESSONS (<state>/lessons/<proj>.md, muster state): accumulated
//     caveats agents discover while working ("the seed data script must run
//     before tests", "auth service logs lag ~30s"). Append-only via
//     `muster lesson add` so concurrent agents can't clobber each other.
//
// Injection points: the system prompt at spawn (injected(), survives /clear
// as a process flag) and the SessionStart hook on /clear (latest lessons —
// they grow after spawn; see sessionStartContext in status.go).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// primer size cap in the spawn briefing; lessons tail cap alongside it.
const primerCap = 6000
const lessonsCap = 2500

func lessonsPath(proj string) string {
	return filepath.Join(dataDir(), "lessons", proj+".md")
}

// primerPath locates the project's primer for an agent spec: the repo root
// (worktree agents read their SOURCE repo's primer — a fresh worktree has
// it too, but the repo's copy is the canonical one), else the registered
// project's path, else the agent dir itself.
func primerPath(s *AgentSpec) string {
	dirs := []string{}
	if s.Repo != "" {
		dirs = append(dirs, s.Repo)
	}
	if s.Dir != "" {
		dirs = append(dirs, s.Dir)
		if root := gitRoot(s.Dir); root != "" && root != s.Dir {
			dirs = append(dirs, root)
		}
	}
	if proj := projectFor(loadProjects(), lsRow{Dir: s.Dir, Repo: s.Repo}); proj != "" {
		for _, p := range loadProjects() {
			if p.Name == proj {
				dirs = append(dirs, p.Path)
			}
		}
	}
	for _, d := range dirs {
		p := filepath.Join(d, ".muster", "primer.md")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// readPrimer returns the primer text for spec, role-focused: a primer may
// contain `## <role/template>` sections; the agent's own section (matched
// against its template name, then role words) is kept in full, other role
// sections are dropped, and everything outside role sections (the shared
// part) always survives. Head-capped — the top of a primer is the "how it
// all connects" part worth keeping when space runs out.
func readPrimer(s *AgentSpec) string {
	p := primerPath(s)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	text := focusPrimer(string(b), s.Template, s.Role)
	return capHead(strings.TrimSpace(text), primerCap)
}

// focusPrimer keeps shared content plus the section matching who this agent
// is; other agents' `## role` sections are dropped. A primer with no `## `
// sections passes through untouched.
func focusPrimer(text, template, role string) string {
	lines := strings.Split(text, "\n")
	var out []string
	keep := true
	matched := strings.ToLower(strings.TrimSpace(template + " " + role))
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			sec := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "## ")))
			keep = sec != "" && matched != "" && strings.Contains(matched, sec)
			if keep {
				out = append(out, l)
			}
			continue
		}
		if keep {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// readLessons returns the tail of the project's lessons file (latest wins).
func readLessons(proj string, capChars int) string {
	if proj == "" {
		return ""
	}
	b, err := os.ReadFile(lessonsPath(proj))
	if err != nil {
		return ""
	}
	return capTail(strings.TrimSpace(string(b)), capChars)
}

// capHead/capTail cap s to an n-BYTE budget (these feed the --append-system-
// prompt / SessionStart additionalContext, both byte-limited). n is a byte
// budget, not a rune count — but the cut must land on a rune boundary, or a
// multibyte UTF-8 char gets split mid-sequence and the injected prompt shows a
// replacement glyph (garbles identity/primer/lessons for any non-ASCII text).
func capHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) { // back off to the start of a rune
		n--
	}
	return s[:n] + "…"
}

func capTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) { // advance to the start of a rune
		i++
	}
	return "…" + s[i:]
}

// contextPack builds the seeding block appended to a fresh agent's system
// prompt: primer + lessons + the instruction to keep feeding the lessons
// file. Empty string when the project has neither (nothing to say).
func contextPack(s *AgentSpec) string {
	proj := projectFor(loadProjects(), lsRow{Dir: s.Dir, Repo: s.Repo})
	primer := readPrimer(s)
	lessons := readLessons(proj, lessonsCap)
	var b strings.Builder
	if primer != "" {
		b.WriteString("PROJECT PRIMER (from .muster/primer.md — how this project is wired):\n")
		b.WriteString(primer)
	}
	if lessons != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("LESSONS LEARNED here by the team (newest last):\n")
		b.WriteString(lessons)
	}
	if proj != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("When you discover a non-obvious constraint, gotcha, or hard-won fact about " +
			"this project, record it in one line for every future agent: muster lesson add " +
			shQuote(proj) + " '<the lesson>'. Keep lessons short and factual.")
	}
	return b.String()
}

// ---- CLI ---------------------------------------------------------------

func cmdLesson(args []string) int {
	if len(args) < 1 {
		return fail(errf("usage: muster lesson add <project> <text...> | muster lesson ls <project>"))
	}
	switch args[0] {
	case "add":
		if len(args) < 3 {
			return fail(errf("usage: muster lesson add <project> <text...>"))
		}
		proj := args[1]
		if !projectExists(proj) {
			return fail(errf("no project %q (muster project ls)", proj))
		}
		text := strings.Join(args[2:], " ")
		text = strings.ReplaceAll(text, "\n", " ") // one lesson, one line
		by := os.Getenv("MUSTER_AGENT")
		if by == "" {
			by = "you"
		}
		line := fmt.Sprintf("- %s (%s, %s)\n", strings.TrimSpace(text), by, time.Now().Format("2006-01-02"))
		p := lessonsPath(proj)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(err)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fail(err)
		}
		_, werr := f.WriteString(line)
		_ = f.Close()
		if werr != nil {
			return fail(werr)
		}
		fmt.Printf("lesson recorded for #%s — every future fresh context gets it\n", proj)
		return 0
	case "ls":
		if len(args) != 2 {
			return fail(errf("usage: muster lesson ls <project>"))
		}
		b, err := os.ReadFile(lessonsPath(args[1]))
		if err != nil {
			fmt.Println("(no lessons yet — muster lesson add", args[1], "'<text>')")
			return 0
		}
		fmt.Print(string(b))
		return 0
	}
	return fail(errf("unknown lesson subcommand %q", args[0]))
}

func projectExists(name string) bool {
	for _, p := range loadProjects() {
		if p.Name == name {
			return true
		}
	}
	return false
}

// primerAge is the detail-panel staleness glance: "primer 12d · 37 lessons".
func primerInfo(s *AgentSpec) string {
	var parts []string
	if p := primerPath(s); p != "" {
		if fi, err := os.Stat(p); err == nil {
			parts = append(parts, "primer "+fmtAge(fi.ModTime()))
		}
	}
	if proj := projectFor(loadProjects(), lsRow{Dir: s.Dir, Repo: s.Repo}); proj != "" {
		if b, err := os.ReadFile(lessonsPath(proj)); err == nil {
			n := len(splitLines(string(b)))
			if n > 0 {
				parts = append(parts, fmt.Sprintf("%d lessons", n))
			}
		}
	}
	return strings.Join(parts, " · ")
}

// lessonProjects lists projects that have a lessons file (for doctor/debug).
func lessonProjects() []string {
	ents, err := os.ReadDir(filepath.Join(dataDir(), "lessons"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(out)
	return out
}
