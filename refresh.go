package main

// Context refresh: the user's clear-not-compact workflow, first-class.
// Compaction is lossy summarization nobody reviews; a /clear plus a
// structured handoff keeps agent quality AND you choose what survives.
// The cycle: ask the agent to flush its handoff file → wait for idle →
// type /clear into its pane → the SessionStart hook (source:"clear")
// adopts the rotated session id and re-injects the handoff → kick it to
// continue. The identity briefing survives on its own (process flag).
// Auto mode (TUI) fires this when an agent is idle past the ctx threshold —
// never mid-task.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ---- in-flight marker ---------------------------------------------------
// The TUI ticks every 2s; without a claim marker it would launch a second
// refresh before the first one's flush prompt even lands. Stale markers
// (crashed refresh) expire.

const refreshMarkTTL = 10 * time.Minute

func refreshMarkPath(name string) string {
	return filepath.Join(dataDir(), "refresh", name+".json")
}

func setRefreshMark(name string) {
	if err := os.MkdirAll(filepath.Dir(refreshMarkPath(name)), 0o755); err != nil {
		return
	}
	b, _ := json.Marshal(map[string]time.Time{"ts": time.Now()})
	_ = atomicWrite(refreshMarkPath(name), b)
}

func clearRefreshMark(name string) { _ = os.Remove(refreshMarkPath(name)) }

func refreshInFlight(name string) bool {
	b, err := os.ReadFile(refreshMarkPath(name))
	if err != nil {
		return false
	}
	var mk map[string]time.Time
	if json.Unmarshal(b, &mk) != nil {
		return false
	}
	return time.Since(mk["ts"]) < refreshMarkTTL
}

// ---- auto trigger ---------------------------------------------------------

// refreshCtxPct: auto-refresh threshold (context used %). 0 disables.
// Env MUSTER_REFRESH_PCT > config refresh_ctx_pct > default 75.
func refreshCtxPct() int {
	if v := os.Getenv("MUSTER_REFRESH_PCT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	if c := loadConfig(); c.RefreshCtxPct != nil {
		return *c.RefreshCtxPct
	}
	return 75
}

// shouldAutoRefresh: idle claude agent past EITHER threshold — a context-used
// percentage (pctThreshold) OR an absolute context-token ceiling (tokThreshold,
// tokens = current context tokens, 0 when Claude Code didn't report them).
// Two triggers because % alone can't see absolute cost: a coordinator on a 1M
// window sits at 52% (~520k tokens) — under a 75% pct threshold yet already
// expensive per turn, and it would ride there until Claude's own lossy
// auto-compaction kicked in. The token ceiling catches that before compaction;
// the pct trigger still covers small-window agents (where tokens never reach
// the ceiling). Idle-only is the safety property — a working agent is never
// yanked mid-task; it gets refreshed the moment it next comes up for air.
// Either threshold at 0 disables that trigger; both 0 disables auto-refresh.
// Pure for testability.
func shouldAutoRefresh(harness, state string, ctxPct float64, pctThreshold int, tokens, tokThreshold int64) bool {
	if harness != "claude" || state != "idle" {
		return false
	}
	if pctThreshold > 0 && ctxPct >= float64(pctThreshold) {
		return true
	}
	return tokThreshold > 0 && tokens >= tokThreshold
}

// ---- the cycle ------------------------------------------------------------

// refreshWait: how long to wait for the agent to finish the handoff flush
// (it may be mid-task when asked — the prompt queues). MUSTER_REFRESH_WAIT_S
// overrides (tests use a stub claude and short waits).
func refreshWait() time.Duration {
	if v := os.Getenv("MUSTER_REFRESH_WAIT_S"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Minute
}

func waitFor(cond func() bool, timeout, poll time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(poll)
	}
	return cond()
}

func cmdRefresh(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: muster refresh <name>")
		return 2
	}
	name := args[0]
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no agent %q", name))
	}
	if s.Harness != "claude" {
		return fail(errf("%s is not a claude agent — refresh drives /clear", name))
	}
	if !tmuxHasSession(s.TmuxSession) {
		return fail(errf("%s is dead — resume it first", name))
	}
	setRefreshMark(name)
	defer clearRefreshMark(name)
	oldID := s.SessionUUID
	hp := handoffPath(name)
	_ = os.MkdirAll(filepath.Dir(hp), 0o755)

	fmt.Printf("refresh %s: requesting handoff flush…\n", name)
	flush := "Update your handoff file " + hp + " NOW: current task, state, key decisions, " +
		"file paths, exact next steps — everything needed to resume cold. Then stop and wait."
	if err := tmuxSendLine(s.TmuxSession, flush); err != nil {
		return fail(err)
	}
	sentAt := time.Now()
	idle := func() bool {
		st := loadStatus(oldID)
		return st != nil && st.State == "idle" && st.TS.After(sentAt)
	}
	if !waitFor(idle, refreshWait(), 2*time.Second) {
		// nothing destructive has happened yet — abort is free
		return fail(errf("%s never went idle after the flush request — refresh aborted before /clear, nothing lost", name))
	}

	fmt.Println("handoff flushed; clearing context…")
	if err := tmuxSendLine(s.TmuxSession, "/clear"); err != nil {
		return fail(err)
	}
	rotated := func() bool {
		s2, err := loadSpec(name)
		return err == nil && s2.SessionUUID != "" && s2.SessionUUID != oldID
	}
	if waitFor(rotated, 20*time.Second, time.Second) {
		fmt.Println("session id rotated + adopted; handoff re-injected via SessionStart hook")
	} else {
		fmt.Println("warning: no session-id rotation observed (hooks off? old claude?) — kicking anyway")
	}
	if err := tmuxSendLine(s.TmuxSession, "Continue working from your handoff notes."); err != nil {
		return fail(err)
	}
	fmt.Printf("%s refreshed: fresh context, briefing intact, handoff carried over\n", name)
	return 0
}
