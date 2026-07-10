package main

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

// This file is the session-5 fleet-workflow surface: done (merge back &
// clean), review (handoff to a reviewer agent), recap (the 10-second
// catch-up), wpin (titled pinned splits). Research: docs/COMPETITORS.md —
// review/merge is the real bottleneck of parallel agents, re-orientation
// is the hidden tax.

// ---- done ---------------------------------------------------------------------

// cmdDone merges a worktree agent's branch back into its repo's checked-out
// branch, then cleans up: kill session, remove worktree, delete branch, drop
// the spec. One command instead of five — and the guards mean it refuses
// rather than loses work.
func cmdDone(args []string) int {
	fs := flag.NewFlagSet("done", flag.ExitOnError)
	squash := fs.Bool("squash", false, "squash-merge instead of --no-ff")
	keepBranch := fs.Bool("keep-branch", false, "don't delete the branch after merging")
	force := fs.Bool("force", false, "proceed even with uncommitted changes in the worktree")
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return fail(errf("usage: muster done <name> [--squash] [--keep-branch] [--force]"))
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no such agent %q", name))
	}
	if s.Worktree == "" || s.Branch == "" {
		return fail(errf("%s has no worktree — done is for worktree agents (kill --rm removes plain ones)", name))
	}
	if out, gerr := git(s.Worktree, "status", "--porcelain"); gerr == nil && out != "" && !*force {
		return fail(errf("%s has uncommitted work in %s — tell the agent to commit (muster send %s 'commit your work'), or --force to merge without it", name, s.Worktree, name))
	}
	merged := "nothing to merge (no commits ahead)"
	if branchAhead(s.Repo, s.Branch) {
		merged, err = mergeBranch(s.Repo, s.Branch, name, *squash)
		if err != nil {
			return fail(err)
		}
	}
	fmt.Println(merged)
	if tmuxHasSession(s.TmuxSession) {
		if err := tmuxKillSession(s.TmuxSession); err != nil {
			return fail(err)
		}
		fmt.Println("killed", s.TmuxSession)
	}
	if err := worktreeRemove(s.Repo, s.Worktree); err != nil {
		if !*force {
			return fail(err)
		}
		fmt.Println("worktree remove failed (--force: continuing):", err)
	} else {
		fmt.Println("removed worktree", s.Worktree)
	}
	if !*keepBranch {
		if out, err := git(s.Repo, "branch", "-D", s.Branch); err != nil {
			fmt.Println("branch delete:", out)
		} else {
			fmt.Println("deleted branch", s.Branch)
		}
	}
	if s.SessionUUID != "" {
		clearStatus(s.SessionUUID)
		clearEvents(s.SessionUUID)
	}
	if err := deleteSpec(name); err != nil {
		return fail(err)
	}
	fmt.Println("done —", name, "retired")
	return 0
}

// ---- review -------------------------------------------------------------------

// cmdReview hands an agent's branch to a reviewer agent over the mesh: a
// message with the branch, the checkout path, commits and diffstat, and
// reply instructions. The reviewer reads the actual worktree — no patch
// pasting, no context loss. Default reviewer: the first live agent whose
// role mentions "review".
func cmdReview(args []string) int {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	by := fs.String("by", "", "reviewer agent (default: first live agent with 'review' in its role)")
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return fail(errf("usage: muster review <name> [--by <reviewer>]"))
	}
	name := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if err := requirePpz(); err != nil {
		return fail(err)
	}
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no such agent %q", name))
	}
	reviewer, err := findReviewer(*by, name)
	if err != nil {
		return fail(err)
	}
	msg := reviewRequest(s)
	if err := ppzSend(reviewer.PpzHandle, msg, "--request-ack"); err != nil {
		return fail(err)
	}
	fmt.Printf("review handed to %s — reply lands in the mstrctl inbox (M in the UI)\n", reviewer.Name)
	return 0
}

func findReviewer(by, exclude string) (*AgentSpec, error) {
	if by != "" {
		r, err := loadSpec(by)
		if err != nil {
			return nil, errf("no such agent %q", by)
		}
		if r.PpzHandle == "" {
			return nil, errf("%s is not on the mesh", by)
		}
		return r, nil
	}
	specs, err := listSpecs()
	if err != nil {
		return nil, err
	}
	for _, r := range specs {
		if r.Name == exclude || r.PpzHandle == "" || !tmuxHasSession(r.TmuxSession) {
			continue
		}
		if strings.Contains(strings.ToLower(r.Role), "review") {
			return r, nil
		}
	}
	return nil, errf("no live reviewer found — spawn one (muster spawn peter --role reviewer) or pass --by <agent>")
}

// reviewRequest builds the handoff message: everything a reviewer agent
// needs to find and judge the work, and how to answer.
func reviewRequest(s *AgentSpec) string {
	var b strings.Builder
	b.WriteString("REVIEW REQUEST from mstrctl: review " + s.Name + "'s work")
	dir := s.Dir
	if s.Branch != "" {
		b.WriteString(" on branch " + s.Branch)
	}
	if s.Worktree != "" {
		dir = s.Worktree
		b.WriteString(" (repo " + s.Repo + ")")
	}
	b.WriteString(". Checkout: " + dir + " — read the code there directly.")
	if s.Worktree != "" && s.Repo != "" {
		if base, err := repoHeadBranch(s.Repo); err == nil {
			if log, err := git(s.Worktree, "log", "--oneline", base+"..HEAD"); err == nil && log != "" {
				lines := strings.Split(log, "\n")
				if len(lines) > 6 {
					lines = append(lines[:6], fmt.Sprintf("(+%d more)", len(log)-6))
				}
				b.WriteString(" Commits: " + strings.Join(lines, " · ") + ".")
			}
			if stat, err := git(s.Worktree, "diff", "--shortstat", base+"...HEAD"); err == nil && stat != "" {
				b.WriteString(" Diff vs " + base + ": " + stat + ".")
			}
		}
	}
	b.WriteString(" Reply to mstrctl (ppz send mstrctl '...') with verdict APPROVE or CHANGES plus specifics, max 10 lines.")
	return b.String()
}

// ---- recap --------------------------------------------------------------------

// cmdRecap prints the 10-second catch-up for one agent: who it is, what
// state it's in and why, what its worktree looks like, what happened
// recently (hook events), and what's waiting in its inbox. The antidote to
// scrollback archaeology after 10 minutes away.
func cmdRecap(args []string) int {
	if len(args) != 1 {
		return fail(errf("usage: muster recap <name>"))
	}
	s, err := loadSpec(args[0])
	if err != nil {
		return fail(errf("no such agent %q", args[0]))
	}
	state, reason := liveState(s, ppzWho())
	head := fmt.Sprintf("%s %s — %s", stateGlyph(state), s.Name, state)
	if reason != "" && reason != "heartbeat" {
		head += " (" + reason + ")"
	}
	fmt.Println(head)
	if s.Role != "" {
		fmt.Println("  role   ★", s.Role)
	}
	if u := loadUsage(s.SessionUUID); u != nil && u.Model != "" {
		fmt.Printf("  usage  %s · ctx %d%%\n", u.Model, int(u.CtxPct))
	}
	dir := collapseHome(s.Dir)
	if s.Branch != "" {
		dir += "  ⎇ " + s.Branch
	}
	fmt.Println("  dir   ", dir)
	fmt.Println("  cmd    $", shJoin(s.Argv))

	if s.Worktree != "" && s.Repo != "" {
		if base, err := repoHeadBranch(s.Repo); err == nil {
			if stat, err := git(s.Worktree, "diff", "--shortstat", base+"...HEAD"); err == nil && stat != "" {
				fmt.Println("  diff  ", strings.TrimSpace(stat), "vs", base)
			}
		}
		if out, err := git(s.Worktree, "status", "--porcelain"); err == nil && out != "" {
			fmt.Printf("  git    %d uncommitted file(s)\n", len(strings.Split(out, "\n")))
		}
		if log, err := git(s.Worktree, "log", "--oneline", "-3"); err == nil && log != "" {
			for i, l := range strings.Split(log, "\n") {
				pre := "  last  "
				if i > 0 {
					pre = "        "
				}
				fmt.Println(pre + l)
			}
		}
	}

	if evs := loadEvents(s.SessionUUID, 10); len(evs) > 0 {
		fmt.Println("\n  recent events")
		for _, e := range evs {
			line := fmt.Sprintf("   %s  %-8s %s", e.TS.Local().Format("15:04:05"), e.State, e.Event)
			if e.Reason != "" {
				line += " — " + firstLine(e.Reason)
			}
			fmt.Println(line)
		}
	} else {
		fmt.Println("\n  no hook events recorded (non-claude harness, or fresh spawn)")
	}

	if s.PpzHandle != "" && ppzReady() {
		msgs := ppzReread(s.PpzHandle+".inbox", "24h")
		shown := 0
		for i := len(msgs) - 1; i >= 0 && shown < 3; i-- {
			e := msgs[i]
			if e.Subject == "ack:read" || e.Sender == "" {
				continue
			}
			if shown == 0 {
				fmt.Println("\n  inbox (last 24h)")
			}
			ts := ""
			if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
				ts = t.Local().Format("15:04")
			}
			fmt.Printf("   %s %s: %s\n", ts, e.Sender, firstLine(e.Payload))
			shown++
		}
	}
	return 0
}

// ---- wpin ---------------------------------------------------------------------

// cmdWpin: `muster wpin <agent> <right|down|left> <target-pane>` — a pinned
// split showing the agent's live terminal, with the pane TITLED after the
// agent (pinned panes named "hostname" was a known issue). Self-exec'd from
// menus so the UI and CLI can't disagree.
func cmdWpin(args []string) int {
	if len(args) < 3 {
		return fail(errf("usage: muster wpin <agent> <right|down|left> <target-pane>"))
	}
	name, dir, target := args[0], args[1], args[2]
	s, err := loadSpec(name)
	if err != nil {
		return fail(errf("no such agent %q", name))
	}
	split := []string{"split-window", "-d", "-P", "-F", "#{pane_id}"}
	switch dir {
	case "right":
		split = append(split, "-h")
	case "down":
		split = append(split, "-v")
	case "left":
		split = append(split, "-h", "-b")
	default:
		return fail(errf("bad direction %q", dir))
	}
	split = append(split, "-t", target, attachCmd(s.TmuxSession))
	id, err2 := tmuxRun(split...)
	if err2 != nil {
		return fail(errf("split-window: %s", id))
	}
	if id = strings.TrimSpace(id); id != "" {
		_, _ = tmuxRun("select-pane", "-t", id, "-T", name)
	}
	return 0
}
