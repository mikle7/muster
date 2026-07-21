package main

// Retask: repurpose an existing agent for a NEW task on a fresh context.
// The missing verb between `refresh` (fresh context, SAME task — handoff
// carried over) and kill+spawn (slow). The 40-50%-context agent sitting
// idle in a project becomes a seeded fresh agent in seconds:
//
//   wait idle → rotate handoff aside → /clear → (optional /model) → task
//
// The SessionStart hook sees the retask marker and injects the project
// primer + latest lessons instead of the (now irrelevant) old handoff —
// the new task must not start life buried in the previous task's notes.
//
// A NEW task often belongs in a NEW place too (the old worktree was the old
// task's). `-C dir` / `--repo dir -b branch` relocate the agent as part of
// the same reset. A running claude's cwd is fixed at exec — you cannot cd it
// — so relocation kills the pane and relaunches in the new dir with a fresh
// session id, which IS the fresh context retask wants; the full briefing
// (identity, primer, room, conventions) is recomputed from the updated spec,
// so `muster ls`/recap and the sidebar stop showing the stale old-task dir
// and role. Without a location flag, the fast in-place /clear path is unchanged.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func retaskMarkPath(name string) string {
	return filepath.Join(dataDir(), "retask", name+".json")
}

type retaskMark struct {
	TS   time.Time `json:"ts"`
	Task string    `json:"task,omitempty"`
}

func setRetaskMark(name, task string) {
	if err := os.MkdirAll(filepath.Dir(retaskMarkPath(name)), 0o755); err != nil {
		return
	}
	b, _ := json.Marshal(retaskMark{TS: time.Now(), Task: task})
	_ = atomicWrite(retaskMarkPath(name), b)
}

// consumeRetaskMark reports (and clears) a pending retask for name — called
// by the SessionStart hook so the post-/clear injection knows this is a NEW
// task, not a refresh. Stale marks (>10m — a crashed retask) don't count.
func consumeRetaskMark(name string) bool {
	b, err := os.ReadFile(retaskMarkPath(name))
	if err != nil {
		return false
	}
	_ = os.Remove(retaskMarkPath(name))
	var mk retaskMark
	if json.Unmarshal(b, &mk) != nil {
		return false
	}
	return time.Since(mk.TS) < refreshMarkTTL
}

func retaskInFlight(name string) bool {
	b, err := os.ReadFile(retaskMarkPath(name))
	if err != nil {
		return false
	}
	var mk retaskMark
	if json.Unmarshal(b, &mk) != nil {
		return false
	}
	return time.Since(mk.TS) < refreshMarkTTL
}

func cmdRetask(args []string) int {
	fs := flag.NewFlagSet("retask", flag.ExitOnError)
	model := fs.String("model", "", "switch model for the new task (opus|sonnet|haiku|id)")
	spare := fs.Bool("spare", false, "mark the reset agent a claimable warm spare (auto-reset sweep)")
	dir := fs.String("C", "", "relocate: point the agent at this existing directory (e.g. the main checkout)")
	repo := fs.String("repo", "", "relocate: create a fresh worktree of this repo (with -b)")
	branch := fs.String("b", "", "branch/worktree name (with --repo)")
	role := fs.String("role", "", "update the agent's charter/role (shown in ls, re-briefed to it and teammates)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: muster retask <name> [--model m] [--role r] [-C dir | --repo dir -b branch] [--spare] [task text…]")
	}
	if len(args) < 1 {
		fs.Usage()
		return 2
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if (*repo == "") != (*branch == "") {
		return fail(errf("--repo and -b go together"))
	}
	if *dir != "" && *repo != "" {
		return fail(errf("-C and --repo are mutually exclusive"))
	}
	task := joinWords(fs.Args())
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no agent %q", name))
	}
	if s.Harness != "claude" {
		return fail(errf("%s is not a claude agent — retask drives /clear", name))
	}
	if !tmuxHasSession(s.TmuxSession) {
		return fail(errf("%s is dead — resume it first (or dispatch will spawn a new one)", name))
	}
	if refreshInFlight(name) || retaskInFlight(name) {
		return fail(errf("%s is already mid-refresh/retask", name))
	}
	// claim both markers: the retask marker steers the hook injection, the
	// refresh marker stops the TUI's auto-refresh firing mid-cycle.
	setRefreshMark(name)
	defer clearRefreshMark(name)

	oldID := s.SessionUUID
	// a busy agent finishes its current turn first — clearing/killing it
	// mid-turn would lose or queue work unpredictably. Dispatch only routes
	// to idle agents, so this wait is usually zero; a manual retask on a
	// working agent waits.
	if st := loadStatus(oldID); st != nil && st.State == "working" {
		fmt.Printf("retask %s: waiting for the current turn to finish (up to %s)…\n", name, refreshWait())
		idle := func() bool {
			st := loadStatus(oldID)
			return st == nil || st.State != "working"
		}
		if !waitFor(idle, refreshWait(), 2*time.Second) {
			return fail(errf("%s never went idle — retask aborted, nothing lost", name))
		}
	}

	// the old task's handoff would poison the new task's injection — rotate
	// it aside (kept as .prev, never destroyed).
	hp := handoffPath(name)
	if _, err := os.Stat(hp); err == nil {
		_ = os.Rename(hp, hp+".prev")
	}
	if *role != "" {
		s.Role = *role
	}

	// Relocating? A running claude can't be moved, so kill+relaunch in the new
	// dir with a fresh session — the clean break retask already wants, and the
	// only thing that actually updates the pane's cwd AND the tracked spec.Dir.
	if *dir != "" || *repo != "" {
		return retaskRelocate(s, oldID, *dir, *repo, *branch, *model, *spare, task)
	}

	// ---- in-place path: fresh context via /clear, same location ----
	if *role != "" {
		// the /clear hook reloads the spec and re-briefs identity, so saving
		// the new role now is all it takes for the reset to announce it.
		_ = saveSpec(s)
	}

	setRetaskMark(name, task)
	fmt.Printf("retask %s: clearing context…\n", name)
	if err := tmuxSendLine(s.TmuxSession, "/clear"); err != nil {
		return fail(err)
	}
	rotated := func() bool {
		s2, err := loadSpec(name)
		return err == nil && s2.SessionUUID != "" && s2.SessionUUID != oldID
	}
	if waitFor(rotated, 20*time.Second, time.Second) {
		fmt.Println("fresh context; primer + lessons injected via SessionStart hook")
	} else {
		fmt.Println("warning: no session-id rotation observed (hooks off? old claude?) — continuing anyway")
	}

	if *spare {
		// reload — the hook's adoption just rewrote the spec
		if s2, err := loadSpec(name); err == nil {
			s2.Spare = true
			_ = saveSpec(s2)
		}
	}
	if *model != "" {
		// after rotation so the fresh session gets it; spec too so resume
		// composes it. Reload — the hook's adoption just rewrote the spec.
		if s2, err := loadSpec(name); err == nil {
			s2.Model = *model
			_ = saveSpec(s2)
		}
		if err := tmuxSendLine(s.TmuxSession, "/model "+*model); err != nil {
			fmt.Fprintf(os.Stderr, "muster: /model: %v (continuing)\n", err)
		}
		time.Sleep(500 * time.Millisecond) // let the model switch settle before the task lands
	}

	kick := task
	if kick == "" {
		kick = "Fresh start — you have a clean context seeded with the project primer. Wait for your task."
	}
	if err := tmuxSendLine(s.TmuxSession, kick); err != nil {
		return fail(err)
	}
	if task != "" {
		fmt.Printf("%s retasked: fresh seeded context, task delivered\n", name)
	} else {
		fmt.Printf("%s retasked: fresh seeded context, waiting for a task\n", name)
	}
	return 0
}

// applyRetaskDir resolves a relocation into s, mutating Dir/Repo/Worktree/
// Branch in place and returning any worktree setup script to run in the pane.
// Mirrors cmdSpawn's dir precedence exactly: --repo/-b makes a fresh worktree;
// -C points at an existing dir and drops the muster-worktree fields (the agent
// is no longer in a managed worktree unless dir happens to be inside one).
// Pure enough to unit-test against a temp repo — no tmux, no launch.
func applyRetaskDir(s *AgentSpec, dir, repo, branch string) (setup string, err error) {
	switch {
	case repo != "":
		wt, fb, werr := worktreeAdd(repo, branch)
		if werr != nil {
			return "", werr
		}
		abs, _ := filepath.Abs(repo)
		s.Repo, s.Worktree, s.Branch, s.Dir = abs, wt, fb, wt
		_, _ = addProject(abs, "") // idempotent: repos you retask into show up in the UI
		return setupScript(abs), nil
	default: // -C dir
		abs, aerr := filepath.Abs(dir)
		if aerr != nil {
			return "", aerr
		}
		if fi, serr := os.Stat(abs); serr != nil || !fi.IsDir() {
			return "", errf("-C %s: not a directory", dir)
		}
		s.Dir, s.Repo, s.Worktree, s.Branch = abs, "", "", ""
		if root := worktreeRepoRoot(abs); root != "" {
			s.Repo = root
			_, _ = addProject(root, "")
		}
		return "", nil
	}
}

// retaskRelocate kills the agent's pane and relaunches it in a new working
// directory with a fresh session — the only way to actually move a claude,
// which carries its cwd from exec. The full first-launch briefing is
// recomputed from the (now updated) spec, so identity/primer/room/conventions
// and the tracked dir+role all match the new location. The task is delivered
// once the fresh session's hook signals it has booted (muster deliver).
func retaskRelocate(s *AgentSpec, oldID, dir, repo, branch, model string, spare bool, task string) int {
	name := s.Name
	oldWt := s.Worktree
	noPpz := s.PpzHandle == "" // preserve mesh membership as it was

	setup, err := applyRetaskDir(s, dir, repo, branch)
	if err != nil {
		return fail(err)
	}
	if repo != "" {
		fmt.Printf("worktree %s (branch %s)\n", s.Worktree, s.Branch)
	}
	if model != "" {
		s.Model = model
	}
	s.Spare = spare
	s.SessionUUID = newUUID() // fresh context: a clean transcript in the new place
	s.ResumedAt = time.Now()

	// abandon the old session's status/events/usage — new task, clean break.
	if oldID != "" {
		clearStatus(oldID)
		clearEvents(oldID)
		_ = os.Remove(usagePath(oldID))
	}

	if err := tmuxKillSession(s.TmuxSession); err != nil {
		return fail(err)
	}
	if rc := launch(s, false, noPpz, setup); rc != 0 {
		return rc
	}
	if oldWt != "" && oldWt != s.Dir {
		fmt.Printf("note: previous worktree %s left untouched — clean it up by hand once you're sure the old work is safe\n", collapseHome(oldWt))
	}
	if task != "" {
		// deliver waits for the fresh session's SessionStart hook, then types.
		_ = exec.Command(selfExe(), "deliver", name, task).Start()
		fmt.Printf("%s retasked → %s: fresh context, task delivering when it boots\n", name, collapseHome(s.Dir))
	} else {
		fmt.Printf("%s retasked → %s: fresh context, waiting for a task\n", name, collapseHome(s.Dir))
	}
	return 0
}

func joinWords(ws []string) string {
	out := ""
	for i, w := range ws {
		if i > 0 {
			out += " "
		}
		out += w
	}
	return out
}
