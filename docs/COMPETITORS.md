# COMPETITORS — pain-point sweep (2026-07-09)

> Four parallel research passes over ~80 primary sources: GitHub issues
> (sorted by reactions), full HN launch threads, practitioner blog posts,
> Cursor/OpenAI forums. Reddit blocks crawlers, so Reddit sentiment arrives
> second-hand via archives and blogs. This file is the evidence; the
> feature decisions it produced are in RESEARCH.md ("session 7") and
> DESIGN.md.

## Tools examined (14)

tmux-native: **herdr**, **claude-squad**, **uzi**, **Tmux-Orchestrator**,
**claude_code_agent_farm** (+ the Show-HN long tail: aoe, amux, jmux, zmx,
agent-deck, TBD).
GUI/web: **vibe-kanban**, **Crystal**, **Conductor**, **Sculptor**,
**Omnara**.
CLI/cloud platforms: **OpenCode**, **Terragon**, **Cursor background
agents**, **OpenAI Codex cloud**, **Google Jules**, **container-use**.

## The ranked pain list (cross-tool)

1. **Status detection via screen-scraping is fragile and slow.** claude-
   squad's top bug cluster is capture-pane crashes/freezes (#216 14👍, #51,
   #189 — adding Claude hooks *broke* its scraper; #215 — 5s keystroke lag);
   uzi's auto-confirm raced its own pane watcher (#9). herdr users report
   "waiting for a shell command shows as idle" (HN 48824139). Claude Code's
   own spinner "lies" (anthropics/claude-code#50665, #25979).
   → muster's hooks+heartbeats architecture is validated; never regress it.

2. **"Which agent needs me?" triage is the entire job.** The most-praised
   herdr feature is the sidebar answering exactly that ("agent sat idle
   waiting for approval for days" — HN 48825383; herdr#318 wants attention-
   priority sort). anthropics/claude-code#36885: "the bottleneck isn't
   Claude's speed — it's the human noticing Claude is waiting." The
   community heuristic for *stuck* is "is the token count still changing?"
   → gap: distinguishing *stalled* (working-but-silent) from working; jump-
   to-blocked; fleet search/filter (HN aantix: "I wish there were a way to
   search across all open tabs").

3. **Worktrees isolate code, not environments.** claude-squad #260 (setup
   hooks), Conductor's top HN thread (deps/env per workspace, submodules,
   Docker port collisions), Cursor secrets threads, Sculptor's whole
   container pitch, workz Show HN ("worktrees only isolate code"),
   incident.io's hand-rolled `w` script; "lifecycle hooks so I can have
   dedicated test databases" (HN 48823584).
   → muster has `.worktreeinclude`; missing: a per-repo setup hook that
   runs on worktree creation.

4. **Merge/cleanup flow is underserved; review is the real bottleneck.**
   uzi's praised `checkpoint` = one-command merge back. vibe-kanban issues:
   worktrees not cleaned after merge (#1764), ghost "running" after
   worktree deleted (#1571). Simon Willison: "the natural bottleneck is
   how fast I can review." Omnara HN: "My problem is QAing and reviewing
   the code all these agents write, and none of these tools solves that."
   favoboa's wishlist: compare N attempts side by side.
   → gap: `done` (merge→kill→clean) and a review handoff to a reviewer
   agent (muster uniquely has named agents + mesh messaging for this).

5. **Re-orientation cost when returning to an agent.** "It can be quite
   difficult to pick up context again after 10+ minutes" (TDS); Sculptor
   praised for per-agent history "without digging through backscroll".
   → gap: a recap surface (what's it doing, what happened recently, what
   does it want) — muster has hook events but discards history.

6. **Don't break the native harness.** Conductor HN: "I don't want an
   interface to replace CC — I just want a better way to manage the
   sessions"; GUI wrappers lose keybindings/plan-mode/slash-commands;
   vibe-kanban #2993: renaming a workspace bricks resume because sessions
   key on cwd. Anthropic blocked OpenCode's subscription auth (357👍
   issue); wrappers died to the 2026 SDK-pricing change while "drive the
   real CLI" modes were exempt.
   → muster's faithful-resume + real-terminal principles are the moat.
   Never own the PTY, never rewrite argv.

7. **Permission hell vs. no sandbox.** Tmux-Orchestrator #10 (agents stuck
   on prompts "defeating the purpose"); muster already defaults
   skip-permissions (configurable). Field reports warn isolation is the
   user's problem — stay honest about that in docs.

8. **Cost/quota anxiety multiplies with parallelism.** Codex "burning
   tokens" 279👍; Jules quota punishes failure; Crystal criticized for no
   Max-plan usage display. muster's statusLine usage badges (model/ctx%/5h)
   already lead here — keep per-agent ctx% prominent.

9. **Abandonment kills trust fast.** claude-squad's top two issues are
   "is this dead?"; Bloop/Terragon/Crystal all shut down. Shipping cadence
   is a feature.

10. **Remote/mobile glances.** ssh+tailscale-from-phone is a top reason to
    stay terminal-native (herdr HN); Codex's two most-upvoted issues ever
    are remote control of local sessions. muster inherits ssh+tmux attach
    for free; ppz hosted mesh covers push-style delivery later.

11. **Screen real estate at scale.** Panes unreadable past ~4 agents
    (anthropics/claude-code#25396 wants windows-per-agent). muster's
    one-agent-one-session + retargeted nested client already avoids this.

12. **Coordination failure modes.** Shared-context teams duplicate work
    and burn tokens (Tmux-Orchestrator #21); survivors converge on
    file-based state + escalation to a human. muster's named-agent mesh +
    standup is the right shape; keep human-in-the-loop defaults.

## What we shipped in response (session 7)

| finding | muster answer |
|---|---|
| stalled ≠ working (2) | derived `stalled` state: hooks say working but no event for `stall_after_min` (default 10) |
| triage at a glance (2) | header state counts; `u` jump-to-attention; `1`–`9` jumps; `/` fleet filter (name/role/state) |
| re-orientation (5) | event history (`status/<uuid>.events.jsonl`) + `muster recap <name>` + `e` popup + right-click item |
| env bootstrap (3) | `.muster/setup` (or `.muster-setup.sh`) runs in the pane on worktree creation, before the agent starts — visible, never stored in Argv |
| merge/cleanup (4) | `muster done <name>` — guarded merge into the repo's checked-out branch → kill → remove worktree+branch; `D` in the sidebar |
| review bottleneck (4) | `muster review <name> [--by r]` — handoff message (branch, diffstat, instructions) to a reviewer-role agent over the mesh; `w` key |
| 38-col clipping (own known issue) | sidebar `i` inbox and `e` recap open tmux display-popups |
| pinned panes unnamed (own known issue) | splits route through `muster wpin`, panes titled with the agent name |

## Primary sources (selection)

- claude-squad: github.com/smtg-ai/claude-squad issues #216 #51 #189 #215
  #151 #132 #214 #250 #86 #88 #260 #56 #89 #124 #60 #137 #119 #104 #98
- herdr: issues #1134 #231 #162 #323 #137 #261 #318; HN 48825383 48823155
  48824139 48826541 48824880 48716955 48823423 48824698 48715653;
  coles.codes/posts/herding-agents-with-herdr
- uzi: issues #11 #9 #12 #13 #2 #14
- Tmux-Orchestrator: issues #10 #12 #21; agent_farm #4
- vibe-kanban: HN 44533004; issues #2687 #2288 #1697 #1946 #1764 #1571
  #2472 #1830 #326 #306 #2993 #3255 #3408
- Crystal: issues #26 #89 #177 #51 #208 #200
- Conductor: HN 44594584 (esp. 44628011 44630721 44631450);
  madewithlove.com/blog/conductor-running-multiple-ai-coding-agents-in-parallel
- Sculptor: HN 45427697; imbue-ai/sculptor #182 #12
- Omnara: HN 44878650 46991591; issues #72 #55 #90 #62 #199 #103
- OpenCode: anomalyco/opencode #7410 #5887 #12661 #6152 #1543; sst/opencode
  #1247 #6573 #5241 #6310; HN 47460525 46625918 47444748
- Terragon: docs.terragonlabs.com/docs/resources/shutdown; HN 46316689 46589735
- Cursor: forum.cursor.com /t/113851 /t/116245 /t/104112 /t/141805 /t/139522
- Codex: openai/codex #14593 #13568 #10450 #9224 #4226 #12564 #13018 #4432
  #2847; HN 48140529
- Jules: HN 44813854 45466588; hyperdev.matsuoka.com/p/google-jules-first-look
- container-use: dagger/container-use #279 #304 #247 #256 #161; HN 44193933
- workflow genre: HN 45489884 (parallel-agent lifestyle, read in full);
  simonwillison.net/2025/Oct/5/parallel-coding-agents;
  steipete.me/posts/just-talk-to-it; hboon.com/using-tmux-with-claude-code;
  incident.io/blog/shipping-faster-with-claude-code-and-git-worktrees;
  zenvanriel.com/ai-engineer-blog/running-multiple-ai-coding-agents-parallel;
  pushtoprod.substack.com/p/stop-babysitting-your-ai-coding-agents;
  anthropics/claude-code #36885 #50665 #25979 #43696 #53922 #25396
