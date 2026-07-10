# STATUS — muster

> Ongoing handoff doc. Any agent picking this up: read this file first, then
> `DESIGN.md` (decisions), `PLAN.md` (phases), `RESEARCH.md` (why).

**Last updated:** 2026-07-10 (session 9 — remote ppz-mesh agents now show in
the main sidebar, not just the M mesh view. vet/test/gofmt green; live
cross-machine E2E on the user's real 3-machine mesh PASSED. Branch
`feat/mesh-sidebar`, uncommitted here — pushed for review.)

## Session 9: mesh agents in the main sidebar

The user runs muster across 3 machines (this Mac + a Linux desktop + a Linux
server) sharing one ppz-server. Cross-machine agents already worked fine via
ppz directly (who/send/terminal watch), and the M mesh view already listed
everyone — but the main sidebar (`gatherRows` in cmds.go) built its row list
purely from local spec files (`listSpecs()`), so an agent running on a
*different* machine never appeared there.

Shipped:

- **`buildRows`** (cmds.go): `gatherRows` split into itself (I/O: listSpecs +
  ppzWho + ppzUnreadCounts) and a pure `buildRows(specs, hb, unread)` for
  testability. After the usual local rows, `remoteRows` appends a synthetic
  `lsRow` for every `ppzWho()` handle with no matching local spec — same
  "ours" distinction meshBody already computes, narrowed to actual agents
  (`heartbeat.harness != ""`; a bare human ppz login or muster's own
  `mstrctl` control handle isn't an agent and stays off the sidebar). New
  `lsRow.Remote`/`.Host` fields; Tmux/Dir/Branch/Wt stay zero — there's no
  local process or worktree behind these rows. An offline heartbeat maps to
  state `dead` regardless of its last-known `agent_state`.
- **Remote rows are visually marked** (`·ext`, meshBody's existing
  convention) in both the row list and the detail panel, which also swaps
  the (empty) dir/branch/cmd lines for host + a one-line action hint.
- **enter/l/tab** on a remote row pops up `ppz terminal watch <name>`
  (popupCmd, a new arbitrary-command sibling to the existing popupSelf)
  instead of trying to focus a local pane that doesn't exist. Dead-remote
  shows a "nothing to resume from here" status instead of attempting
  `muster resume` (which needs a local spec).
- **Local-only actions guarded** for a selected remote row instead of
  erroring: recap (e), refresh-context (f), review handoff (w), file menu
  (v/V), kill (K), resume (r) all show a plain-English "local-only" status
  message. `t` (terminal here) no longer silently blanks `m.space` when the
  selection is remote. Right-click's agent menu gets a third branch
  (remote / dead / local-live) instead of showing local-only items that
  would just fail.
- **Two bugs beyond the original plan, found while verifying it and fixed**:
  (1) `agentHandle()` (cmds.go) resolved a name to its ppz handle by
  requiring a *local* spec file — so `muster send`/`inbox`/`cron add`
  against a remote-only agent failed "no such agent" even though the row is
  visible and selectable. Now falls back to treating the name as a live
  mesh handle directly when no local spec exists. (2)
  `workspacePanes.retarget` (workspace.go) tried to tmux-attach an *empty*
  session string the instant a remote row was merely selected (not just
  entered) — `j`/`k` onto a remote row broke the right pane before this fix.
  Added a `remote:` target case with a friendly placeholder, mirroring the
  existing `dead:` case.

Tests: `session9_test.go` — `buildRows`/`remoteRows` (remote row shape,
no duplicate for a locally-spawned handle, non-agent/self filtering,
offline→dead, unread passthrough) and `agentHandle`'s mesh fallback via a
faked `ppz` binary (mirrors `TestPpzRunTimesOut`'s pattern).

### Session 9 E2E evidence

Headless local (scratch tmux + fake ppz binary simulating a mixed
local+remote mesh): sidebar showed a dead local agent and one `·ext` remote
row correctly bucketed and unduplicated; selecting the remote row rendered
the mesh-only detail panel and placeholder right pane (no broken tmux
attach); `K`/`e` on it produced the local-only status messages instead of
erroring; the pre-existing local kill-confirm prompt was unaffected.

**Live cross-machine** (real shared mesh, real agents): built + rsynced the
branch to an isolated scratch dir on the Linux desktop (mikle-linux.local)
over SSH, ran it under an isolated tmux server (`-L mstrtest`) + scratch
state dir so the user's real, attached `muster` session there was never
touched. `muster ls --json` and the live TUI both showed pixel-studios'
real ivy/jack/quinn/remy (running on the Mac) as `remote:true` rows with
correct host/harness/state/unread — go vet/build clean on Linux too. `s`
(send) on a remote row delivered a real mesh message to ivy end-to-end,
confirming the `agentHandle` fallback (ivy sent a heads-up afterward: it
was a labelled, harmless test message). `ppz terminal watch ivy` verified
standalone to stream her real live terminal. Scratch tmux server + dir
torn down after; the user's real `muster` session and `~/repos/muster`
checkout on that machine were never touched.

## Session 8: what's in that terminal + clear-not-compact

Two user asks: (1) herdr showed the branch/worktree per agent — muster's
sidebar only had names; (2) the user's context workflow (rolling handoff
md + /clear at high ctx%, never /compact) should be first-class and
automated. Verified against current Claude Code docs
(code.claude.com/docs/en/sessions, /hooks) before building:
**/clear ROTATES the session id**; SessionStart fires with
`source:"clear"` + the new id; SessionStart hook stdout
(hookSpecificOutput.additionalContext, 10k cap) is injected into the
fresh context; `--append-system-prompt` is a process flag so the
identity briefing survives /clear on its own. Note: docs don't
explicitly confirm the flag-persistence point — confirm during live
dogfood (agent should still know its name/role after a manual /clear).

Shipped (all tested, session8_test.go):

- **⎇ branch sub-line in the sidebar**: every agent row grows a dim
  second line with the LIVE checked-out branch (`liveBranch`: pure file
  reads of .git/HEAD, walks up from subdirs, resolves worktree gitdir
  files, detached → short sha — no git subprocess on the 2s tick).
  Worktree agents marked `·wt`. Implemented as its own sideItem kind
  ("sub") so hit-testing stays 1-line-per-item; clicking it selects its
  agent; j/k/1-9/u skip it. **Guard fix**: `done`/`D` and the menu item
  now key off the new lsRow.Wt (muster-created worktree), not
  Branch != "" — live Branch is set for ANY git checkout now.
- **Session-id adoption** (status.go): the hook sink, on SessionStart,
  re-points the spec at a rotated session id (MUSTER_AGENT names the
  agent; Argv untouched — faithful-resume tests still pin that). Events
  history migrates to the new uuid; stale status/usage dropped. Before
  this, a manual /clear silently froze status/ctx% and left resume
  targeting the pre-clear snapshot.
- **Handoff re-injection**: briefing now instructs agents to maintain
  `<state>/handoff/<name>.md` continuously (keyed by NAME — survives
  rotation). On SessionStart source=clear the sink emits
  additionalContext with the handoff tail (9k cap, latest wins) +
  orientation preamble. Missing file → guidance to reconstruct from git.
- **`muster refresh <name>`** (refresh.go; sidebar `f`, right-click
  "refresh context…"): flush prompt → wait idle (MUSTER_REFRESH_WAIT_S,
  default 300s; aborts safely pre-/clear on timeout) → /clear → wait for
  id rotation (20s, warns if none) → "continue from handoff" kick.
  In-flight marker `<state>/refresh/<name>.json` (10m TTL) prevents
  double-fires.
- **Auto-refresh**: TUI tick fires the cycle when an agent is
  claude+idle+ctx% ≥ threshold (`refresh_ctx_pct` config /
  MUSTER_REFRESH_PCT, default 75, 0 off). Idle-only = never yanks a
  working agent; it catches them next time they surface. Background
  self-exec so the tick never blocks.

### Session 8 E2E evidence (headless, Linux sandbox, scratch server)

`ls --json` showed the live branch and tracked an agent-side
`git checkout -b` mid-session; TUI capture showed `⎇ hotfix/live-switch`
under alice and `⎇ mstr/fix2 ·wt` under a worktree agent. Hook-driven:
PreToolUse under old uuid → SessionStart source=clear with rotated id →
spec re-pointed, argv untouched, events migrated
(PreToolUse+SessionStart under new uuid), old status/usage gone,
additionalContext JSON contained the handoff note; source=startup
emitted nothing. Full `muster refresh` cycle with faked status
transitions: flush prompt + /clear + kick all landed in the pane in
order, exit 0, marker cleaned. go vet/test/gofmt clean (14 new tests).

### Session 8 open questions for live dogfood

- Confirm briefing survival after manual /clear (docs silent on the
  flag-persistence point — ask alice who she is post-clear).
- `/clear` is typed into claude's input via send-keys; if a fuzzy
  autocomplete ever ranks another slash command above the exact match,
  the Enter would fire the wrong one. Watch the first live run.
- Auto-refresh threshold 75% is a first guess (statusline ctx% arrives
  only for claude agents). Tune like stall_after_min.
- ppz-side: consider a handoff-flush nudge via mesh instead of
  send-keys if typed prompts ever collide with a user mid-composition
  (agent input box is shared with the user by design).

## Session 7: what the rest of the market taught us

Research first: 4 parallel sweeps over ~80 primary sources across 14
competitors (herdr, claude-squad, uzi, Tmux-Orchestrator, agent-farm,
vibe-kanban, Crystal, Conductor, Sculptor, Omnara, OpenCode, Terragon,
Cursor BG agents, Codex cloud, Jules, container-use). Full evidence with
URLs: **docs/COMPETITORS.md**. Headlines: scraping-based status is every
tmux tool's top bug source (we're immune, keep it that way); "which agent
needs me" triage is the product; worktrees isolate code not environments;
review/merge is the real bottleneck; wrappers that hide the harness die.

(Section written as "session 5" before master's agent claimed 5–6;
renumbered to 7. The branch name stays session5-competitive.)

Shipped (developed on worktree .wt/session5, branch session5-competitive,
now merged here; all guarded by tests):

- **Stalled state (⌛)**: hooks say working but no event for
  `stall_after_min` (default 10; MUSTER_STALL_MIN; 0 off) → derived
  `stalled` at read time (applyStall, pure fn, tested). Ranks between
  blocked and working in attention sort; red in the sidebar; counted in
  the new header triage counts ("✋2 ⌛1 ⚙3").
- **Event history + recap**: hook sink appends every event to
  `status/<uuid>.events.jsonl` (128KiB trim → last 200). `muster recap
  <name>` = state+reason, role, usage, dir/⎇, cmd, worktree
  diffstat/uncommitted/last-commits, event timeline, recent inbox.
  Sidebar `e` and right-click "recap" open it in a display-popup.
- **`muster done <name>` [--squash|--keep-branch|--force]** (sidebar `D`,
  right-click on worktree agents): merge into the repo's checked-out
  branch → kill → remove worktree → delete branch → drop spec. Refuses on
  uncommitted worktree (msg suggests `muster send <name> 'commit…'`),
  refuses on dirty repo, aborts conflicts cleanly ("ask the agent to
  rebase"). Merge helpers unit-tested against real temp repos (happy,
  dirty-refusal, conflict-abort).
- **`muster review <name> [--by r]`** (sidebar `w`, right-click): mesh
  message to a reviewer agent (default: first live agent with "review" in
  its role) with branch, checkout path, commits, diffstat vs the repo's
  HEAD branch, and the reply protocol (APPROVE/CHANGES to mstrctl).
- **Worktree setup hook**: `.muster/setup` or `.muster-setup.sh` at repo
  root runs IN the pane, in the fresh worktree, BEFORE the agent (visible;
  best-effort; spawn-only; never in Argv — TestSetupNeverInArgv pins it).
- **Fleet triage**: `/` live filter (name/role/state/branch/dir), `u`
  jump-to-attention (blocked → stalled → unread), `1`–`9` positional
  jumps, header per-state counts. Sidebar `i` inbox + `e` recap use
  display-popups (kills the 38-col clip known-issue); pinned splits now
  titled with the agent name (`muster wpin`, self-exec'd from menus).

### Session 7 candidate-next-steps pass (2026-07-10)

The user asked for all of session 4's "candidate next steps". Steps 2–3
(done/review, filter/jump keys) shipped above. The rest:

- **Step 1 (dogfood), headless half DONE**: 3-agent fleet with roles
  across 2 projects, faked claude statuses — header `✋1 ⌛1 ⚙1`, `o`
  sort (blocked→stalled→working), `u` jump to dave with promoted
  permission_prompt reason, `3` jump to stalled peter, `/game` filter to
  dave by role, `D` on a non-worktree agent shows the friendly guard,
  TUI survives `e`/popup keys with no client attached. The live-mesh
  half (rooms etiquette, Peter-reviews-a-PR, real standup) CANNOT run
  here — no ppz binary/mesh in the sandbox → **docs/FOLLOWUP.md**.
- **Step 4 (menu tuning), code half DONE**: sidebar right-click menus now
  compute client coords from `#{pane_left}/#{pane_top}` (verified =0/1 in
  the workspace layout, matching the old +2 guess) instead of hardcoding;
  robust under zoom/splits. The by-feel placement check needs a real
  client → FOLLOWUP.md. Pinned-pane titles were fixed above (wpin).
- **Step 5 (pipescloud.io)**: purely operational (interactive browser
  device flow on the user's machine) — nothing to code; steps written in
  FOLLOWUP.md §3.

### Session 7 E2E evidence (headless, Linux sandbox, scratch server)

Spawned a worktree agent from a repo with `.muster/setup` → marker file
present in the worktree before the agent ran; `done` refused while
`b.txt` was uncommitted (guard msg), then merged `mstr/feat` into main
(--no-ff commit visible in log), removed worktree + branch + spec. Faked
a 25m-stale working status → `ls` showed `⌛ stalled (no events for
25m)`; `recap` rendered the event timeline (working→blocked→working);
TUI header showed `⌛1`, `/xyz` filter narrowed to the
nothing-matches empty state with the filter echoed in the header and the
`/ ›` input at the bottom. go vet + go test (9 new tests) + gofmt clean.
NOTE: sandbox tmux servers die between test shells — kill-path prints
weren't exercised; cmdDone reuses tmuxKillSession (session-1 tested).

### Review checklist for the user (this uncommitted merge)

1. `git diff --cached master` (the whole merge is staged, nothing committed).
2. Run `.dev/dogfood.sh`, then follow docs/DOGFOOD.md — it exercises every
   session-5/6/7 feature on a fresh fleet.
3. If good: `git commit` (the prepared merge message is in .git/MERGE_MSG).

## Session 6: rooms become a real shared channel (uncollared ppz pipe)

User demoed the token-doubling bug: "message everyone" fanned out N unicast
sends (`sendRoomCmd` loop), so each agent got what looked like a private DM,
couldn't see peers' replies, and independently fetched the same GitHub issue
list. Deep dive into ppz (WIRE.md, CHANGELOG, e2e tests, read.go) found the
proper primitive: **uncollared pipes** (v0.31+) are symmetric many-to-many
channels — per-session cursors (`cursors/<session>.json`), sender-attributed
tabular render (read.go treats uncollared like inbox), `ppz send LEAF`
resolves the uncollared pipe first. The old `broadcast` auto-pipe was removed
in v0.30 — the room.go comment citing it was stale. ppz has NO turn-taking/
locks/dedup; coordination is prompt-level protocol.

Changes (all live-verified: scratch tmux + scratch state + real local mesh,
`room-zztest` pipe, TUI send → agent `subs read` → in-room reply → TUI):

- **`roomPipe(proj)`** (ppz.go): `room-<proj>` squeezed into ppz's segment
  regex (32-char cap, dash rules) + `TestRoomPipe`. `ensureRoomPipe` creates
  it idempotently (E_PIPE_TAKEN ok; E_NAME_TAKEN = handle clash, surfaced).
  `subscribeRoom(session, pipe)` = `ppz subs add` under the agent's session.
- **`sendRoomCmd`** (room.go): ONE send to the room pipe (was N unicasts).
- **`gatherRoom`** unions the room-pipe history (`reread --since 24h`) with
  the existing member-inbox scan, so DMs/standup replies still show. Room
  messages render `sender → #proj`, no ✓✓ (shared-pipe ack semantics are
  per-reader and unverified — check before wiring ticks to rooms).
- **`launch()`** (cmds.go): creates the project's room pipe and subscribes
  the agent's ppz session before the harness starts — so ppz's built-in
  subs-alert nudge delivers room traffic. Agents spawned BEFORE this build
  need kill+resume to join their room (same operational note as session 5's
  skip-permissions).
- **`meshBriefing`** (spec.go): TEAM ROOM paragraph — reply in-room not to
  sender's inbox; addressed-agent-acts-alone; `CLAIMING: <task>` before
  whole-room work (kills the duplicate-fetch behavior); discuss and divide.

## Session 5: room chat becomes interactive, ppz-usage audit

User reported agents still prompting for permission despite the session-4
skip-permissions feature. Root cause: that feature landed at 16:28 today: it
only takes effect at spawn/resume (`composeSpawn`/`composeResume` in
spec.go), and muster never respawns a *live* agent — selecting one just
`switch-client`s to its existing tmux pane (`workspace.go` `retarget`). The
user's alice/george/terry were spawned at 09:15-09:23, hours before that
code existed, and are still running the old argv (verified via `ps`: no
`--dangerously-skip-permissions` on their live `claude` processes). Fix is
operational, not code: `muster kill <name>` then `muster resume <name>` (or
`r` in the sidebar on a killed agent) relaunches with current `injected()`
logic and picks up the flag; conversation is preserved via `--resume
<uuid>`. Not yet applied — killing a live, in-progress agent is the user's
call, left to them.

Also audited every ppz CLI call muster makes (ppz.go, cmds.go, room.go,
tui.go) against ppz's actual source (cmd/ppz, internal/cli, internal/daemon)
— subcommands/flags, JSON shapes, ack:read semantics, the terminal-share
handle-exists branch, `ppz subs read` vs muster's own `read`/`reread`, env
vars. All correct except one real bug, fixed:

- **`ppzReady()` false positive** (ppz.go): checked `strings.Contains(out,
  "logged in")` against `ppz status` text, but the *unauthenticated* state
  literally prints `"not logged in"` — a substring match. Daemon-up-but-
  logged-out was misreported as ready, so a fresh spawn would get wrapped in
  `ppz terminal share` and fail unauthenticated instead of showing the
  guided connect screen. Fixed to match the exact `"daemon: logged in"`
  line.
- Found but not fixed (logged for later, no user-visible urgency): `muster
  ls`'s unread badge is actually a lifetime message count, not real
  unread — mstrctl's ppz session never does a cursor-advancing `read`
  (everything goes through `reread`/`ls`), so the cursor never moves and the
  count never drops. `ppzReadInbox` (the one function that *would* do a
  cursor-advancing read) is dead code, unused since the muster-relay design
  was dropped. Candidate fix: run a cursor-advancing read when a room/agent
  is actually opened, Slack-style.

**Room chat is now a real group chat**, not just a transcript + the T-key
standup broadcast: `muster room <proj> --watch` (room.go) has a compose line
always focused at the bottom — type, hit enter, it fans out to every room
member's ppz handle (no server-side broadcast pipe exists, per
docs/WIRE.md, so this is client-side fan-out like `muster broadcast`, just
project-scoped). Since typing is now live, `q` no longer closes the room —
only esc (clears the draft, or closes if already empty) / ctrl-c do.
Send errors show on their own status line below the input, not appended
inline — an earlier version appended the error to the input line and it
silently corrupted the whole frame (header scrolled off-screen) whenever
the input+error text was long enough to soft-wrap past the pane width,
which desyncs bubbletea's alt-screen row bookkeeping. Verified via headless
tmux: failed send (nonexistent handle) renders a clean one-line error with
header intact; real send to the live `alice` handle landed and rendered as
`you → alice` in the transcript within ~2s. docs/KEYMAP.md's room-chat
section updated. go vet/test/gofmt clean.

## Prior sessions

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
- rooms are backed by a shared uncollared pipe since session 6; the view
  still unions member-inbox DMs, so an agent messaging someone OUTSIDE the
  room shows in the recipient's room, not the sender's. Acceptable.
- unread badge counts lifetime messages, not unread (see session 5 notes;
  `ppzReadInbox` is dead code awaiting the Slack-style cursor fix).
- command-prompt inputs with double quotes would break rmenu's send/cron
  shell templates (user typing into their own shell — not a boundary).
- ~~Sidebar `i` inbox clipped to 38 cols~~ fixed session 7 (popup); the
  `C` schedules list still renders in the sidebar (rarely long — fine).
- ~~Pinned split panes get default titles~~ fixed session 7 (wpin).
- Stall threshold (10m) is a guess — one long tool call (big build) can
  false-positive. Tune after dogfood; MUSTER_STALL_MIN=0 disables.
- `review` picks the FIRST live role~review agent; no round-robin.
- 2026-07-10 incident (9-agent fleet): laptop froze under load; NATS
  reconnect churn after the stall broke the ppz READ path
  (E_SERVER_UNREACHABLE) while who/status stayed up. Contributors: every
  agent's PTY repaints stream to JetStream (~2MB/10min per busy agent —
  a ppz-side throttle is FOLLOWUP material), and muster's 2s tick piled
  up hung ppz subprocesses. muster side fixed same day: every ppz call
  now has a hard timeout (ppzRun, default 10s, MUSTER_PPZ_TIMEOUT_MS)
  and the TUI refresh is single-flight (no stacking while one hangs).
  Recovery: `ppz daemon restart`, then `muster resume --all` for any
  agents whose terminal-share wrappers dropped.
- 5h % arrives only after an agent's first API response (Pro/Max only);
  rate_limits needs Claude Code ≥2.1.191.
- tmux-resurrect stale mstr-* interplay unchanged (session-1 note).

## Candidate next steps (in value order)

All five session-4 candidates are now either shipped or blocked on the
user's machine — the machine-bound remainder lives in **docs/FOLLOWUP.md**
(live-mesh E2E, real-mouse menu feel, pipescloud.io login, rebuild).

1. ~~Human dogfood~~ headless half done (session 7); live-mesh half →
   FOLLOWUP §1, real-mouse half → FOLLOWUP §2.
2. ~~`muster done` + review handoff~~ shipped session 7.
3. ~~Keymap future keys (`/`, `u`, 1–9)~~ shipped session 7.
4. ~~Menu position + pinned-pane titles~~ code half shipped session 7
   (pane_left/pane_top coords, wpin titles); feel check → FOLLOWUP §2.
5. ~~pipescloud.io~~ operational only → FOLLOWUP §3.

Fresh candidates after that: unread-badge cursor fix (FOLLOWUP §5),
comparative review of N attempts (PLAN backlog), stalled notifications.
