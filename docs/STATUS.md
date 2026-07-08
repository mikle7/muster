# STATUS — muster

> Ongoing handoff doc. Any agent picking this up: read this file first, then
> `DESIGN.md` (decisions), `PLAN.md` (phases), `RESEARCH.md` (why).

**Last updated:** 2026-07-09 (session 3 — spaces, right-click, agent
usage badges, roles + standup, pipes-first mesh view. Live multi-agent
comms E2E PASSED.)

## Session 3: the team release

User direction: folder-you-open = space (herdr-style); no path typing;
herdr's right-click splits + worktree menus; model/context/5h per agent;
pipes must go from "confusing CLI" to first-party legibility; and the END
GOAL stated explicitly: named agents (Alice, Dave, Peter…) with roles that
message each other BY NAME, run standups, hand off reviews. Standup is an
example of what must be possible, not a dedicated panel.

Shipped (all committed; binary installed to ~/.local/bin/muster):

- **Spaces**: `muster` run inside a repo auto-registers it as a project
  (subdir → repo root), records it as the current space (`space` file in
  state dir, re-pointed every launch), sidebar floats it first with a ●.
  Spawn forms preselect it.
- **Repo picker**: `[+ project]`/`P` = filter-as-you-type list of git repos
  discovered under `MUSTER_REPO_ROOTS` (default ~/Repos ~/code ~/src …),
  click or enter to add; literal `~/`|`/` input still accepted.
- **Right-click** (tmux display-menu at pointer — native rendering/keys):
  agent → type-into / split right·down·left (pins an EXTRA live nested
  client so several agents are visible at once) / zoom / send / schedule /
  inbox / kill (resume+kill when dead). project → new agent, new worktree
  agent (branch prefilled `wt-<hex4>`), remove from sidebar. Menu→TUI
  roundtrip via hidden F6/F7/F8 keys + menuProj state. `z` = zoom key.
- **Usage badges**: injected settings now include a `statusLine` command
  (`muster hook status-line`) — Claude pipes model/context%/rate-limits
  JSON per update; muster persists `status/<uuid>.usage.json` and renders
  the agent's in-pane status line. Sidebar rows show ctx% (colored ≥60/85),
  detail shows model (`Haiku 4.5 · ctx 18%`), header shows account-wide
  `5h N%→HH:MM`. Settings are REGENERATED at every launch (upgrades reach
  agents without re-running init). herdr does NOT have this (verified —
  its detection is screen-scrape state only): differentiator.
- **Roles + team briefing**: `spawn --role` / form field; mesh briefing now
  includes own role + teammate roster (name+role, recomputed each launch)
  + the STANDUP protocol. `muster standup` = broadcast with a reporting
  prompt; replies are ordinary mstrctl inbox traffic (T in the TUI).
- **Pipes first-party**: header shows `pipes ok/off` live; `M` (or click
  the title) = mesh view: raw `ppz status`, team table (liveness·state +
  roles, foreign handles marked ·ext), recent messages to mstrctl,
  schedules, caps. When pipes is OFF it's a guided connect screen:
  [1] start daemon, [2] `ppz login pipescloud.io` run INTERACTIVELY in the
  agent pane (device flow, browser). ppzReady now cached 10s;
  ppzCmd sets NO_COLOR + PPZ_UPDATE_CHECK=0.
- **Polish**: `o` toggles attention-sort (blocked→working→idle→dead, flat)
  vs project grouping; macOS notification on transition INTO blocked
  (MUSTER_NOTIFY=0 disables).

### Live E2E evidence (2026-07-09, real local mesh + real haiku agents)

Spawned alice (role: manages the ibex admin backoffice, repoA) via the
FORM and dave (game dev for pixel studios, gamesrv) via CLI. Dave's
composed argv contained the roster ("Teammates: alice (manages the ibex
admin backoffice)"). Then:
`muster send dave "ask alice what she's working on, report as RELAY:"` →
70s later mstrctl inbox held `dave  RELAY: alice is working on the ibex
user-permissions page redesign` (dave messaged alice BY NAME, she
answered, he relayed; ack:read receipts throughout). `muster standup` →
both replied within 10s in Task/Progress/Blockers/Next format. Mesh view
showed status/team/messages live; usage files showed Haiku 4.5, ctx
18-19%, 5h 4% with reset time rendered in the header. Splits: alice in
main pane + dave pinned below simultaneously (2 nested clients). Picker,
space ●, F7 branch-prefill, priority sort, headless right-click all
verified by capture. Unit tests + vet green.

## What this is

tmux-first agent-team manager on the ppz mesh. Single Go binary, deps =
charmbracelet only, no daemon. Workspace mode: sidebar TUI pane + the
selected agent's REAL terminal (nested tmux client) — see DESIGN.md.

## Environment on this machine

- Repo `Repos/pipe-terminal/muster`; build `go build -o muster .`
- Local ppz mesh RUNNING (server :8080, NATS :4222, Postgres `ppz`).
  `.dev/ppz-local/start.sh` after reboot; NEVER regenerate
  `.dev/ppz-local/nats.env`. ppz CLI symlink: `~/.local/bin/ppz`.
- User's live fleet: `pixel` + `tester` (~/.local/share/muster/). Scratch
  testing: MUSTER_STATE_DIR + `-L mstrtest` + **MUSTER_NOTIFY=0** (else
  test agents pop real desktop notifications — learned the loud way).
- Mesh handle pollution from tests: alice/dave/worker*/runner… exist on
  the local mesh's who list (offline). Harmless; `ppz source rm` if tidy.

## Known issues / next session TODO

- **Human dogfood still pending** (all E2E is headless): real-mouse feel,
  display-menu placement (x,y offset guessed +2 for the border row — may
  need nudging), login-in-pane flow with a real browser, notification UX.
- Pinned split panes get default titles (hostname), not the agent name —
  display-menu one-liner can't set -T on the new pane. Polish someday.
- The form command field scrolls horizontally when long (textinput
  behavior) — looks like truncation in captures; it isn't. Fine.
- 5h % arrives only after an agent's first API response (Pro/Max only) —
  header hides it until then. rate_limits needs Claude Code ≥2.1.191.
- Inbox/schedules/help still clipped to 38 cols (popup idea deferred).
- tmux-resurrect stale mstr-* interplay unchanged (session-1 note).

## Candidate next steps (in value order)

1. Human dogfood the team workflow: real fleet, roles for pixel/tester,
   run a real standup, review handoff (the Peter-reviews-a-PR loop).
2. `muster done <name>` (merge → kill → rm worktree) + a "review this
   branch" one-key handoff to a reviewer-role agent.
3. Split-pane titles + display-menu position tuning after dogfood.
4. Point the mesh at hosted pipescloud.io for always-on schedules.
