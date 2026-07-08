# STATUS — muster

> Ongoing handoff doc. Any agent picking this up: read this file first, then
> `DESIGN.md` (decisions), `PLAN.md` (phases, all ✅), `RESEARCH.md` (why).

**Last updated:** 2026-07-08 (session 1 — MVP complete, all live tests passed)

## What this is

tmux-first agent-army manager on the ppz mesh, built to surpass herdr for
keyboard/tmux users. Single Go binary, zero deps, no daemon. See README.md.

## Where we are: MVP DONE and proven live

Every phase in PLAN.md is checked off, with real end-to-end evidence:

1. **Faithful resume (the herdr #965 killer):** spawned real claude with
   `--dangerously-skip-permissions --model haiku`, killed the tmux session,
   `muster resume` → pane shows "bypass permissions on" AND recalled the
   pre-restart codeword. Exact argv preserved; resume = argv + `--resume
   <pre-pinned uuid>`.
2. **Hooks status:** SessionStart/Stop/Notification hooks (via per-agent
   `--settings`, user settings untouched) drive ⚙/✋/✔ in `muster ls`;
   observed "blocked (Claude is waiting for your input)" live.
3. **Mesh loop:** `muster send worker2 "TASK…"` → ppz's built-in pump
   nudged the idle agent → it ran `ppz subs read` itself, created the
   requested file, replied `ppz send mstrctl 'DONE …'`; ack:read receipts
   arrived with correct in_reply_to.
4. **Scheduling:** `muster cron add worker2 --at +20s "append cron-fired…"`
   → ppz SERVER fired it, pump nudged, agent did it unattended. `--every`
   and `--cron` use the same path.
5. **Worktrees:** `spawn --repo X -b feature-x` → sibling `X__wt/feature-x`
   on branch `mstr/feature-x`, `.worktreeinclude` copied `.env`;
   `kill --rm` refused while dirty, `--force` override works.

## Environment on this machine

- muster repo: `Repos/pipe-terminal/muster` (git, committed). Build:
  `go build -o muster .`
- Local ppz mesh RUNNING: `ppz-server` (pid file? re-check `curl
  localhost:8080/healthz`) with dev-login, NATS 127.0.0.1:4222, Postgres db
  `ppz` on homebrew postgresql@14. State: `muster/.dev/ppz-local/`
  (`nats.env` = trust root, DO NOT regenerate; `seed/key-alpha.txt` = login
  key). Restart recipe in RESEARCH.md §1 + server.log alongside.
  CLI logged in as org `alpha`; `ppz` symlinked at `~/.local/bin/ppz`
  (→ `Repos/pipe-terminal/ppz/bin/ppz`, built from source v0.51).
- Test artifacts (scratch, disposable): tmux server `-L mstrtest`,
  MUSTER_STATE_DIR under the session scratchpad, agents worker1/worker2/
  dummy1. Killed at session end; specs remain in the scratch state dir.

## Known issues / next session TODO

- **menu untested visually** (display-menu needs an attached tmux client;
  errors gracefully otherwise). Try it interactively: bind
  `run-shell 'muster menu'`.
- **tmux-resurrect interplay:** if the user's resurrect/continuum restores
  `mstr-*` sessions as dead shells after reboot, `muster resume` will say
  "already running". Advice: kill stale mstr-* first or exclude them from
  resurrect. Consider auto-detecting a shell-only mstr session.
- **Refusing-agent nag loop:** ppz pump re-nags every 30s while unread
  remains; an agent that refuses to `ppz subs read` loops (seen once with a
  pre-briefing poisoned conversation; fresh spawns fine). Escape hatch:
  `PPZ_SESSION=<handle> ppz subs read` drains manually.
- `muster ls` unread counts read the MESH cursor of mstrctl, not the
  agent's own cursor (close enough for a glance; revisit).
- Post-MVP backlog lives at the bottom of PLAN.md (bubbletea dashboard,
  status-right segment, agent-teams interop, adopt, jj, `muster done`).

## Candidate next steps (in value order)

1. Dogfood on the real tmux server (install muster to ~/.local/bin, bind
   menu key, spawn a real work agent).
2. `muster done <name>`: merge branch → kill → rm worktree (workmux-style).
3. Status-right segment (`muster status --format tmux`) for always-visible
   fleet health.
4. Point the mesh at hosted pipescloud.io instead of the local server for
   true always-on schedules.
