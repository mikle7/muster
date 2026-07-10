package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- stalled derivation -------------------------------------------------------

func TestApplyStall(t *testing.T) {
	now := time.Now()
	cases := []struct {
		state  string
		age    time.Duration
		after  time.Duration
		want   string
		reason string
	}{
		{"working", 5 * time.Minute, 10 * time.Minute, "working", ""},
		{"working", 15 * time.Minute, 10 * time.Minute, "stalled", "no events"},
		{"working", 15 * time.Minute, 0, "working", ""}, // disabled
		{"blocked", time.Hour, 10 * time.Minute, "blocked", "perm"},
		{"idle", time.Hour, 10 * time.Minute, "idle", ""},
	}
	for _, c := range cases {
		reason := ""
		if c.state == "blocked" {
			reason = "perm"
		}
		st, r := applyStall(c.state, reason, now.Add(-c.age), c.after, now)
		if st != c.want {
			t.Errorf("applyStall(%s, age %v, after %v) = %s, want %s", c.state, c.age, c.after, st, c.want)
		}
		if c.reason != "" && !strings.Contains(r, c.reason) {
			t.Errorf("reason %q missing %q", r, c.reason)
		}
	}
}

// ---- event history ------------------------------------------------------------

func TestEventsAppendLoad(t *testing.T) {
	t.Setenv("MUSTER_STATE_DIR", t.TempDir())
	if err := os.MkdirAll(statusDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	uuid := "test-uuid"
	for i := 0; i < 5; i++ {
		appendEvent(AgentStatus{State: "working", Event: "PostToolUse", SessionID: uuid, TS: time.Now()})
	}
	appendEvent(AgentStatus{State: "blocked", Event: "Notification", Reason: "permission", SessionID: uuid, TS: time.Now()})
	evs := loadEvents(uuid, 3)
	if len(evs) != 3 {
		t.Fatalf("loadEvents(3) = %d events", len(evs))
	}
	last := evs[len(evs)-1]
	if last.State != "blocked" || last.Reason != "permission" {
		t.Errorf("last event = %+v, want the blocked one", last)
	}
	clearEvents(uuid)
	if evs := loadEvents(uuid, 3); evs != nil {
		t.Errorf("events survive clearEvents: %v", evs)
	}
}

// ---- worktree setup composition -------------------------------------------------

func TestWithSetup(t *testing.T) {
	if got := withSetup("claude --model haiku", ""); got != "claude --model haiku" {
		t.Errorf("no-script must be identity, got %q", got)
	}
	got := withSetup("claude", "/repo/.muster/setup")
	if !strings.Contains(got, "sh /repo/.muster/setup") {
		t.Errorf("setup script not invoked: %q", got)
	}
	if !strings.HasSuffix(got, "; claude") {
		t.Errorf("agent command must come last: %q", got)
	}
	if !strings.Contains(got, "setup failed") {
		t.Errorf("setup must be best-effort (agent starts anyway): %q", got)
	}
}

// setup is a spawn-time pane composition — it must NEVER leak into the
// stored Argv (the faithful-resume guarantee extends to it).
func TestSetupNeverInArgv(t *testing.T) {
	s := &AgentSpec{Name: "x", Argv: []string{"claude"}, Harness: "claude", SessionUUID: "u"}
	spawn := composeSpawn(s, "")
	resume := composeResume(s, "")
	for _, argv := range [][]string{spawn, resume, s.Argv} {
		for _, a := range argv {
			if strings.Contains(a, "setup") {
				t.Errorf("setup leaked into argv: %v", argv)
			}
		}
	}
}

// ---- fleet filter ---------------------------------------------------------------

func TestFilterRows(t *testing.T) {
	rows := []lsRow{
		{Name: "alice", Role: "reviews every PR", State: "idle", Dir: "/r/ibex"},
		{Name: "dave", Role: "game dev", State: "blocked", Branch: "mstr/auth", Dir: "/r/games"},
		{Name: "peter", State: "working", Dir: "/r/ibex"},
	}
	if got := filterRows(rows, ""); len(got) != 3 {
		t.Errorf("empty filter must pass everything, got %d", len(got))
	}
	if got := filterRows(rows, "review"); len(got) != 1 || got[0].Name != "alice" {
		t.Errorf("role filter failed: %v", got)
	}
	if got := filterRows(rows, "BLOCKED"); len(got) != 1 || got[0].Name != "dave" {
		t.Errorf("state filter must be case-insensitive: %v", got)
	}
	if got := filterRows(rows, "auth"); len(got) != 1 || got[0].Name != "dave" {
		t.Errorf("branch filter failed: %v", got)
	}
	if got := filterRows(rows, "ibex"); len(got) != 2 {
		t.Errorf("dir filter failed: %v", got)
	}
}

// ---- done: merge helpers ---------------------------------------------------------

func testRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	mustGit := func(args ...string) string {
		out, err := git(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return out
	}
	mustGit("init", "-b", "main")
	mustGit("config", "user.email", "t@t")
	mustGit("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", ".")
	mustGit("commit", "-m", "base")
	return dir
}

func TestMergeBranchHappyPath(t *testing.T) {
	repo := testRepo(t)
	if out, err := git(repo, "checkout", "-b", "mstr/feat"); err != nil {
		t.Fatal(out)
	}
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("feature\n"), 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-m", "feat")
	git(repo, "checkout", "main")

	if !branchAhead(repo, "mstr/feat") {
		t.Fatal("branchAhead should be true before merge")
	}
	summary, err := mergeBranch(repo, "mstr/feat", "dave", false)
	if err != nil {
		t.Fatalf("mergeBranch: %v", err)
	}
	if !strings.Contains(summary, "merged mstr/feat into main") {
		t.Errorf("summary = %q", summary)
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); err != nil {
		t.Error("merged file missing")
	}
	if branchAhead(repo, "mstr/feat") {
		t.Error("branchAhead should be false after merge")
	}
}

func TestMergeBranchRefusesDirtyRepo(t *testing.T) {
	repo := testRepo(t)
	git(repo, "branch", "mstr/feat")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty\n"), 0o644)
	if _, err := mergeBranch(repo, "mstr/feat", "dave", false); err == nil {
		t.Fatal("must refuse to merge into a dirty repo")
	}
}

func TestMergeBranchConflictAborts(t *testing.T) {
	repo := testRepo(t)
	git(repo, "checkout", "-b", "mstr/feat")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("theirs\n"), 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-m", "theirs")
	git(repo, "checkout", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("ours\n"), 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-m", "ours")

	_, err := mergeBranch(repo, "mstr/feat", "dave", false)
	if err == nil {
		t.Fatal("conflicting merge must error")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error should mention conflicts: %v", err)
	}
	// nothing half-merged left behind
	if out, _ := git(repo, "status", "--porcelain"); out != "" {
		t.Errorf("repo left dirty after aborted merge:\n%s", out)
	}
}

func TestRepoHeadBranch(t *testing.T) {
	repo := testRepo(t)
	b, err := repoHeadBranch(repo)
	if err != nil || b != "main" {
		t.Errorf("repoHeadBranch = %q, %v", b, err)
	}
}

// ---- setup script discovery ------------------------------------------------------

func TestSetupScript(t *testing.T) {
	repo := t.TempDir()
	if s := setupScript(repo); s != "" {
		t.Errorf("no script expected, got %q", s)
	}
	os.MkdirAll(filepath.Join(repo, ".muster"), 0o755)
	want := filepath.Join(repo, ".muster", "setup")
	os.WriteFile(want, []byte("#!/bin/sh\necho hi\n"), 0o755)
	if s := setupScript(repo); s != want {
		t.Errorf("setupScript = %q, want %q", s, want)
	}
}

// ---- ppz subprocess timeout -------------------------------------------------
// A wedged daemon must produce a bounded error, not a hung muster (the
// 2026-07-10 incident: 2s TUI ticks piling hung ppz calls onto a frozen
// laptop). MUSTER_PPZ points at a script that sleeps forever.
func TestPpzRunTimesOut(t *testing.T) {
	dir := t.TempDir()
	slow := filepath.Join(dir, "slowppz")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSTER_PPZ", slow)
	t.Setenv("MUSTER_PPZ_TIMEOUT_MS", "200")
	start := time.Now()
	_, err := ppzOut("test-session", "status")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should say timed out: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("timeout took %v — WaitDelay not working", time.Since(start))
	}
}
