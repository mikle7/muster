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

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: muster retask <name> [--model m] [--spare] [task text…]")
	}
	if len(args) < 1 {
		fs.Usage()
		return 2
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
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
	// a busy agent finishes its current turn first — /clear typed mid-turn
	// would queue unpredictably. Dispatch only routes to idle agents, so
	// this wait is usually zero; a manual retask on a working agent waits.
	if st := loadStatus(oldID); st != nil && st.State == "working" {
		fmt.Printf("retask %s: waiting for the current turn to finish (up to %s)…\n", name, refreshWait())
		idle := func() bool {
			st := loadStatus(oldID)
			return st == nil || st.State != "working"
		}
		if !waitFor(idle, refreshWait(), 2*time.Second) {
			return fail(errf("%s never went idle — retask aborted before /clear, nothing lost", name))
		}
	}

	// the old task's handoff would poison the new task's injection — rotate
	// it aside (kept as .prev, never destroyed).
	hp := handoffPath(name)
	if _, err := os.Stat(hp); err == nil {
		_ = os.Rename(hp, hp+".prev")
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
