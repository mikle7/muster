package main

import (
	"path/filepath"
	"testing"
)

// applyRetaskDir is the metadata half of the relocate path (retask.go): the
// bug it fixes is a retasked agent still showing the OLD task's dir/role in
// `muster ls`. These pin that the spec fields actually move.

// -C into a plain checkout drops the muster-worktree fields — the agent is no
// longer in a managed worktree, so `muster done` must not treat it as one.
func TestApplyRetaskDirPlainCheckout(t *testing.T) {
	pinState(t)
	main := testRepo(t)
	s := &AgentSpec{
		Name: "led", Harness: "claude",
		Dir: "/old/__wt/thing", Repo: "/old", Worktree: "/old/__wt/thing", Branch: "mstr/thing",
	}
	setup, err := applyRetaskDir(s, main, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if setup != "" {
		t.Fatalf("plain -C has no worktree setup, got %q", setup)
	}
	if s.Dir != main {
		t.Fatalf("Dir not moved: %s", s.Dir)
	}
	if s.Worktree != "" || s.Branch != "" {
		t.Fatalf("worktree fields must clear on -C into a plain dir: wt=%q br=%q", s.Worktree, s.Branch)
	}
}

// --repo/-b builds a fresh worktree and records all four fields, so recap and
// `muster done` see the new location.
func TestApplyRetaskDirNewWorktree(t *testing.T) {
	pinState(t)
	repo := testRepo(t)
	s := &AgentSpec{Name: "led", Harness: "claude", Dir: repo, Repo: repo}
	if _, err := applyRetaskDir(s, "", repo, "ts7"); err != nil {
		t.Fatal(err)
	}
	if s.Branch != "mstr/ts7" {
		t.Fatalf("branch = %q, want mstr/ts7", s.Branch)
	}
	wantDir := repo + "__wt" + string(filepath.Separator) + "ts7"
	if s.Dir != wantDir || s.Worktree != wantDir {
		t.Fatalf("worktree dir wrong: dir=%q wt=%q want %q", s.Dir, s.Worktree, wantDir)
	}
	abs, _ := filepath.Abs(repo)
	if s.Repo != abs {
		t.Fatalf("Repo = %q, want %q", s.Repo, abs)
	}
}

// -C into a missing/non-dir path fails before anything is mutated.
func TestApplyRetaskDirRejectsNonDir(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Name: "led", Dir: "/keep"}
	if _, err := applyRetaskDir(s, filepath.Join(t.TempDir(), "nope"), "", ""); err == nil {
		t.Fatal("expected error for a non-existent -C dir")
	}
	if s.Dir != "/keep" {
		t.Fatalf("spec mutated on error: %s", s.Dir)
	}
}
