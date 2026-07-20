package main

// Session 8: branch-in-sidebar + context refresh (clear-not-compact).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- liveBranch ---------------------------------------------------------

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLiveBranchClone(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feat/login\n")
	if got := liveBranch(repo); got != "feat/login" {
		t.Fatalf("got %q want feat/login", got)
	}
}

func TestLiveBranchWalksUpFromSubdir(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(repo, "cmd", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := liveBranch(sub); got != "main" {
		t.Fatalf("got %q want main", got)
	}
}

func TestLiveBranchWorktreeGitFile(t *testing.T) {
	base := t.TempDir()
	// linked worktree: .git is a FILE pointing at the real gitdir
	gitDir := filepath.Join(base, "repo", ".git", "worktrees", "wt1")
	writeFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/mstr/fix\n")
	wt := filepath.Join(base, "repo__wt", "fix")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+gitDir+"\n")
	if got := liveBranch(wt); got != "mstr/fix" {
		t.Fatalf("got %q want mstr/fix", got)
	}
}

func TestLiveBranchDetachedAndNonRepo(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	if got := liveBranch(repo); got != "01234567" {
		t.Fatalf("detached: got %q want 01234567", got)
	}
	if got := liveBranch(t.TempDir()); got != "" {
		t.Fatalf("non-repo: got %q want empty", got)
	}
}

// ---- sidebar sub-line items ----------------------------------------------

func TestWithSubEmitsBranchLine(t *testing.T) {
	items := withSub(nil, sideItem{kind: "agent", row: lsRow{Name: "alice", Branch: "main"}})
	if len(items) != 2 || items[1].kind != "sub" || items[1].row.Name != "alice" {
		t.Fatalf("want agent+sub, got %+v", items)
	}
	items = withSub(nil, sideItem{kind: "agent", row: lsRow{Name: "bob"}})
	if len(items) != 1 {
		t.Fatalf("branchless agent must not grow a sub-line: %+v", items)
	}
}

// selection ring must skip sub-lines: j/k, 1-9 and u address AGENTS.
func TestAgentRingSkipsSubs(t *testing.T) {
	m := tuiModel{}
	m.items = withSub(nil, sideItem{kind: "agent", row: lsRow{Name: "alice", Branch: "main"}})
	m.items = withSub(m.items, sideItem{kind: "agent", row: lsRow{Name: "bob", Branch: "dev"}})
	ring := m.agentIdxs()
	if len(ring) != 2 || m.items[ring[0]].row.Name != "alice" || m.items[ring[1]].row.Name != "bob" {
		t.Fatalf("ring wrong: %v", ring)
	}
}

// ---- session-id adoption (/clear rotates the uuid) -------------------------

func TestAdoptRotatedSession(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Name: "alice", Harness: "claude", SessionUUID: "old-uuid",
		Argv: []string{"claude", "--model", "opus"}, TmuxSession: "mstr-alice", CreatedAt: time.Now()}
	if err := saveSpec(s); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statusDir(), 0o755); err != nil { // cmdHook does this before appendEvent
		t.Fatal(err)
	}
	appendEvent(AgentStatus{State: "working", Event: "PreToolUse", SessionID: "old-uuid", TS: time.Now()})
	writeFile(t, statusPath("old-uuid"), `{"state":"idle","session_id":"old-uuid"}`)
	writeFile(t, usagePath("old-uuid"), `{"ctx_pct":88}`)

	adoptRotatedSession("alice", "new-uuid")

	got, err := loadSpec("alice")
	if err != nil || got.SessionUUID != "new-uuid" {
		t.Fatalf("spec not re-pointed: %+v %v", got, err)
	}
	// faithful resume: Argv untouched, resume targets the NEW id
	if strings.Join(got.Argv, " ") != "claude --model opus" {
		t.Fatalf("Argv mutated: %v", got.Argv)
	}
	if r := composeResume(got, ""); !strings.Contains(strings.Join(r, " "), "--resume new-uuid") {
		t.Fatalf("resume misses new id: %v", r)
	}
	// history migrated, stale per-session files gone
	if evs := loadEvents("new-uuid", 10); len(evs) != 1 || evs[0].Event != "PreToolUse" {
		t.Fatalf("events not migrated: %+v", evs)
	}
	for _, p := range []string{statusPath("old-uuid"), usagePath("old-uuid"), eventsPath("old-uuid")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("stale file survived: %s", p)
		}
	}
}

func TestAdoptRotatedSessionNoops(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Name: "bob", Harness: "claude", SessionUUID: "same", Argv: []string{"claude"}}
	if err := saveSpec(s); err != nil {
		t.Fatal(err)
	}
	adoptRotatedSession("bob", "same") // unchanged id
	adoptRotatedSession("ghost", "x")  // unknown agent
	adoptRotatedSession("", "y")       // no MUSTER_AGENT
	adoptRotatedSession("bob", "")     // no session id
	if got, _ := loadSpec("bob"); got.SessionUUID != "same" {
		t.Fatalf("noop mutated spec: %+v", got)
	}
}

// ---- handoff re-injection ---------------------------------------------------

func TestHandoffHookJSON(t *testing.T) {
	pinState(t)
	writeFile(t, handoffPath("alice"), "## Task\nport the login form\n## Next\nwire the API")
	b := clearHookJSON("alice", "refresh")
	var out struct {
		H struct {
			Event string `json:"hookEventName"`
			Ctx   string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.H.Event != "SessionStart" || !strings.Contains(out.H.Ctx, "port the login form") {
		t.Fatalf("bad hook JSON: %s", b)
	}
	if !strings.Contains(out.H.Ctx, "context was just cleared") {
		t.Fatalf("missing orientation preamble: %s", out.H.Ctx)
	}
	if clearHookJSON("", "manual") != nil {
		t.Fatal("empty agent must emit nothing")
	}
}

func TestHandoffHookJSONCapsAtTail(t *testing.T) {
	pinState(t)
	old := strings.Repeat("OLD ", 3000)
	writeFile(t, handoffPath("big"), old+"\nLATEST-NOTE")
	b := clearHookJSON("big", "refresh")
	if len(b) > 11000 {
		t.Fatalf("over the 10k additionalContext cap: %d bytes", len(b))
	}
	if !strings.Contains(string(b), "LATEST-NOTE") {
		t.Fatal("cap must keep the TAIL — latest notes win")
	}
}

func TestHandoffMissingFileStillInjectsGuidance(t *testing.T) {
	pinState(t)
	b := clearHookJSON("noone", "refresh")
	if !strings.Contains(string(b), "handoff file is empty") {
		t.Fatalf("missing-file fallback absent: %s", b)
	}
}

// ---- briefing tells the agent about the handoff ------------------------------

func TestBriefingIncludesHandoffPath(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Name: "alice", PpzHandle: "alice", Harness: "claude"}
	b := meshBriefing(s)
	if !strings.Contains(b, handoffPath("alice")) || !strings.Contains(b, "CONTEXT HANDOFF") {
		t.Fatalf("briefing misses handoff contract: %s", b)
	}
}

// ---- auto-refresh gating ------------------------------------------------------

func TestShouldAutoRefresh(t *testing.T) {
	cases := []struct {
		harness, state string
		ctx            float64
		threshold      int
		want           bool
	}{
		{"claude", "idle", 80, 75, true},
		{"claude", "idle", 75, 75, true},
		{"claude", "idle", 74, 75, false},    // below threshold
		{"claude", "working", 90, 75, false}, // never yank a working agent
		{"claude", "blocked", 90, 75, false},
		{"claude", "dead", 90, 75, false},
		{"", "idle", 90, 75, false},      // unknown harness: no /clear to drive
		{"claude", "idle", 90, 0, false}, // 0 disables
	}
	for _, c := range cases {
		if got := shouldAutoRefresh(c.harness, c.state, c.ctx, c.threshold); got != c.want {
			t.Errorf("shouldAutoRefresh(%q,%q,%v,%d) = %v want %v",
				c.harness, c.state, c.ctx, c.threshold, got, c.want)
		}
	}
}

func TestRefreshMarkLifecycle(t *testing.T) {
	pinState(t)
	if refreshInFlight("alice") {
		t.Fatal("no mark yet")
	}
	setRefreshMark("alice")
	if !refreshInFlight("alice") {
		t.Fatal("fresh mark must gate")
	}
	clearRefreshMark("alice")
	if refreshInFlight("alice") {
		t.Fatal("cleared mark must not gate")
	}
}
