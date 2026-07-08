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

## Non-goals (MVP)

- No Windows. No zellij backend (interface kept thin enough to add).
- No screen-scraping detection manifests.
- No plugin system, no mouse UI, no remote-server mode of our own (ppz
  hosted mesh already covers cross-host messaging; `ssh + tmux attach`
  covers remote attach).
- No agent-teams interop yet (post-MVP; noted in PLAN).

## Naming

Binary + repo: `muster`. Sessions `mstr-<name>`, branches `mstr/<name>`,
ppz control handle `mstrctl`, agent handles = agent name.
