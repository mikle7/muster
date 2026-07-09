# STATUS — muster

> Ongoing handoff doc. Any agent picking this up: read this file first, then
> `DESIGN.md` (decisions), `PLAN.md` (phases), `RESEARCH.md` (why).

**Last updated:** 2026-07-09 (session 4 — rooms + read receipts, right-click
fixed & extended to the agent pane, skip-permissions default, recursive repo
discovery, quick terminal, file viewer, keymap. Headless E2E incl. live
mesh read-receipt PASSED.)

## Session 4: rooms, right-click everywhere, quality-of-life

User issue list: read receipts; pipes as "rooms"; right-click only worked on
the sidebar and closed on button-release; crowded detail panel; agents should
default to --dangerously-skip-permissions (configurable); clicking a space
should show a slack-like chat of its agents; keyboard map; repo picker missed
nested projects (repos/PixelPioneers/*); quick terminal for dev servers/
migrations; fast viewing of files agents mention (md first-class).

Shipped (built, tested, installed):

- **Rooms**: a project IS a room. Left-click a project row → the right pane
  becomes `muster room <proj> --watch` (bubbletea viewport, 2s refresh,
  wheel/q): every mesh message to any member agent (+ member→you traffic)
  in the last 24h as one chat — day dividers, `you → dummy`, and **✓✓ read
  receipts** from ppz ack:read envelopes. The `+` at the row's end still
  opens the spawn form; esc or selecting an agent leaves. Room ownership of
  the pane is a `room:` lastTarget + `roomView` model field so the 2s tick
  doesn't clobber it (that WAS a bug — retarget reconciles from selection).
  CLI: `muster room <proj>` prints the transcript.
- **Right-click fixed + right pane**: menus now open on mouse RELEASE — a
  tmux menu opened while the button is down dies the moment you let go
  (that was the "closes on release" bug). For the agent pane: bootstrap now
  binds root-table `MouseUp3Pane` (unbound in stock tmux) guarded by
  `session==muster && pane_current_command!=muster`; outside muster it
  replicates the unbound default (forward when mouse_any_flag). In-muster it
  runs `muster rmenu <pane> <mouse_x> <mouse_y>`, which maps pane_tty →
  nested client → mstr-* session → agent, then shows a self-contained menu:
  send (tmux command-prompt), open-a-file, terminal-here, zoom, inbox
  (display-popup 80%×70% — also dodges the 38-col clip), schedule, kill
  (confirm-before). No sidebar roundtrip. Claude panes grab the mouse
  (verified mouse_any_flag=1) so the pass-through press is harmless.
- **skip-permissions default ON**: `injected()` adds
  `--dangerously-skip-permissions` at compose time (NEVER into Argv — the
  faithful-resume guarantee; deduped if the user's argv has it). New
  `<state>/config.json` ({skip_permissions, repo_roots, repo_depth}), env
  MUSTER_SKIP_PERMISSIONS=0 overrides; `muster init` writes defaults.
- **Recursive repo discovery**: picker now WalkDirs each root to repo_depth
  (default 3), pruning dot-dirs/node_modules/vendor/etc and not descending
  into found repos — so repos/PixelPioneers/<proj> shows up. Roots: env >
  config > defaults.
- **Quick terminal**: `t` (and right-click "terminal here" on agents AND
  projects) opens a 12-line shell strip under the agent pane, cwd = agent/
  project dir — dev servers, migrations, git.
- **File viewer**: `v` (or right-click "open a file…") scrapes path-looking
  tokens from the agent's screen (+300 lines scrollback), stats them against
  the agent's cwd, and menus the hits (last-mentioned first, max 12). Pick →
  pager in a split beside the agent (glow for .md if installed, else bat,
  else less; images/PDFs → macOS `open`). q closes the split; prefix+z
  fullscreens. Chat + reading side by side, as requested.
- **Detail panel decluttered**: one fact per line — glyph+name+state+age /
  model+ctx+unread (or the blocked reason, promoted, in red) / dir+branch /
  ★role-or-$cmd.
- **Keymap**: docs/KEYMAP.md — design rules (tmux-native, vim motion,
  uppercase = wider blast radius, every menu item names its key) + full
  tables + deliberate future keys. `?` help updated (user's prefix is C-a,
  so help says "prefix" not C-b).

### Session 4 E2E evidence (headless, scratch server + stub claude)

Composed spawn showed `--dangerously-skip-permissions` while the stored spec
argv stayed `['claude']`. Picker listed repos/PixelPioneers/{game-one,two}
(depth 2) and pruned node_modules. Project click → right pane ran
`muster room` (title #proj, member list, empty-state), survived refresh
ticks, and clicking the agent restored the real nested client. `t` opened a
zsh split in the agent's dir. Right-pane tty resolved to mstr-dummy (rmenu
chain). LIVE mesh: `muster send dummy` + a real inbox read produced
`you → dummy ✓✓` in the room transcript. go vet/test/gofmt clean.

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
  the local mesh's who list (offline). Harmless; `ppz source destroy
  <handle>` cleans up (there is no `source rm`).

## Known issues / next session TODO

- **Human dogfood still pending** (all E2E is headless): real-mouse feel,
  login-in-pane flow, notification UX, room chat with a real multi-agent
  conversation.
- ~~rmenu placement~~ FIXED (session 5): `#{mouse_x}/#{mouse_y}` are
  PANE-relative but `display-menu -x/-y` are client-absolute, so the agent
  menu opened a sidebar-width left of the pointer. rmenu now adds
  `#{pane_left}/#{pane_top}` (fetched in the existing display-message call);
  numeric `-y` anchors the menu's BOTTOM edge. Also added herdr-style
  split right/down/up/left (l/j/u/h) to the agent-pane menu — plain shell
  in the agent's dir, focused (herdr semantics; the sidebar menu's splits
  still pin extra views of the agent). Headless-verified: menu at pointer
  in the right pane, split right spawns zsh at pane_left=120.
- cmd+click on file paths is terminal-emulator territory (iTerm semantic
  history), not reachable from tmux — `v` / right-click is the muster way.
  glow isn't installed on this machine; .md falls back to bat (fine).
- room shows member-inbox traffic only (no dedicated room pipe); an agent
  messaging someone OUTSIDE the room shows in the recipient's room, not
  the sender's. Acceptable until rooms get their own broadcast pipe.
- command-prompt inputs with double quotes would break rmenu's send/cron
  shell templates (user typing into their own shell — not a boundary).
- Sidebar `i` inbox view still clipped to 38 cols (rmenu's popup inbox
  isn't); consider popups for sidebar inbox/schedules too.
- Pinned split panes get default titles (hostname), not the agent name.
- 5h % arrives only after an agent's first API response (Pro/Max only);
  rate_limits needs Claude Code ≥2.1.191.
- tmux-resurrect stale mstr-* interplay unchanged (session-1 note).

## Candidate next steps (in value order)

1. Human dogfood: fleet with roles, real standup, room chat during a
   multi-agent task, review handoff (the Peter-reviews-a-PR loop).
2. `muster done <name>` (merge → kill → rm worktree) + a "review this
   branch" one-key handoff to a reviewer-role agent.
3. Keymap future keys: `/` filter, `u` jump-to-blocked, number jumps.
4. Menu position tuning + pinned-pane titles after dogfood.
5. Point the mesh at hosted pipescloud.io for always-on schedules.
