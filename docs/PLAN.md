# PLAN — build order & progress

> Check boxes as you go. Keep STATUS.md's "Where we are" in sync.

## Phase 1 — skeleton + spawn/ls/attach/kill (no ppz needed) ✅
- [x] go.mod, main.go with flag.FlagSet subcommand dispatch
- [x] spec store (agents/*.json, atomic writes)
- [x] tmux wrapper (new-session -d -e, has-session =exact, send-keys
      "=name:" pane-target form, kill-session, display-menu)
- [x] `spawn` (argv verbatim in spec; claude composition w/ --session-id)
- [x] `ls` (spec ⋈ tmux liveness), `attach`, `kill`
- [x] unit tests: resume/spawn argv composition (the sacred transform)

## Phase 2 — faithful resume + hooks status ✅
- [x] `muster hook` sink + hooks settings file + `init`
- [x] verified against claude 2.1.204: SessionStart/Stop/Notification fire;
      --settings accepts a file; payload has hook_event_name/session_id
- [x] status merge into `ls` (+ dead/stale rules)
- [x] `resume <name>` / `--all`
- [x] LIVE TEST PASSED: real claude + --dangerously-skip-permissions +
      --model haiku; killed tmux session; resumed; "bypass permissions on"
      visible post-resume AND conversation memory intact (codeword recall)

## Phase 3 — ppz mesh integration ✅
- [x] detect ppz login; wrap spawns in `ppz terminal share`
      (named form first launch; `set handle` + bare form on resume — pty
      handle survives, E_SOURCE_TAKEN avoided, inbox/schedules preserved)
- [x] `send` / `broadcast` / `inbox` (control handle mstrctl)
- [x] `cron add/ls/rm` over ppz schedule
- [x] heartbeat agent_state merged into `ls` (ppz identifies harness=claude)
- [x] LIVE TEST PASSED: muster send → ppz pump nudged idle agent → agent
      ran `ppz subs read` itself → created file → replied DONE to mstrctl
      → ack:read receipts linked via in_reply_to

## Phase 4 — delivery + menu + worktrees ✅ (relay DELETED — see DESIGN)
- [x] relay replaced by ppz's built-in subs-alert pump + mesh briefing
      (--append-system-prompt) + ppz symlink into ~/.local/bin
- [x] `menu` (display-menu; graceful error outside a client), `doctor`
- [x] worktree spawn (--repo/-b, sibling __wt layout, mstr/ branch,
      .worktreeinclude copy), kill --rm dirty-guard + --force
- [x] LIVE TEST PASSED: cron --at +20s fired server-side → pump nudged →
      agent appended 'cron-fired' to file, unattended

## Phase 5 — polish & handoff
- [x] README.md
- [x] git init + first commit
- [x] update STATUS.md with what's done/left, known issues

## Session 2 (2026-07-08) — workspace mode  ✅ all E2E-tested headless
- [x] live interactive agent pane (nested tmux attach, switch-client retarget)
- [x] projects registry + sidebar grouping (`muster project`, projects.json)
- [x] mouse support, `[+ agent]`/`[+ project]` buttons, spawn + project forms
- [x] dead-agent placeholder pane, enter-resume, q/d quit/leave semantics
- [x] docs + install to ~/.local/bin
(see STATUS.md "Session 2" + DESIGN.md "Workspace mode")

## Session 3 (2026-07-09) — spaces, menus, usage, team  ✅ live comms E2E passed
- [x] cwd = space (auto-register, ● marker, form preselect) + repo picker
- [x] right-click menus (display-menu): agent splits/zoom/actions, project spawn/worktree
- [x] model + ctx% + 5h window via injected statusLine (usage.json per session)
- [x] roles + roster briefing + `muster standup`; alice/dave named-comms E2E proven
- [x] pipes mesh view (M): status/team/messages/schedules + guided connect
- [x] attention sort (o), blocked desktop notifications
(see STATUS.md "Session 3" + DESIGN.md "Session 3")

## Session 5 (2026-07-09) — the competitive sweep  ✅ vet/test/gofmt + headless E2E
- [x] researched 14 competitors (~80 sources) → docs/COMPETITORS.md
- [x] stalled state (⌛): working + no hook events for stall_after_min (config/env)
- [x] event history (status/<uuid>.events.jsonl) + `muster recap` + `e` popup
- [x] `muster done` — guarded merge back → kill → remove worktree+branch+spec (`D`)
- [x] `muster review [--by]` — branch handoff to a reviewer-role agent over the mesh (`w`)
- [x] worktree setup hook: .muster/setup runs in the pane before the agent (spawn only)
- [x] fleet triage: header state counts, `/` filter, `u` jump-to-attention, 1–9 jumps
- [x] known-issue fixes: inbox/recap popups (38-col clip), titled pinned splits (wpin)
(see STATUS.md "Session 5" + DESIGN.md "Session 5" + docs/COMPETITORS.md)

## Post-MVP backlog (do not build now)
- ~~bubbletea dashboard (`muster ui`)~~ → shipped, then reshaped into workspace mode (session 2)
- tmux status-right segment helper (`muster status --format tmux`)
- Claude agent-teams interop (adopt teammates as muster agents)
- adopt existing panes (`muster adopt`)
- non-claude resume flag table (codex --resume?, opencode, etc.)
- zellij backend behind the tmux interface
- ~~`muster done` (merge + cleanup one-shot à la workmux)~~ → shipped session 5
- jj (Jujutsu) worktree support (herdr D#480 has 21 votes — real demand)
- comparative review: N agents' attempts at one task, side-by-side diffs
  (the strongest unmet wishlist item in the research)
- stalled-state desktop notification (blocked-only today; wait for dogfood)
- hosted always-on agents via pipescloud + `ppz command` remote control
