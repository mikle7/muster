# DESIGN — muster

> tmux-first agent-army manager on the ppz mesh. Decisions here are final
> unless STATUS.md says otherwise. Rationale lives in RESEARCH.md.

## One-liner

Run an army of coding agents across repos and worktrees as plain tmux
sessions; see who needs attention at a glance; restart them EXACTLY as you
launched them; wire them together (and to your schedule) over ppz pipes.

## Principles

1. **tmux is the runtime.** We never own a PTY, never render terminal
   content, never intercept keys. Users keep their tmux config, bindings,
   copy-mode, resurrect/continuum, SSH story. "Attach" = `switch-client`.
2. **Faithful resume is sacred.** The exact user argv is stored verbatim at
   spawn and replayed verbatim on resume (+ the harness resume flag). Env
   overrides and cwd too. herdr's #965 can never happen here.
3. **Events over scraping.** Claude Code hooks are the status source of
   truth; ppz heartbeats cover wrapped non-hook agents; we do NOT ship
   screen-scraping manifests.
4. **No daemon.** State = tmux server (live procs) + ppz mesh (comms,
   schedules, heartbeats) + flat JSON files (specs, statuses). The only
   long-running muster process is the optional relay, which itself runs in
   a tmux session like everything else.
5. **ppz optional, magical when present.** Without ppz: spawn/ls/attach/
   resume/worktrees all work. With ppz: messaging, scheduling, cross-host
   roster, remote watch.

## Architecture

```
                    ┌───────────────────────────────┐
   muster CLI ──────│ tmux (user's default server)  │── user attaches/
   (one-shot cmds)  │  session mstr-<agent> each    │   detaches freely
        │           └───────────────────────────────┘
        │                     │ agent pane runs:
        │                     │ ppz terminal share <handle> -- <EXACT argv>
        ▼                     ▼
  ~/.local/share/muster/   ppz mesh (local daemon → server/NATS)
    agents/<name>.json       inbox/stdout/heartbeat pipes per agent
    status/<uuid>.json  ◄── claude hooks (muster hook) write these
                             schedules fire server-side (always-on)
```

- **Spec files** (`agents/<name>.json`): `{name, cwd, repo, worktree,
  branch, argv, env, harness, session_uuid, ppz_handle, tmux_session,
  created_at}`. argv is the user's command VERBATIM (muster-injected args
  are computed at launch, never stored into argv).
- **Status files** (`status/<session_uuid>.json`): `{state, event, ts,
  session_id}` written atomically by `muster hook` (invoked by Claude Code
  hooks; stdin JSON → file). States: `working | blocked | idle | error`.
  tmux-session-gone overrides everything → `dead`.
- **Launch composition** (harness=claude):
  `spawn argv = user_argv + [--session-id <uuid>] + [--settings <muster-hooks.json>]`
  `resume argv = user_argv + [--resume <uuid>] + [--settings <muster-hooks.json>]`
  For unknown harnesses: spawn/resume = user argv verbatim (resume flag
  table extensible in config).
- **ppz wrap**: when ppz is logged in, the pane command becomes
  `ppz terminal share <handle> -- <composed argv>` with
  `PPZ_SESSION=<handle>` set via tmux `-e`. Handle = agent name (sanitized).

## Status model & precedence

`dead` (no tmux session) > hook status file (fresh) > ppz heartbeat
agent_state > `unknown`. Hook freshness window: status file mtime younger
than the tmux pane's start time is stale → ignore. Blocked reasons kept in
the file (`permission_prompt`, `agent_needs_input`, `rate_limit`...).

Claude hook wiring (installed by `muster init` as a settings file passed
via `--settings` at launch — never touches user/project settings):
- PreToolUse / PostToolUse → working
- PermissionRequest → blocked (reason=permission)
- Notification (permission_prompt|agent_needs_input|idle_prompt) → blocked/idle
- Stop → idle
- SessionEnd → ended
All hooks run `muster hook` (reads stdin JSON, writes status file). Exact
event names verified against installed claude at build time.

## Command surface (MVP)

```
muster spawn <name> [-C dir] [--repo dir -b branch] [-e K=V]... [--] [cmd...]
       # default cmd from config (e.g. claude --dangerously-skip-permissions)
       # --repo+-b creates worktree <repo>__wt/<branch>, branch mstr/<branch>
muster ls [--json] [--watch]        # merged: spec + tmux + status + inbox
muster attach <name>                # switch-client inside tmux, attach outside
muster kill <name> [--rm]           # kill session; --rm also removes worktree
                                    #   (refuses if uncommitted/unpushed)
muster resume <name> | --all        # THE feature: exact argv + resume flag
muster send <name> <text...>        # → ppz send <handle> (from handle mstr-ctl)
muster broadcast <text...>          # → all live agents
muster inbox <name>                 # → ppz reread of agent inbox (peek)
muster cron add <name> (--every D|--cron E|--at T) <prompt...>
muster cron ls | rm <id>            # sugar over ppz schedule
muster relay                        # the pump: waits on inboxes, injects
                                    #   prompts into IDLE agents via send-keys
muster relay start|stop             # run relay inside tmux session mstr-relay
muster menu                         # tmux display-menu picker (bind a key)
muster hook                         # internal: hook sink (stdin json → status)
muster init                         # write hooks settings + print tmux snippet
muster doctor                       # env/mesh checks
```

## Message delivery (REVISED during build — no muster relay exists)

Original plan had a `muster relay` process injecting inbox payloads into
idle panes. DELETED: ppz's `terminal share` already ships a battle-tested
subs-alert pump — when anything subscribed is unread and the wrapped pane
has been idle ≥15s (30s cooldown, fire-time ConfirmUnread re-check), it
types "Please run 'ppz subs read' and action messages" into the pty with
the harness-correct submit key (needs PPZ_AGENT_HARNESS env, muster sets
it). The agent reads its own inbox → cursor + acks have the RIGHT
semantics (ack fires from the agent's read, not a proxy's).

muster's contributions to make that flow work:
1. `--append-system-prompt` mesh briefing injected at launch (mesh only):
   tells claude its handle, what the nudge means, how to reply
   (`ppz send <handle> '<text>'`). Not stored in Argv.
2. `ppz` symlinked into ~/.local/bin by `muster init` — shell profiles
   rebuild PATH (macOS path_helper), so tmux -e PATH injection does NOT
   survive into claude's Bash tool; a symlink does.
3. PPZ_AGENT_HARNESS + PPZ_SESSION pinned in the pane env.
Busy agents: pump waits (idle-gate); JetStream retains (24h/5000).
Tunables: PPZ_TERMINAL_INBOX_IDLE_MS / _COOLDOWN_MS.
Known edge: an agent that refuses/ignores the nudge gets re-nagged each
cooldown while unread remains (observed once with a poisoned pre-briefing
conversation; fresh spawns behave).

## Worktree rules

- Layout: `<repo>__wt/<branch>` sibling dir (visible, not hidden — herdr
  #261 lesson). Branch `mstr/<branch>` unless user gives full ref.
- Copy `.worktreeinclude`-matched gitignored files (`.env` problem).
- `kill --rm` refuses on uncommitted/untracked/unpushed unless --force.

## Stack

- Go 1.25, **stdlib only** for MVP (flag.FlagSet subcommands, encoding/json,
  os/exec shell-outs to tmux/git/ppz/claude). Single static binary.
- bubbletea dashboard = post-MVP (menu + ls --watch cover MVP).
- Tests: unit tests for spec/compose/resume-transform logic (pure funcs),
  plus a live smoke script.

## Workspace mode (session 2 — the interactive UI rethink)

User feedback on the session-1 TUI: "you can't type in the claude window
on the right… it should work as normal", plus clickable spawn/add-project.

**Decision: the right pane is tmux itself, not a widget.** `muster` now
bootstraps a dedicated `muster` tmux session: pane 0 is the sidebar TUI
(fixed 38 cols), pane 1 runs `TMUX= tmux attach -t =mstr-<sel>` — a real
nested client on the same server. Moving the selection retargets that
client with `switch-client -c <pane_tty>` (respawn-pane when it died).
Typing/scrolling/pasting in the right pane IS the agent's terminal.

Rejected alternatives, for the record:
- *Key-forwarding into a capture-pane preview* (send-keys per keystroke +
  fast tick): echo latency, no mouse, cursor artifacts — a worse terminal.
- *Owning a PTY + vt emulation in bubbletea*: herdr's approach, and the
  ground rule exists precisely to avoid it.

Consequences / details:
- Workspace-scoped options only (`mouse on`, `status off`, pane titles);
  set with target `=name:` — **bare `=name` is rejected by set-option**,
  same gotcha as send-keys. Agent sessions get `status off` at spawn and
  (idempotently) at retarget, so old fleets render clean too.
- `q` kills the workspace session (fleet survives); `d` sends clients
  home (`switch-client -l`, detach fallback) and leaves it running.
- Stale `muster` sessions (continuum-restored shells) are detected by
  "no pane runs the muster binary" and recreated.
- Mouse: bubbletea `WithMouseCellMotion`; one line per sidebar item keeps
  hit-testing pure arithmetic (no zone lib). Buttons `[+ agent]`
  `[+ project]`; forms (spawn: name/project/branch/command; project:
  path/name) replace the list in the left pane. Spawning with a branch =
  worktree; the projects registry (`projects.json`, `muster project`)
  groups the sidebar and feeds the form's project selector.
- Inner-tmux escape hatches: outer copy-mode sees only the visible inner
  screen; real scrollback is `C-b C-b [`. Documented in `?` help.

## Session 3 — spaces, menus, usage, and pipes-as-product

- **Space = the folder you open.** `muster` resolves cwd → repo root,
  auto-registers it, records it in `<state>/space` (re-pointed per launch
  so re-running from another repo re-aims the UI). No config, no command.
- **No path typing.** Project add = a picker over repos discovered one
  level under `MUSTER_REPO_ROOTS` (sane defaults). Literal paths still
  accepted in the same input.
- **Right-click = tmux display-menu at the pointer.** Zero custom overlay
  code; tmux does rendering, keyboard nav, dismissal. Menu items either
  send ordinary sidebar keys back to the TUI pane (agent actions — the
  click already selected the agent) or hidden F6/F7/F8 keys carrying
  which project was clicked (menuProj). Splits pin EXTRA nested clients
  beside the main pane — the multiplexed "watch several agents" view,
  still zero PTY ownership.
- **Usage via statusLine, not scraping.** The injected --settings now
  registers `muster hook status-line`; Claude Code pushes
  model/context%/rate-limits JSON on every update. Two files per session
  (status.json = hooks, usage.json = statusline) so writers never race.
  Settings regenerate at every launch — upgrades propagate silently.
  (herdr has NO equivalent — verified by source read; its state detection
  is screen pattern-matching only.)
- **Team model.** Role lives in the spec; the mesh briefing (recomputed
  each launch, never stored in Argv — resume stays faithful) tells each
  agent its role, the current roster (names + roles), and the STANDUP
  protocol. Standup deliberately has NO panel: it's a broadcast whose
  replies are normal inbox traffic — the mesh view is the one surface for
  team conversation. Proven live: dave asked alice by name and relayed
  her answer; both delivered formatted standups in seconds.
- **Pipes legibility.** `ppz status` has no --json (research: WIRE.md),
  so the mesh view shows its text verbatim plus who --json (liveness ≠
  agent_state — composed ourselves), reread'd mstrctl traffic, and
  schedule ls. Mesh-off state doubles as onboarding: daemon start and
  `ppz login pipescloud.io` run interactively IN the agent pane — the
  workspace is its own setup terminal. ppzCmd always sets NO_COLOR +
  PPZ_UPDATE_CHECK=0; ppzReady cached 10s (the TUI ticks 2s).

## Session 5 — the competitive sweep (see docs/COMPETITORS.md)

Fourteen competitors' pain points, distilled into five decisions:

- **Stalled is a first-class state.** Hooks say "working" but nothing has
  fired for `stall_after_min` (default 10, env MUSTER_STALL_MIN, 0 off) →
  derived state `stalled` (⌛), ranked between blocked and working in the
  attention sort. Derivation happens at READ time (applyStall, pure) — the
  hook sink stays dumb, nothing new is written. Spinners lie; absent events
  don't. This is the "is my agent actually stuck?" answer every scraping
  tool gets wrong.
- **Recap kills scrollback archaeology.** `muster hook` now appends every
  event to `status/<uuid>.events.jsonl` (trimmed at 128KiB to the last 200)
  alongside the latest-status file. `muster recap <name>` (sidebar `e`,
  right-click "recap") = identity, state+reason, usage, dir/branch,
  worktree diffstat + last commits, the event timeline, recent inbox.
  Re-orientation was the #2 hidden cost of fleets in the research.
- **done + review close the loop.** `muster done <name>` merges the
  worktree branch into the repo's checked-out branch (--no-ff, or --squash)
  and cleans everything up — with guards: uncommitted worktree refuses
  (--force overrides), dirty REPO always refuses, conflicts abort cleanly
  and say "ask the agent to rebase". `muster review <name> [--by r]` sends
  a reviewer-role agent the branch, checkout path, commits, diffstat and
  reply protocol over the mesh — the reviewer reads the real worktree, no
  patch pasting. Review is the true bottleneck of parallel agents; muster
  is the only tool in the space with named agents to hand work to.
- **Worktree setup hook.** `.muster/setup` (or `.muster-setup.sh`) runs IN
  the agent's pane, in the fresh worktree, before the agent starts —
  visible install output, best-effort (failure prints and the agent still
  starts). Composed at spawn only, never stored in Argv, never re-run on
  resume. "Worktrees isolate code, not environments" was the #1 complaint
  about every competitor.
- **Fleet triage scales past one screen.** Header shows per-state counts
  worst-first (✋2 ⌛1 ⚙3). `/` filters by name/role/state/branch/dir
  (live, esc clears), `u` jumps to whoever needs you most (blocked →
  stalled → unread), 1–9 jump by position. Sidebar `i`/`e` open
  display-popups (the 38-col clip is gone); pinned splits are titled with
  the agent's name via `muster wpin` (self-exec keeps UI == CLI).

## Non-goals (MVP)

- No Windows. No zellij backend (interface kept thin enough to add).
- No screen-scraping detection manifests.
- No plugin system, no remote-server mode of our own (ppz
  hosted mesh already covers cross-host messaging; `ssh + tmux attach`
  covers remote attach). (Mouse UI: shipped in session 2 after all —
  workspace mode made it natural.)
- No agent-teams interop yet (post-MVP; noted in PLAN).

## Naming

Binary + repo: `muster`. Sessions `mstr-<name>`, branches `mstr/<name>`,
ppz control handle `mstrctl`, agent handles = agent name.
