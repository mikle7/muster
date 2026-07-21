package main

// Session 13: /clear rework (identity re-injection + manual mode) and the
// merged ⇒ auto-reset sweep.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- clearMode: who initiated the /clear ----------------------------------

func TestClearModeClassification(t *testing.T) {
	pinState(t)
	if got := clearMode("al"); got != "manual" {
		t.Fatalf("no markers should mean manual, got %q", got)
	}
	setRetaskMark("al", "new thing")
	if got := clearMode("al"); got != "retask" {
		t.Fatalf("retask mark should win, got %q", got)
	}
	if got := clearMode("al"); got != "manual" {
		t.Fatalf("retask mark must be one-shot, got %q", got)
	}
	setRefreshMark("al")
	defer clearRefreshMark("al")
	if got := clearMode("al"); got != "refresh" {
		t.Fatalf("refresh mark should mean refresh, got %q", got)
	}
}

// ---- manual /clear: fresh start, handoff parked, identity re-injected -----

func TestClearHookManualParksHandoffAndRebriefs(t *testing.T) {
	pinState(t)
	proj := t.TempDir()
	if _, err := addProject(proj, "game"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(proj, ".muster", "primer.md"), "PRIMER-CONTENT")
	writeFile(t, lessonsPath("game"), "- LESSON-CONTENT (x, 2026-07-01)\n")
	writeFile(t, handoffPath("al"), "OLD-TASK-NOTES")
	s := &AgentSpec{Name: "al", Harness: "claude", Dir: proj, PpzHandle: "al", Argv: []string{"claude"}}
	if err := saveSpec(s); err != nil {
		t.Fatal(err)
	}
	ctx := string(clearHookJSON("al", "manual"))
	for _, want := range []string{
		"fresh start", "NEW task",
		"You are agent 'al'", // identity re-injected — the resumed-process amnesia fix
		"PRIMER-CONTENT", "LESSON-CONTENT",
		handoffPath("al") + ".prev", // pointer to the parked notes
		"ppz subs read",             // inbox retention notice for mesh agents
	} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("manual injection misses %q: %s", want, ctx)
		}
	}
	if strings.Contains(ctx, "OLD-TASK-NOTES") {
		t.Fatalf("old handoff leaked into a manual clear: %s", ctx)
	}
	if _, err := os.Stat(handoffPath("al")); !os.IsNotExist(err) {
		t.Fatal("handoff must be parked aside on a manual clear")
	}
	if _, err := os.Stat(handoffPath("al") + ".prev"); err != nil {
		t.Fatal("parked handoff must be kept as .prev, never destroyed")
	}
}

// Every mode re-briefs identity: the --append-system-prompt briefing only
// exists on first-launch processes, so a resumed agent's /clear must get it
// from the hook or it wakes up amnesiac.
func TestClearHookRebriefsEveryMode(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Name: "al", Harness: "claude", Dir: t.TempDir(), PpzHandle: "al", Argv: []string{"claude"}}
	if err := saveSpec(s); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"retask", "refresh", "manual"} {
		if ctx := string(clearHookJSON("al", mode)); !strings.Contains(ctx, "You are agent 'al'") {
			t.Fatalf("%s injection misses the identity briefing: %s", mode, ctx)
		}
	}
}

// ---- merged ⇒ auto-reset eligibility ---------------------------------------

func TestWorkShipped(t *testing.T) {
	repo := testRepo(t)
	git(repo, "checkout", "-b", "mstr/feat")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("feature\n"), 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-m", "feat")
	git(repo, "checkout", "main")
	s := &AgentSpec{Name: "dave", Branch: "mstr/feat", Repo: repo, Dir: repo,
		CreatedAt: time.Now().Add(-time.Hour)}

	if workShipped(s) {
		t.Fatal("unmerged branch must not count as shipped")
	}
	if _, err := git(repo, "merge", "--no-ff", "-m", "land", "mstr/feat"); err != nil {
		t.Fatal(err)
	}
	if !workShipped(s) {
		t.Fatal("merged branch with commits during tenure should be shipped")
	}

	// analysis-only agent: no commits since it was created — context is its
	// value, never reset
	s.CreatedAt = time.Now().Add(time.Hour)
	if workShipped(s) {
		t.Fatal("no commits during tenure must not count as shipped")
	}
	s.CreatedAt = time.Now().Add(-time.Hour)

	// dirty worktree = work in flight
	os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("wip\n"), 0o644)
	if workShipped(s) {
		t.Fatal("dirty worktree must never be touched")
	}
}
