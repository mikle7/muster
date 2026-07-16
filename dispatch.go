package main

// Dispatch: the deterministic router. The job the "master agent" pattern
// was doing — clear an agent, brief it, hand it the task — is pure
// bookkeeping that needs no LLM and no inbox queue: muster does it in
// milliseconds of local reads.
//
// Grammar (one task per line; palette and CLI share it):
//
//   fix the login redirect @backend !sonnet /triage-logs #gamesrv
//
//   @word   → target: an agent, a template (pool), or "ask" (ephemeral lane)
//   !word   → model override
//   /word   → skill to invoke (must look like a skill name, so paths in
//             prose don't false-positive)
//   #word   → project (when not the current space)
//   trailing "?" with no target → the ask lane (questions want answers,
//             not teammates)
//
// Epics: a header line plus "- " bullets fans out — each bullet becomes
// one task carrying the epic header as shared context, inheriting the
// header's tokens unless it overrides them:
//
//   ship the auth revamp #gamesrv !sonnet
//   - token rotation endpoints @backend
//   - login flow update @frontend
//   - session store migration @infra
//
// Resolution per task, in order: explicit agent → retask it when idle
// (fresh seeded context — the whole point), queue to it when busy;
// template → claim a warm spare, else retask the freshest idle member,
// else grow the pool under its cap, else refuse loudly; no target → the
// project's single idle agent, else the project's single template, else
// ask (for questions) or refuse. Refusals are always explicit — dispatch
// never silently queues work nobody will start.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type dispatchTask struct {
	Text     string // the task text with tokens stripped
	Epic     string // shared epic header ("" for standalone tasks)
	Target   string // @token ("" = auto)
	Model    string // !token
	Skill    string // /token
	Proj     string // #token
	Question bool   // line ended with "?"
}

// skillTokenRe: a /word only counts as a skill token if it looks like a
// skill name — lowercase, digits, dashes, no dots or further slashes — so
// "/var/log" or "src/foo.go" in prose never get eaten.
var skillTokenRe = regexp.MustCompile(`^/[a-z0-9][a-z0-9-]*$`)

// parseTaskLine splits one line into its task text and routing tokens.
func parseTaskLine(line string) dispatchTask {
	var t dispatchTask
	var words []string
	for _, w := range strings.Fields(line) {
		switch {
		case len(w) > 1 && strings.HasPrefix(w, "@"):
			t.Target = strings.TrimPrefix(w, "@")
		case len(w) > 1 && strings.HasPrefix(w, "!"):
			t.Model = strings.TrimPrefix(w, "!")
		case len(w) > 1 && strings.HasPrefix(w, "#"):
			t.Proj = strings.TrimPrefix(w, "#")
		case skillTokenRe.MatchString(w):
			t.Skill = strings.TrimPrefix(w, "/")
		default:
			words = append(words, w)
		}
	}
	t.Text = strings.Join(words, " ")
	t.Question = strings.HasSuffix(strings.TrimSpace(t.Text), "?")
	return t
}

func isBullet(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ")
}

// parseDispatch turns palette/CLI text into tasks. With bullets, non-bullet
// lines form the epic header whose tokens become per-bullet defaults;
// without bullets, every non-empty line is an independent task.
func parseDispatch(text string) []dispatchTask {
	lines := strings.Split(text, "\n")
	hasBullets := false
	for _, l := range lines {
		if isBullet(l) {
			hasBullets = true
			break
		}
	}
	var tasks []dispatchTask
	if !hasBullets {
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			tasks = append(tasks, parseTaskLine(l))
		}
		return tasks
	}
	var headerParts []string
	var header dispatchTask
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !isBullet(l) {
			h := parseTaskLine(l)
			if h.Text != "" {
				headerParts = append(headerParts, h.Text)
			}
			// later header lines may add tokens; first non-empty wins per slot
			if header.Target == "" {
				header.Target = h.Target
			}
			if header.Model == "" {
				header.Model = h.Model
			}
			if header.Skill == "" {
				header.Skill = h.Skill
			}
			if header.Proj == "" {
				header.Proj = h.Proj
			}
			continue
		}
		s := strings.TrimSpace(l)
		t := parseTaskLine(strings.TrimSpace(s[2:]))
		t.Epic = strings.Join(headerParts, " ")
		if t.Target == "" {
			t.Target = header.Target
		}
		if t.Model == "" {
			t.Model = header.Model
		}
		if t.Skill == "" {
			t.Skill = header.Skill
		}
		if t.Proj == "" {
			t.Proj = header.Proj
		}
		tasks = append(tasks, t)
	}
	return tasks
}

// brief renders the text actually delivered to an agent.
func (t dispatchTask) brief() string {
	b := t.Text
	if t.Epic != "" {
		b = "EPIC: " + t.Epic + "\nYOUR PART: " + t.Text +
			"\n(other parts of this epic are with other teammates — coordinate in the room if they overlap)"
	}
	if t.Skill != "" {
		b = "/" + t.Skill + " " + b
	}
	return b
}

// ---- resolution ---------------------------------------------------------

type dispatchAction struct {
	Kind     string // "ask" | "retask" | "claim" | "send" | "spawn" | "refuse"
	Agent    string // retask/claim/send target, or the minted spawn name
	Template string // spawn/claim/retask-via-pool: which template
	Why      string // human line for the preview / refusal
	Task     dispatchTask
}

// resolveDispatch decides what one task becomes. Pure over its inputs —
// the palette preview and the executor call the exact same function, so
// what the preview shows is what enter does.
func resolveDispatch(t dispatchTask, rows []lsRow, templates []Template, projects []Project, space string) dispatchAction {
	act := dispatchAction{Task: t}
	proj := t.Proj
	if proj == "" {
		for _, p := range projects {
			if p.Path == space {
				proj = p.Name
			}
		}
	}
	byName := map[string]lsRow{}
	for _, r := range rows {
		if !r.Remote && !r.Ask {
			byName[r.Name] = r
		}
	}
	tmplByName := map[string]Template{}
	for _, tp := range templates {
		tmplByName[tp.Name] = tp
	}

	if t.Target == "ask" {
		act.Kind = "ask"
		act.Why = "ephemeral one-shot"
		return act
	}
	if t.Target != "" {
		if r, ok := byName[t.Target]; ok {
			return resolveAgentTarget(act, r)
		}
		if tp, ok := tmplByName[t.Target]; ok {
			return resolvePool(act, tp, rows)
		}
		act.Kind = "refuse"
		act.Why = "@" + t.Target + " matches no agent or template"
		return act
	}

	// no explicit target — look at the project's own bench first
	var idle []lsRow
	var projTemplates []Template
	for _, r := range rows {
		if r.Remote || r.Ask || r.Harness != "claude" {
			continue
		}
		if projectFor(projects, r) != proj || proj == "" {
			continue
		}
		if r.State == "idle" {
			idle = append(idle, r)
		}
	}
	for _, tp := range templates {
		if tp.Project == proj && proj != "" {
			projTemplates = append(projTemplates, tp)
		}
	}
	switch {
	case t.Question:
		// a question wants an answer, not a teammate — the ask lane wins
		// over every implicit route (an explicit @target still overrides,
		// handled above)
		act.Kind = "ask"
		act.Why = "question → ask lane"
		return act
	case len(idle) == 1:
		return resolveAgentTarget(act, idle[0])
	case len(projTemplates) == 1:
		return resolvePool(act, projTemplates[0], rows)
	case len(idle) > 1:
		act.Kind = "refuse"
		names := make([]string, len(idle))
		for i, r := range idle {
			names[i] = r.Name
		}
		act.Why = "several idle agents (" + strings.Join(names, ", ") + ") — pick one with @name"
		return act
	default:
		act.Kind = "refuse"
		act.Why = "no target: add @agent/@template, end with ? for the ask lane, or create a template"
		return act
	}
}

func resolveAgentTarget(act dispatchAction, r lsRow) dispatchAction {
	act.Agent = r.Name
	act.Template = r.Template
	switch r.State {
	case "idle":
		if r.Spare {
			act.Kind = "claim"
			act.Why = "warm spare — already fresh, task lands instantly"
			return act
		}
		act.Kind = "retask"
		act.Why = fmt.Sprintf("idle at ctx %d%% → fresh seeded context", int(r.CtxPct))
	case "dead":
		act.Kind = "refuse"
		act.Why = r.Name + " is dead — resume it first (r), or route to a template"
	default:
		act.Kind = "send"
		act.Why = r.Name + " is " + r.State + " — task queues for when it surfaces"
	}
	return act
}

func resolvePool(act dispatchAction, tp Template, rows []lsRow) dispatchAction {
	act.Template = tp.Name
	live := 0
	var spare, best *lsRow
	for i := range rows {
		r := &rows[i]
		if r.Template != tp.Name || r.Remote || r.Ask {
			continue
		}
		if r.State != "dead" {
			live++
		}
		if r.State != "idle" {
			continue
		}
		if r.Spare {
			if spare == nil {
				spare = r
			}
			continue
		}
		// the freshest idle member (lowest context) takes the task
		if best == nil || r.CtxPct < best.CtxPct {
			best = r
		}
	}
	switch {
	case spare != nil:
		act.Kind = "claim"
		act.Agent = spare.Name
		act.Why = "warm spare — already fresh, task lands instantly"
	case best != nil:
		act.Kind = "retask"
		act.Agent = best.Name
		act.Why = fmt.Sprintf("idle at ctx %d%% → fresh seeded context", int(best.CtxPct))
	case live < tp.cap():
		act.Kind = "spawn"
		act.Why = fmt.Sprintf("pool %d/%d busy → hiring", live, tp.cap())
	default:
		act.Kind = "refuse"
		act.Why = fmt.Sprintf("%s pool full (%d/%d) and everyone busy — raise the cap or wait", tp.Name, live, tp.cap())
	}
	return act
}

// ---- execution ----------------------------------------------------------

// executeDispatch performs one resolved action. Slow parts (retask's
// idle-wait+/clear cycle, delivery after a spawn boots) run as background
// muster subprocesses so the palette closes instantly.
func executeDispatch(act dispatchAction, projects []Project, space string) (string, error) {
	t := act.Task
	switch act.Kind {
	case "refuse":
		return "", errf("%s: %s", clip(t.Text, 40), act.Why)
	case "ask":
		dir := space
		if t.Proj != "" {
			for _, p := range projects {
				if p.Name == t.Proj {
					dir = p.Path
				}
			}
		}
		if dir == "" {
			dir, _ = os.Getwd()
		}
		a := &Ask{Question: t.Text, Skill: t.Skill, Model: t.Model, Dir: dir}
		if err := startAsk(a); err != nil {
			return "", err
		}
		return "ask " + a.ID + " running (" + a.Model + ")", nil
	case "send":
		brief := t.brief()
		if s, err := loadSpec(act.Agent); err == nil && s.PpzHandle != "" && ppzReady() {
			if err := ppzSend(s.PpzHandle, brief, "--request-ack"); err != nil {
				return "", err
			}
			return act.Agent + " ← queued (mesh nudges it when idle)", nil
		}
		// no mesh: type it — claude queues typed input until the turn ends
		if s, err := loadSpec(act.Agent); err == nil {
			if err := tmuxSendLine(s.TmuxSession, brief); err != nil {
				return "", err
			}
			return act.Agent + " ← typed (runs after its current turn)", nil
		}
		return "", errf("no agent %q", act.Agent)
	case "claim":
		s, err := loadSpec(act.Agent)
		if err != nil {
			return "", err
		}
		s.Spare = false
		if err := saveSpec(s); err != nil {
			return "", err
		}
		if t.Model != "" && t.Model != s.Model {
			s.Model = t.Model
			_ = saveSpec(s)
			_ = tmuxSendLine(s.TmuxSession, "/model "+t.Model)
			time.Sleep(300 * time.Millisecond)
		}
		if err := tmuxSendLine(s.TmuxSession, t.brief()); err != nil {
			return "", err
		}
		// backfill the bench in the background
		if tp := findTemplate(s.Template); tp != nil && tp.Warm {
			_ = exec.Command(selfExe(), "spawn", "--as", tp.Name, "--spare").Start()
		}
		return act.Agent + " ← claimed warm spare, task landed", nil
	case "retask":
		args := []string{"retask", act.Agent}
		if t.Model != "" {
			args = append(args, "--model", t.Model)
		}
		args = append(args, t.brief())
		if err := exec.Command(selfExe(), args...).Start(); err != nil {
			return "", err
		}
		return act.Agent + " ← retasking (fresh seeded context)", nil
	case "spawn":
		specs, _ := listSpecs()
		name := poolName(act.Template, specs)
		writePending(name, "spawning from "+act.Template)
		spawnArgs := []string{"spawn", name, "--as", act.Template}
		if t.Model != "" {
			spawnArgs = append(spawnArgs, "--model", t.Model)
		}
		if t.Proj != "" {
			for _, p := range projects {
				if p.Name == t.Proj {
					spawnArgs = append(spawnArgs, "-C", p.Path)
				}
			}
		}
		out, err := exec.Command(selfExe(), spawnArgs...).CombinedOutput()
		if err != nil {
			clearPending(name)
			return "", errf("spawn %s: %s", name, firstLine(string(out)))
		}
		// deliver once claude is actually up (its SessionStart hook fires)
		_ = exec.Command(selfExe(), "deliver", name, t.brief()).Start()
		return name + " ← hired, task lands when it boots", nil
	}
	return "", errf("unknown dispatch kind %q", act.Kind)
}

func cmdDispatch(args []string) int {
	fs := flag.NewFlagSet("dispatch", flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "show the routing plan without acting")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: muster dispatch "<task…>"
tokens: @agent|@template|@ask  !model  /skill  #project · "?" ending routes to the ask lane
epics:  header line + "- " bullets fan out to multiple agents`)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	text := strings.Join(fs.Args(), " ")
	// a single argument may carry real newlines (palette, heredocs)
	if fs.NArg() == 1 {
		text = fs.Arg(0)
	}
	if strings.TrimSpace(text) == "" {
		fs.Usage()
		return 2
	}
	tasks := parseDispatch(text)
	if len(tasks) == 0 {
		return fail(errf("nothing to dispatch"))
	}
	rows, err := gatherRows()
	if err != nil {
		return fail(err)
	}
	templates, projects, space := loadTemplates(), loadProjects(), currentSpace()
	rc := 0
	for _, t := range tasks {
		act := resolveDispatch(t, rows, templates, projects, space)
		if *dry {
			fmt.Printf("→ %-7s %-12s %s  (%s)\n", act.Kind, act.Agent+act.Template, clip(t.Text, 44), act.Why)
			continue
		}
		msg, err := executeDispatch(act, projects, space)
		if err != nil {
			fmt.Fprintln(os.Stderr, "muster:", err)
			rc = 1
			continue
		}
		fmt.Println("→", msg)
	}
	return rc
}

// cmdDeliver (internal) types a brief into an agent's pane once its claude
// is actually up — the SessionStart hook writing a status file is the
// "it booted" signal (a fresh claude fires SessionStart immediately, then
// sits at its prompt; there is no Stop/idle until the first turn ends).
func cmdDeliver(args []string) int {
	if len(args) < 2 {
		return fail(errf("usage: muster deliver <name> <text…> (internal)"))
	}
	name := args[0]
	text := strings.Join(args[1:], " ")
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no agent %q", name))
	}
	booted := func() bool {
		s2, err := loadSpec(name)
		if err != nil {
			return false
		}
		st := loadStatus(s2.SessionUUID)
		return st != nil && st.TS.After(s2.CreatedAt.Add(-time.Second))
	}
	if !waitFor(booted, 120*time.Second, time.Second) {
		fmt.Fprintln(os.Stderr, "muster: deliver: no hook signal from", name, "after 120s — typing anyway")
	}
	time.Sleep(1500 * time.Millisecond) // let the input box actually accept keys
	if err := tmuxSendLine(s.TmuxSession, text); err != nil {
		return fail(err)
	}
	fmt.Println("delivered to", name)
	return 0
}

// ---- pending rows (optimistic UI) ----------------------------------------

// The sidebar shows a spawn the instant dispatch decides it, not two ticks
// later: dispatch writes a pending marker, gatherRows renders it as a
// "pending" row until the real spec appears (or 45s passes).

type pendingRow struct {
	Name string    `json:"name"`
	Note string    `json:"note"`
	TS   time.Time `json:"ts"`
}

func pendingPath() string { return filepath.Join(dataDir(), "pending.json") }

func writePending(name, note string) {
	rows := loadPendingRaw()
	rows = append(rows, pendingRow{Name: name, Note: note, TS: time.Now()})
	b, _ := json.Marshal(rows)
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = atomicWrite(pendingPath(), b)
}

func clearPending(name string) {
	rows := loadPendingRaw()
	kept := rows[:0]
	for _, r := range rows {
		if r.Name != name {
			kept = append(kept, r)
		}
	}
	b, _ := json.Marshal(kept)
	_ = atomicWrite(pendingPath(), b)
}

func loadPendingRaw() []pendingRow {
	b, err := os.ReadFile(pendingPath())
	if err != nil {
		return nil
	}
	var rows []pendingRow
	_ = json.Unmarshal(b, &rows)
	return rows
}

// loadPending returns live pending markers: young, and not yet real specs.
func loadPending(specs []*AgentSpec) []pendingRow {
	have := map[string]bool{}
	for _, s := range specs {
		have[s.Name] = true
	}
	var out []pendingRow
	for _, r := range loadPendingRaw() {
		if have[r.Name] || time.Since(r.TS) > 45*time.Second {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---- warm spares ----------------------------------------------------------

func spareMarkPath(tmpl string) string {
	return filepath.Join(dataDir(), "spare", tmpl+".json")
}

func spareInFlight(tmpl string) bool {
	b, err := os.ReadFile(spareMarkPath(tmpl))
	if err != nil {
		return false
	}
	var mk map[string]time.Time
	if json.Unmarshal(b, &mk) != nil {
		return false
	}
	return time.Since(mk["ts"]) < 3*time.Minute
}

func setSpareMark(tmpl string) {
	if err := os.MkdirAll(filepath.Dir(spareMarkPath(tmpl)), 0o755); err != nil {
		return
	}
	b, _ := json.Marshal(map[string]time.Time{"ts": time.Now()})
	_ = atomicWrite(spareMarkPath(tmpl), b)
}

// maybeWarmSpares keeps one booted, briefed, primed spare per warm template
// so dispatch can hand a task to an already-running fresh agent instantly.
// Called from the TUI's 2s tick; spawns in the background, one in flight
// per template.
func maybeWarmSpares(rows []lsRow) {
	templates := loadTemplates()
	for _, tp := range templates {
		if !tp.Warm || spareInFlight(tp.Name) {
			continue
		}
		live, spares := 0, 0
		for _, r := range rows {
			if r.Template != tp.Name || r.Remote || r.Ask {
				continue
			}
			if r.State != "dead" {
				live++
				if r.Spare {
					spares++
				}
			}
		}
		if spares > 0 || live >= tp.cap() {
			continue
		}
		setSpareMark(tp.Name)
		_ = exec.Command(selfExe(), "spawn", "--as", tp.Name, "--spare").Start()
	}
}
