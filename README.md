# muster

**Herd an army of coding agents with the tmux you already love.**

muster runs each agent (Claude Code or anything else) as a plain tmux
session, remembers *exactly* how you launched it, shows who needs attention
at a glance, and wires your agents together — across repos, machines, and
your calendar — over the [ppz](https://github.com/pipescloud/ppz) message
mesh.

```
$ muster spawn api-fix --repo ~/code/api -b fix-auth -- claude --dangerously-skip-permissions
$ muster spawn docs -C ~/code/docs
$ muster ls
NAME     STATE      HARNESS  UNREAD AGE  DIR
api-fix  ⚙ working  claude   0      2m   ~/code/api__wt/fix-auth
docs     ✋ blocked  claude   0      1m   ~/code/docs (permission)
$ muster send api-fix "also bump the changelog when you're done"
$ muster cron add api-fix --every 4h "check CI on your branch, fix if red"
```

## Why not herdr?

- **Faithful resume.** Close everything, reboot, `muster resume --all` —
  every agent comes back with its EXACT original command: every flag
  (`--dangerously-skip-permissions` included), env, cwd, and the same
  conversation (herdr drops your flags on restore *by design* — issue #965).
- **tmux-native.** No new terminal, no mouse-first UI, no re-learned
  keybinds. Agents are regular tmux sessions; your config, copy-mode,
  plugins, and `ssh … tmux attach` all just work. muster never owns a PTY.
- **No screen scraping.** Status comes from Claude Code hooks (installed
  per-agent via `--settings`, your own settings untouched) and ppz
  heartbeats. Not from regexing the screen.
- **Agents talk to each other.** Every agent gets a mesh handle. Agents
  message agents (`ppz send api-fix 'heads up …'`), you message agents
  (`muster send`), and read-receipts flow back automatically.
- **Scheduled prompts, durable, server-side.** `muster cron` schedules
  fire from the ppz server even while your laptop sleeps, then get typed
  into the agent next time it's idle. Point ppz at
  [pipescloud.io](https://pipescloud.io) and the mesh itself is always on.

## Install

```bash
go build -o muster .   # Go 1.22+; zero dependencies
mv muster ~/.local/bin/
muster init            # hooks settings + ppz symlink + tmux snippet
muster doctor          # check tmux/claude/ppz
```

ppz is optional: without it you still get spawn/ls/attach/resume/worktrees.
With it (`ppz login pipescloud.io`, or self-host — see
`docs/…/ppz-local` notes in STATUS.md) you add messaging, scheduling,
heartbeats, and remote terminal watch.

## Commands

| | |
|---|---|
| `spawn <name> [-C dir \| --repo dir -b branch] [--] [cmd…]` | start an agent (worktree per branch, `.worktreeinclude` copied) |
| `ls [--json] [--watch]` | fleet status: ⚙ working ✋ blocked ✔ idle ☠ dead |
| `attach <name>` / `menu` | jump to an agent / tmux popup picker |
| `resume <name> \| --all` | restart dead agents exactly as launched |
| `kill <name> [--rm [--force]]` | stop; optionally remove worktree (dirty-guarded) |
| `send` / `broadcast` / `inbox` | message agents over the mesh |
| `cron add\|ls\|rm` | durable scheduled prompts (`--every 4h`, `--cron "0 9 * * 1"`, `--at +10m`) |
| `init` / `doctor` / `hook` | setup, checks, hook sink (internal) |

Config is env vars: `MUSTER_DEFAULT_CMD` (default spawn command),
`MUSTER_PPZ`, `MUSTER_STATE_DIR`, `MUSTER_TMUX`, `MUSTER_TMUX_ARGS`.

## How it works

- One JSON spec per agent in `~/.local/share/muster/agents/` stores your
  argv verbatim. Launch-time extras (`--session-id`, `--settings`,
  mesh briefing via `--append-system-prompt`) are recomputed every start
  and never written back — that's the faithful-resume guarantee.
- Claude agents get a pre-pinned session UUID, so resume is
  `your exact argv + --resume <uuid>`. Deterministic; no transcript parsing.
- On the mesh, agents run wrapped in `ppz terminal share <handle>`:
  heartbeats (+ working/idle/blocked detection) and remote
  `ppz terminal watch <handle>` come free. When an idle agent has unread
  mail, ppz itself nudges it to `ppz subs read` and act — correct ack and
  cursor semantics because the agent reads its own mail.

## Status / handoff

Living docs in `docs/`: `STATUS.md` (where things stand), `DESIGN.md`
(decisions + revisions), `PLAN.md` (build phases), `RESEARCH.md` (the
ppz/herdr/prior-art investigation this design came from).
