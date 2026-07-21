package main

// Merged ⇒ auto-reset: an idle agent whose branch work has landed is done —
// nobody should have to garden it back to usefulness by hand (the real
// failure: agents dangling for days at 40-50% context after their PR
// merged). The sweep runs off the TUI tick next to maybeWarmSpares and
// retasks such agents onto a fresh seeded context, marked as claimable
// spares. Full-auto per Michael's call 2026-07-17: no grace prompt, no
// notification — the parked handoff and the merged branch ARE the record.

import (
	"os/exec"
	"strings"
	"time"
)

// autoResetIdle: how long an agent must sit idle before the sweep touches
// it. Not a veto window — just anti-flap so a just-finished agent stays
// reachable for immediate follow-ups.
const autoResetIdle = 15 * time.Minute

// lastAutoResetSweep throttles the sweep: git subprocesses have no place on
// every 2s tick. The TUI is a single process, so a package var suffices.
var lastAutoResetSweep time.Time

func maybeAutoReset(rows []lsRow) {
	if time.Since(lastAutoResetSweep) < time.Minute {
		return
	}
	lastAutoResetSweep = time.Now()
	for _, r := range rows {
		if r.Harness != "claude" || r.Remote || r.Pending || r.Spare ||
			r.State != "idle" || r.Branch == "" || r.Repo == "" {
			continue
		}
		if refreshInFlight(r.Name) || retaskInFlight(r.Name) {
			continue
		}
		s, err := loadSpec(r.Name)
		if err != nil {
			continue
		}
		if !idleFor(s, autoResetIdle) || !workShipped(s) {
			continue
		}
		// background self-exec: the tick stays instant, and the UI acts
		// through the same verb the CLI exposes (runSelf principle).
		_ = exec.Command(selfExe(), "retask", r.Name, "--spare").Start()
	}
}

// idleFor: the agent's last hook event left it idle at least d ago.
func idleFor(s *AgentSpec, d time.Duration) bool {
	st := loadStatus(s.SessionUUID)
	return st != nil && st.State == "idle" && time.Since(st.TS) >= d
}

// workShipped: the branch got commits during this agent's tenure AND none
// remain unmerged. branchAhead compares against the repo's checked-out
// branch, which muster's own `done` flow merges into locally — so a local
// merge is detected on the next sweep, and a GitHub-merged PR is detected
// once the repo is pulled.
// ponytail: squash-merged GitHub PRs keep branchAhead true forever (new
// SHAs), so this sweep never catches them — add a throttled `gh pr view`
// fallback if that flow turns out to matter.
func workShipped(s *AgentSpec) bool {
	if s.Branch == "" || s.Repo == "" {
		return false
	}
	// a dirty worktree is work in flight — never touch it
	if out, err := git(s.Dir, "status", "--porcelain"); err != nil || out != "" {
		return false
	}
	// analysis-only agents never commit — their context is their value;
	// leave them alone
	out, err := git(s.Repo, "log", "-1", "--format=%H",
		"--since="+s.CreatedAt.Format(time.RFC3339), s.Branch)
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	return !branchAhead(s.Repo, s.Branch)
}
