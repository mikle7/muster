package main

// The ask lane: fire-and-forget questions answered by an ephemeral,
// fresh-context, skill-armed `claude -p` one-shot. Built for the live-ops
// case ("user X can't get into the game — what's happening?"): one
// keystroke, the right skill, a fast model, an answer in minutes.
//
// Deliberately mesh-less: an ask has no ppz handle, no room subscription,
// no roster in its briefing — it cannot be messaged, pinged, or pulled
// into team chatter. It exists to answer one question and disappear.
//
// Still a real tmux pane (mstr-ask-<id>) while it runs — visible, tailable,
// killable, muster principles intact. The answer is captured from claude's
// --output-format json result and outlives the pane.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Ask struct {
	ID        string    `json:"id"`
	Question  string    `json:"question"`
	Skill     string    `json:"skill,omitempty"`
	Model     string    `json:"model"`
	Dir       string    `json:"dir"`
	Project   string    `json:"project,omitempty"`
	State     string    `json:"state"` // running|done|error
	Answer    string    `json:"answer,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	StartedAt time.Time `json:"started_at"`
	DoneAt    time.Time `json:"done_at,omitempty"`
	Dismissed bool      `json:"dismissed,omitempty"`
	KeepPane  bool      `json:"keep_pane,omitempty"`
}

func asksDir() string          { return filepath.Join(dataDir(), "asks") }
func askPath(id string) string { return filepath.Join(asksDir(), id+".json") }
func askSession(id string) string {
	return tmuxSession("ask-" + id)
}

func loadAsk(id string) (*Ask, error) {
	b, err := os.ReadFile(askPath(id))
	if err != nil {
		return nil, err
	}
	var a Ask
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func saveAsk(a *Ask) error {
	if err := os.MkdirAll(asksDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	return atomicWrite(askPath(a.ID), b)
}

// listAsks returns non-dismissed asks newest-first; done ones older than
// askRetention drop off the sidebar (the JSON stays on disk).
const askRetention = 24 * time.Hour

func listAsks() []*Ask {
	ents, err := os.ReadDir(asksDir())
	if err != nil {
		return nil
	}
	var out []*Ask
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		a, err := loadAsk(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil || a.Dismissed {
			continue
		}
		if a.State != "running" && !a.DoneAt.IsZero() && time.Since(a.DoneAt) > askRetention {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// askModel: the ask lane's default model. Speed is the point (Michael's own
// call: sonnet for triage) — config ask_model / MUSTER_ASK_MODEL override.
func askModel() string {
	if v := os.Getenv("MUSTER_ASK_MODEL"); v != "" {
		return v
	}
	if c := loadConfig(); c.AskModel != "" {
		return c.AskModel
	}
	return "sonnet"
}

func cmdAsk(args []string) int {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	skill := fs.String("skill", "", "skill to invoke (/name is prefixed to the prompt)")
	model := fs.String("model", "", "model (default: config ask_model, else sonnet)")
	dir := fs.String("C", "", "directory to run in (default: current space, else cwd)")
	proj := fs.String("proj", "", "project name to run in (alternative to -C)")
	keep := fs.Bool("keep", false, "keep the pane around after the answer")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: muster ask [--skill s] [--model m] [--proj p|-C dir] <question…>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	question := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if question == "" {
		fs.Usage()
		return 2
	}
	d := *dir
	if d == "" && *proj != "" {
		for _, p := range loadProjects() {
			if p.Name == *proj {
				d = p.Path
			}
		}
		if d == "" {
			return fail(errf("no project %q", *proj))
		}
	}
	if d == "" {
		if d = currentSpace(); d == "" {
			d, _ = os.Getwd()
		}
	}
	m := *model
	if m == "" {
		m = askModel()
	}
	a := &Ask{
		Question: question, Skill: *skill, Model: m, Dir: d,
		KeepPane: *keep,
	}
	if err := startAsk(a); err != nil {
		return fail(err)
	}
	skillNote := ""
	if *skill != "" {
		skillNote = " /" + *skill
	}
	fmt.Printf("ask %s%s (%s in %s) — answer lands as a notification + A in the sidebar\n",
		a.ID, skillNote, m, collapseHome(d))
	return 0
}

// startAsk assigns an id, persists the ask, and spawns its pane. Shared by
// the CLI and dispatch (the palette's ask lane).
func startAsk(a *Ask) error {
	a.ID = strings.ReplaceAll(newUUID(), "-", "")[:8]
	if a.Model == "" {
		a.Model = askModel()
	}
	a.Project = projectFor(loadProjects(), lsRow{Dir: a.Dir})
	a.State = "running"
	a.StartedAt = time.Now()
	a.KeepPane = a.KeepPane || askKeepPane()
	if err := saveAsk(a); err != nil {
		return err
	}
	// the pane runs muster's own ask-run so the capture/notify/reap logic
	// lives in-process, visible in the pane, not in shell glue
	cmd := shQuote(selfExe()) + " ask-run " + a.ID
	if err := tmuxNewSession(askSession(a.ID), a.Dir, cmd, map[string]string{"MUSTER_ASK": a.ID}); err != nil {
		a.State, a.Answer = "error", "tmux: "+err.Error()
		_ = saveAsk(a)
		return err
	}
	_, _ = tmuxRun("set-option", "-t", "="+askSession(a.ID)+":", "status", "off")
	return nil
}

// cmdAskRun executes inside the ask's pane: runs claude -p, captures the
// JSON result, persists the answer, notifies, and (by default) reaps its
// own pane. Errors keep the pane so the output can be read.
func cmdAskRun(args []string) int {
	if len(args) != 1 {
		return fail(errf("usage: muster ask-run <id> (internal)"))
	}
	a, err := loadAsk(args[0])
	if err != nil {
		return fail(err)
	}
	prompt := a.Question
	if a.Skill != "" {
		prompt = "/" + a.Skill + " " + prompt
	}
	fmt.Printf("⚡ ask %s · %s\n%s\n\n", a.ID, a.Model, a.Question)
	argv := []string{"-p", prompt, "--output-format", "json", "--model", a.Model}
	if skipPermissions() {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	// seed it like any fresh teammate: primer + lessons — but no mesh
	// briefing, no roster; asks are solo sprinters by design
	if pack := contextPack(&AgentSpec{Dir: a.Dir}); pack != "" {
		argv = append(argv, "--append-system-prompt", pack)
	}
	c := exec.Command("claude", argv...)
	c.Dir = a.Dir
	c.Stderr = os.Stderr
	out, runErr := c.Output()

	var res struct {
		Result    string  `json:"result"`
		TotalCost float64 `json:"total_cost_usd"`
		IsError   bool    `json:"is_error"`
	}
	parseErr := json.Unmarshal(out, &res)
	a.DoneAt = time.Now()
	switch {
	case runErr != nil && parseErr != nil:
		a.State = "error"
		a.Answer = strings.TrimSpace(string(out))
		if a.Answer == "" {
			a.Answer = runErr.Error()
		}
	case parseErr != nil:
		a.State = "error"
		a.Answer = "unparseable claude output: " + capHead(strings.TrimSpace(string(out)), 2000)
	case res.IsError:
		a.State = "error"
		a.Answer = res.Result
	default:
		a.State = "done"
		a.Answer = res.Result
		a.CostUSD = res.TotalCost
	}
	if err := saveAsk(a); err != nil {
		fmt.Fprintln(os.Stderr, "muster: saving answer:", err)
	}
	elapsed := a.DoneAt.Sub(a.StartedAt).Round(time.Second)
	if a.State == "done" {
		notify("muster: answer ready ("+elapsed.String()+")", firstLine(a.Answer))
		fmt.Printf("\n%s\n\n✓ done in %s — answer saved; view: muster answer %s\n", a.Answer, elapsed, a.ID)
	} else {
		notify("muster: ask failed", firstLine(a.Answer))
		fmt.Printf("\n✖ failed after %s: %s\n", elapsed, a.Answer)
	}
	// reap the pane on success unless asked to keep it; errors always stay
	// readable. The answer outlives the pane either way.
	if a.State == "done" && !a.KeepPane {
		time.Sleep(2 * time.Second)
		_ = tmuxKillSession(askSession(a.ID))
	}
	return 0
}

func cmdAnswer(args []string) int {
	if len(args) != 1 {
		return fail(errf("usage: muster answer <id>"))
	}
	a, err := loadAsk(args[0])
	if err != nil {
		return fail(errf("no ask %q", args[0]))
	}
	skill := ""
	if a.Skill != "" {
		skill = " /" + a.Skill
	}
	fmt.Printf("⚡ %s%s · %s · %s\n\nQ: %s\n\n", a.ID, skill, a.Model, a.State, a.Question)
	switch a.State {
	case "running":
		fmt.Printf("still running (%s) — select it in the sidebar to watch\n",
			time.Since(a.StartedAt).Round(time.Second))
	default:
		fmt.Println(a.Answer)
		if a.CostUSD > 0 {
			fmt.Printf("\n(%s · $%.2f)\n", a.DoneAt.Sub(a.StartedAt).Round(time.Second), a.CostUSD)
		}
	}
	return 0
}

func cmdAnswers(args []string) int {
	asks := listAsks()
	if len(asks) == 0 {
		fmt.Println("no recent asks. fire one: muster ask [--skill s] '<question>'  (or ; in the sidebar)")
		return 0
	}
	for _, a := range asks {
		glyph := "⚙"
		age := time.Since(a.StartedAt).Round(time.Second).String()
		switch a.State {
		case "done":
			glyph = "✓"
			age = a.DoneAt.Sub(a.StartedAt).Round(time.Second).String()
		case "error":
			glyph = "✖"
		}
		fmt.Printf("%s %s  %-7s %-8s %s\n", glyph, a.ID, age, a.Model, clip(a.Question, 60))
		if a.State != "running" {
			fmt.Printf("           %s\n", clip(firstLine(a.Answer), 70))
		}
	}
	fmt.Println("\nfull answer: muster answer <id>  (or enter on the ask row)")
	return 0
}

// cmdAskRm dismisses an ask (running ones are killed first). Sidebar K.
func cmdAskRm(args []string) int {
	if len(args) != 1 {
		return fail(errf("usage: muster ask-rm <id>"))
	}
	a, err := loadAsk(args[0])
	if err != nil {
		return fail(errf("no ask %q", args[0]))
	}
	if tmuxHasSession(askSession(a.ID)) {
		_ = tmuxKillSession(askSession(a.ID))
	}
	if a.State == "running" {
		a.State, a.Answer, a.DoneAt = "error", "killed", time.Now()
	}
	a.Dismissed = true
	if err := saveAsk(a); err != nil {
		return fail(err)
	}
	fmt.Println("dismissed ask", a.ID)
	return 0
}

// askKeepPane: config ask_keep_pane makes every ask keep its pane.
func askKeepPane() bool {
	if c := loadConfig(); c.AskKeepPane != nil {
		return *c.AskKeepPane
	}
	return false
}

// notify posts a desktop notification (macOS; MUSTER_NOTIFY=0 disables).
// Same mechanism as the blocked-agent nudge — you're rarely staring at the
// workspace when an answer lands.
func notify(title, body string) {
	if os.Getenv("MUSTER_NOTIFY") == "0" || runtime.GOOS != "darwin" {
		return
	}
	script := fmt.Sprintf("display notification %q with title %q", body, title)
	_ = exec.Command("osascript", "-e", script).Start()
}
