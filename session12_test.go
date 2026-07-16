package main

// Session 12: task-first dispatch, context packs, model-as-launch-state,
// the ask lane, templates/pools, retask. Everything here is the pure core
// of those features — the tmux/claude halves are covered by headless E2E.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- model adoption + composition ------------------------------------------

func TestAdoptModel(t *testing.T) {
	m, rest := adoptModel([]string{"claude", "--model", "opus", "--verbose"})
	if m != "opus" || strings.Join(rest, " ") != "claude --verbose" {
		t.Fatalf("adopt failed: %q %v", m, rest)
	}
	m, rest = adoptModel([]string{"claude", "--model=sonnet"})
	if m != "sonnet" || strings.Join(rest, " ") != "claude" {
		t.Fatalf("adopt = form failed: %q %v", m, rest)
	}
	if m, _ := adoptModel([]string{"claude"}); m != "" {
		t.Fatalf("phantom model %q", m)
	}
}

func TestComposeInjectsSpecModel(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Harness: "claude", SessionUUID: "u1", Argv: []string{"claude"}, Model: "opus"}
	spawn := strings.Join(composeSpawn(s, ""), " ")
	if !strings.Contains(spawn, "--model opus") {
		t.Fatalf("spawn misses model: %s", spawn)
	}
	res := strings.Join(composeResume(s, ""), " ")
	if !strings.Contains(res, "--model opus") || !strings.Contains(res, "--resume u1") {
		t.Fatalf("resume misses model/resume: %s", res)
	}
	// no model → no flag (claude picks its default)
	s.Model = ""
	if strings.Contains(strings.Join(composeSpawn(s, ""), " "), "--model") {
		t.Fatal("phantom --model injected")
	}
	// Argv stays verbatim — the sacred half of the guarantee
	if strings.Join(s.Argv, " ") != "claude" {
		t.Fatalf("Argv mutated: %v", s.Argv)
	}
}

// ---- dispatch parsing -------------------------------------------------------

func TestParseTaskLineTokens(t *testing.T) {
	tk := parseTaskLine("fix the login redirect @backend !sonnet /triage-logs #gamesrv")
	if tk.Target != "backend" || tk.Model != "sonnet" || tk.Skill != "triage-logs" || tk.Proj != "gamesrv" {
		t.Fatalf("tokens: %+v", tk)
	}
	if tk.Text != "fix the login redirect" {
		t.Fatalf("text: %q", tk.Text)
	}
	// paths must not read as skill tokens
	tk = parseTaskLine("check /var/log/app.log and src/foo.go please")
	if tk.Skill != "" || !strings.Contains(tk.Text, "/var/log/app.log") {
		t.Fatalf("path ate a skill token: %+v", tk)
	}
	tk = parseTaskLine("why can't user 4821 log in?")
	if !tk.Question {
		t.Fatal("trailing ? not detected")
	}
}

func TestParseDispatchEpic(t *testing.T) {
	tasks := parseDispatch("ship the auth revamp #gamesrv !sonnet\n" +
		"- token rotation endpoints @backend\n" +
		"- login flow update @frontend !opus\n")
	if len(tasks) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(tasks))
	}
	if tasks[0].Epic != "ship the auth revamp" || tasks[0].Target != "backend" {
		t.Fatalf("bullet 1: %+v", tasks[0])
	}
	// header tokens inherit; per-line overrides win
	if tasks[0].Model != "sonnet" || tasks[0].Proj != "gamesrv" {
		t.Fatalf("inheritance: %+v", tasks[0])
	}
	if tasks[1].Model != "opus" {
		t.Fatalf("override lost: %+v", tasks[1])
	}
	brief := tasks[0].brief()
	if !strings.Contains(brief, "EPIC: ship the auth revamp") || !strings.Contains(brief, "YOUR PART: token rotation") {
		t.Fatalf("brief: %s", brief)
	}
}

func TestParseDispatchPlainLines(t *testing.T) {
	tasks := parseDispatch("first thing @a\nsecond thing @b\n")
	if len(tasks) != 2 || tasks[0].Target != "a" || tasks[1].Target != "b" || tasks[0].Epic != "" {
		t.Fatalf("plain lines: %+v", tasks)
	}
}

// ---- dispatch resolution ------------------------------------------------------

func dispatchFixture() ([]lsRow, []Template, []Project) {
	rows := []lsRow{
		{Name: "backend-1", State: "working", Harness: "claude", Template: "backend", Dir: "/p/game"},
		{Name: "backend-2", State: "idle", Harness: "claude", Template: "backend", CtxPct: 40, Dir: "/p/game"},
		{Name: "backend-3", State: "idle", Harness: "claude", Template: "backend", CtxPct: 10, Dir: "/p/game"},
		{Name: "solo", State: "idle", Harness: "claude", Dir: "/p/web"},
		{Name: "deadguy", State: "dead", Harness: "claude", Dir: "/p/game"},
	}
	templates := []Template{{Name: "backend", Project: "game", Cap: 3}}
	projects := []Project{{Name: "game", Path: "/p/game"}, {Name: "web", Path: "/p/web"}}
	return rows, templates, projects
}

func TestResolveExplicitAgent(t *testing.T) {
	rows, ts, ps := dispatchFixture()
	act := resolveDispatch(parseTaskLine("do x @backend-2"), rows, ts, ps, "")
	if act.Kind != "retask" || act.Agent != "backend-2" {
		t.Fatalf("idle agent should retask: %+v", act)
	}
	act = resolveDispatch(parseTaskLine("do x @backend-1"), rows, ts, ps, "")
	if act.Kind != "send" {
		t.Fatalf("busy agent should queue: %+v", act)
	}
	act = resolveDispatch(parseTaskLine("do x @deadguy"), rows, ts, ps, "")
	if act.Kind != "refuse" {
		t.Fatalf("dead agent must refuse: %+v", act)
	}
	act = resolveDispatch(parseTaskLine("do x @nosuch"), rows, ts, ps, "")
	if act.Kind != "refuse" {
		t.Fatalf("unknown target must refuse: %+v", act)
	}
}

func TestResolvePoolPicksFreshestIdle(t *testing.T) {
	rows, ts, ps := dispatchFixture()
	act := resolveDispatch(parseTaskLine("do x @backend"), rows, ts, ps, "")
	if act.Kind != "retask" || act.Agent != "backend-3" {
		t.Fatalf("want freshest idle member (backend-3): %+v", act)
	}
}

func TestResolvePoolSparesWin(t *testing.T) {
	rows, ts, ps := dispatchFixture()
	rows = append(rows, lsRow{Name: "backend-4", State: "idle", Harness: "claude", Template: "backend", Spare: true})
	act := resolveDispatch(parseTaskLine("do x @backend"), rows, ts, ps, "")
	if act.Kind != "claim" || act.Agent != "backend-4" {
		t.Fatalf("warm spare should be claimed first: %+v", act)
	}
}

func TestResolvePoolGrowsThenRefuses(t *testing.T) {
	_, ts, ps := dispatchFixture()
	busy := []lsRow{
		{Name: "backend-1", State: "working", Harness: "claude", Template: "backend"},
		{Name: "backend-2", State: "working", Harness: "claude", Template: "backend"},
	}
	act := resolveDispatch(parseTaskLine("do x @backend"), busy, ts, ps, "")
	if act.Kind != "spawn" || act.Template != "backend" {
		t.Fatalf("under-cap pool should hire: %+v", act)
	}
	busy = append(busy, lsRow{Name: "backend-3", State: "working", Harness: "claude", Template: "backend"})
	act = resolveDispatch(parseTaskLine("do x @backend"), busy, ts, ps, "")
	if act.Kind != "refuse" {
		t.Fatalf("full busy pool must refuse loudly: %+v", act)
	}
}

func TestResolveNoTarget(t *testing.T) {
	rows, ts, ps := dispatchFixture()
	// space=web: exactly one idle agent there → retask it
	act := resolveDispatch(parseTaskLine("do the thing"), rows, ts, ps, "/p/web")
	if act.Kind != "retask" || act.Agent != "solo" {
		t.Fatalf("single idle project agent should take it: %+v", act)
	}
	// space=game: two idle backends + one project template → pool wins? no:
	// two idle agents means ambiguity, but the single game template exists —
	// idle count != 1 so template path fires
	act = resolveDispatch(parseTaskLine("do the thing"), rows, ts, ps, "/p/game")
	if act.Kind != "retask" || act.Agent != "backend-3" {
		t.Fatalf("project template pool should route: %+v", act)
	}
	// question with nowhere else to go → ask lane
	act = resolveDispatch(parseTaskLine("why is prod slow?"), nil, nil, ps, "")
	if act.Kind != "ask" {
		t.Fatalf("question should fall to ask: %+v", act)
	}
	// statement with nowhere to go → loud refusal, never silent queueing
	act = resolveDispatch(parseTaskLine("do mystery work"), nil, nil, ps, "")
	if act.Kind != "refuse" {
		t.Fatalf("no target must refuse: %+v", act)
	}
	// @ask forces the lane even for statements
	act = resolveDispatch(parseTaskLine("summarize the deploy log @ask"), rows, ts, ps, "")
	if act.Kind != "ask" {
		t.Fatalf("@ask override: %+v", act)
	}
}

// ---- pools ----------------------------------------------------------------

func TestPoolName(t *testing.T) {
	specs := []*AgentSpec{{Name: "backend-1"}, {Name: "backend-3"}}
	if n := poolName("backend", specs); n != "backend-2" {
		t.Fatalf("want backend-2, got %s", n)
	}
	if n := poolName("infra", nil); n != "infra-1" {
		t.Fatalf("want infra-1, got %s", n)
	}
}

// ---- context packs ----------------------------------------------------------

func TestFocusPrimer(t *testing.T) {
	primer := "shared intro\n## backend\napi lives in svc/\n## frontend\nspa in web/\nmore shared? no — still frontend\n"
	got := focusPrimer(primer, "backend", "")
	if !strings.Contains(got, "shared intro") || !strings.Contains(got, "api lives in svc/") {
		t.Fatalf("backend focus lost content: %q", got)
	}
	if strings.Contains(got, "spa in web/") {
		t.Fatalf("frontend section leaked: %q", got)
	}
	// no sections → untouched
	if focusPrimer("plain text", "backend", "") != "plain text" {
		t.Fatal("sectionless primer must pass through")
	}
}

func TestContextPackAndInjection(t *testing.T) {
	pinState(t)
	proj := t.TempDir()
	if _, err := addProject(proj, "game"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proj, ".muster", "primer.md"), "# game\nauth service on :9000")
	writeFile(t, lessonsPath("game"), "- seed db before tests (alice, 2026-07-01)\n")
	s := &AgentSpec{Name: "al", Harness: "claude", Dir: proj, Argv: []string{"claude"}, SessionUUID: "u"}
	pack := contextPack(s)
	if !strings.Contains(pack, "auth service on :9000") || !strings.Contains(pack, "seed db before tests") {
		t.Fatalf("pack incomplete: %s", pack)
	}
	if !strings.Contains(pack, "muster lesson add") {
		t.Fatalf("pack misses the lesson instruction: %s", pack)
	}
	// injected() carries it — even off-mesh (PpzHandle empty)
	argv := composeSpawn(s, "")
	joined := strings.Join(argv, "\x00")
	if !strings.Contains(joined, "auth service on :9000") {
		t.Fatalf("spawn briefing misses the pack: %s", strings.Join(argv, " "))
	}
	// and never lands in the stored Argv
	if len(s.Argv) != 1 || s.Argv[0] != "claude" {
		t.Fatalf("Argv polluted: %v", s.Argv)
	}
}

// ---- retask hook injection -----------------------------------------------------

func TestClearHookJSONRetask(t *testing.T) {
	pinState(t)
	proj := t.TempDir()
	if _, err := addProject(proj, "game"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proj, ".muster", "primer.md"), "PRIMER-CONTENT")
	writeFile(t, lessonsPath("game"), "- LESSON-CONTENT (x, 2026-07-01)\n")
	writeFile(t, handoffPath("al"), "OLD-TASK-NOTES")
	s := &AgentSpec{Name: "al", Harness: "claude", Dir: proj, Argv: []string{"claude"}, TmuxSession: "mstr-al"}
	if err := saveSpec(s); err != nil {
		t.Fatal(err)
	}
	b := clearHookJSON("al", true)
	var out struct {
		H struct {
			Ctx string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.H.Ctx, "NEW task") || !strings.Contains(out.H.Ctx, "PRIMER-CONTENT") ||
		!strings.Contains(out.H.Ctx, "LESSON-CONTENT") {
		t.Fatalf("retask injection wrong: %s", out.H.Ctx)
	}
	if strings.Contains(out.H.Ctx, "OLD-TASK-NOTES") {
		t.Fatalf("old handoff leaked into a retask: %s", out.H.Ctx)
	}
	// refresh path still carries the handoff + lessons
	b = clearHookJSON("al", false)
	if !strings.Contains(string(b), "OLD-TASK-NOTES") || !strings.Contains(string(b), "LESSON-CONTENT") {
		t.Fatalf("refresh injection wrong: %s", b)
	}
}

func TestRetaskMarkConsume(t *testing.T) {
	pinState(t)
	setRetaskMark("al", "new thing")
	if !retaskInFlight("al") {
		t.Fatal("mark not visible")
	}
	if !consumeRetaskMark("al") {
		t.Fatal("consume failed")
	}
	if consumeRetaskMark("al") {
		t.Fatal("mark must be one-shot")
	}
}

// ---- ask rows -----------------------------------------------------------------

func TestAskRows(t *testing.T) {
	now := time.Now()
	asks := []*Ask{
		{ID: "a1", Question: "why is login down?", State: "running", Model: "sonnet", StartedAt: now.Add(-2 * time.Minute)},
		{ID: "a2", Question: "old one", State: "done", StartedAt: now.Add(-10 * time.Minute), DoneAt: now.Add(-5 * time.Minute)},
	}
	rows := askRows(asks)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if !rows[0].Ask || rows[0].State != "working" || rows[0].Question == "" {
		t.Fatalf("running row: %+v", rows[0])
	}
	if rows[1].State != "idle" || rows[1].Age != "5m0s" {
		t.Fatalf("done row: %+v", rows[1])
	}
}

// ---- templates + pending ---------------------------------------------------------

func TestTemplateRoundTripAndBriefing(t *testing.T) {
	pinState(t)
	ts := []Template{{Name: "backend", Role: "apis", Model: "sonnet", Skills: []string{"triage-logs"}, Warm: true}}
	if err := saveTemplates(ts); err != nil {
		t.Fatal(err)
	}
	got := findTemplate("backend")
	if got == nil || got.Model != "sonnet" || !got.Warm {
		t.Fatalf("round trip: %+v", got)
	}
	b := templateBriefing(got)
	if !strings.Contains(b, "'backend' template") || !strings.Contains(b, "/triage-logs") {
		t.Fatalf("briefing: %s", b)
	}
	if got.cap() != 3 {
		t.Fatalf("default cap: %d", got.cap())
	}
}

func TestPendingRows(t *testing.T) {
	pinState(t)
	writePending("backend-2", "spawning")
	rows := loadPending(nil)
	if len(rows) != 1 || rows[0].Name != "backend-2" {
		t.Fatalf("pending: %+v", rows)
	}
	// a real spec supersedes the marker
	rows = loadPending([]*AgentSpec{{Name: "backend-2"}})
	if len(rows) != 0 {
		t.Fatalf("spec should clear pending: %+v", rows)
	}
	clearPending("backend-2")
	if len(loadPendingRaw()) != 0 {
		t.Fatal("clearPending failed")
	}
}

// A fresh boot (SessionStart source=startup) is idle at its prompt — the
// signal warm spares are claimable on. clear/resume stay working (mid-flow).
func TestSessionStartStartupIsIdle(t *testing.T) {
	if got, _ := hookEventState("SessionStart", "", "startup"); got != "idle" {
		t.Fatalf("startup = %q, want idle", got)
	}
	for _, src := range []string{"clear", "resume", ""} {
		if got, _ := hookEventState("SessionStart", "", src); got != "working" {
			t.Fatalf("source %q = %q, want working", src, got)
		}
	}
}
