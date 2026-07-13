# muster

**A tmux-native manager for a fleet of coding agents.**

muster runs each agent (Claude Code, or anything else you can launch from a
shell) as a plain tmux session, remembers exactly how you launched it, shows
who needs your attention at a glance, and — optionally — wires your agents
together across repos and machines over the [ppz](https://github.com/pipescloud/ppz)
message mesh.

```
$ muster spawn api-fix --repo ~/code/api -b fix-auth -- claude --dangerously-skip-permissions
$ muster spawn docs -C ~/code/docs --role "keeps the docs honest"
$ muster ls
NAME     STATE      HARNESS  INBOX  AGE  DIR
api-fix  ⚙ working  claude   0      2m   ~/code/api__wt/fix-auth
docs     ✋ blocked  claude   0      1m   ~/code/docs
$ muster send api-fix "also bump the changelog when you're done"
$ muster cron add api-fix --every 4h "check CI on your branch, fix if red"
```

## What it does

- **Real tmux sessions, not a wrapper.** Every agent is a normal tmux
  session — your config, copy-mode, plugins, and `ssh … tmux attach` all
  work on it unmodified. muster never owns a PTY and never renders
  terminal content itself.
- **Faithful resume.** Kill everything, reboot, `muster resume --all` —
  every agent comes back with its exact original command: every flag,
  every env var, the same working directory, and (for Claude) the same
  conversation. What you typed is stored verbatim and never silently
  altered.
- **Status from real signals, not screen-scraping.** State comes from
  Claude Code hooks (installed per-agent, your own settings untouched) and
  ppz heartbeats — so muster can tell you an agent is **stalled** (says
  it's working, but nothing has happened in 10+ minutes), which a spinner
  never can.
- **Worktrees, done right.** `spawn --repo x -b branch` creates a sibling
  worktree, copies your gitignored `.worktreeinclude` files (`.env`, etc.),
  and runs `.muster/setup` in the agent's own pane before it starts.
  `muster done <name>` merges the branch back, kills the session, and
  cleans everything up — guarded against dirty worktrees and merge
  conflicts.
- **Review as a first-class step.** `muster review <name>` hands a
  branch, its diffstat, and the reply protocol to a reviewer-role agent
  over the mesh — no patch-pasting, the reviewer reads the real worktree.
- **Context management.** `muster refresh <name>` flushes the agent's
  handoff notes, runs `/clear`, and re-injects them — a scripted way to
  reset context without losing continuity. It can also fire automatically
  once an agent crosses a context-usage threshold while idle.
- **A team, not a pile of terminals.** Give each agent a name and a role;
  every agent's briefing includes its own role and the current roster, so
  agents can message each other by name (`ppz send alice '…'`), and
  `muster standup` asks the whole fleet for a status report at once.
- **Scheduled prompts that survive your laptop sleeping.** `muster cron`
  schedules fire server-side and get typed into the agent the next time
  it's idle.
- **Cross-machine by default (with ppz).** Agents running on other
  machines sharing your mesh show up in the same fleet view, marked
  remote, with the same send/attach/watch actions.

ppz is entirely optional: without it you still get spawn/ls/attach/resume/
worktrees. With it you add messaging, scheduling, heartbeats, standups,
rooms, and remote attach.

## Working across multiple machines

Each agent is a tmux session on the machine that spawned it — muster
doesn't move it anywhere. What ppz adds is a shared mesh: run muster on
your desktop, your laptop, and a always-on server, all pointed at the
same account, and every agent on every one of them shows up in the same
fleet view (marked `·ext` for the ones not local to the machine you're
currently on).

Select a remote agent and you get a **real, bidirectional attach** — the
exact same terminal, not a preview: your keystrokes, resizes, and Ctrl-C
go to the actual remote pane, and its output streams back live. Walk
away from your desk, open muster on your laptop, select the same agent
by name, and keep typing exactly where you left off. Sending it a
message, watching it, or attaching to it works from any machine on the
mesh; actions that require the local filesystem (`resume`, `kill --rm`,
recap's diffstat) still need to run on the machine that actually spawned
it and show a plain "local-only" message elsewhere.

This makes a spare always-on machine (or a small cloud box) a natural
place to keep long-running agents — spawn them there once, then check
in and drive them from whichever laptop you're on that day.

## Install

muster is a single Go binary with no runtime dependencies beyond the
[charmbracelet](https://github.com/charmbracelet) TUI libraries.

```bash
git clone https://github.com/mikle7/muster
cd muster
go build -o muster .        # Go 1.25+
mv muster ~/.local/bin/
```

## First-time setup

```bash
muster init      # writes the Claude hooks settings file + a tmux snippet
                  # + a default config.json, and symlinks ppz onto PATH if present
muster doctor     # checks tmux, claude, and the ppz mesh
```

`muster init` never touches your own tmux or Claude settings — the hooks
file is passed per-agent via `--settings` at launch time only.

If you want messaging, scheduling, and cross-machine agents, set up ppz
too (skip this if you only want local spawn/ls/attach/resume):

```bash
ppz login pipescloud.io      # hosted mesh, or self-host — see the ppz repo
```

Once ppz is logged in, every agent muster spawns joins the mesh
automatically — nothing else to configure.

Then just run `muster` from inside any repo:

```bash
cd ~/code/api
muster
```

The folder you're in becomes your **space** — auto-registered as a
project and preselected in the spawn form. That's the whole setup.

## Two ways to run your fleet

### Let an agent build it for you

muster is just a CLI, so an agent with shell access can drive it exactly
like a person would. The fastest way to stand up a team is to describe
the team you want to an agent and let it run the `spawn`/`project`/`--role`
commands itself:

```
You: Using muster, set up a team for this repo: "dave" working on a
worktree branch fixing the auth bug, "peter" whose role is reviewing
every PR, and "alice" for general gamedev work in ~/code/gamesrv. Give
each of them a role, then confirm the fleet with `muster ls`.
```

Point the agent at this README (or `muster help`) and it has everything
it needs: the command surface is small and self-describing, and every
action available in the UI has a CLI equivalent (`runSelf` in the code —
the UI never does anything the CLI can't). This is the normal way to
provision a fleet once you're past the first agent or two.

### Driving it by hand

If you'd rather spawn and manage agents yourself, everything above is
also a keystroke away in the UI, or a direct command:

```bash
muster spawn dave --repo ~/code/api -b auth-fix --role "fixes the auth bug"
muster spawn peter -C ~/code/api --role "reviews every PR"
muster spawn alice -C ~/code/gamesrv --role "general gamedev"
muster ls
```

## The UI

Just run `muster`. It opens the **workspace**: a dedicated tmux session
with a sidebar on the left and the *selected agent's real terminal* on the
right — not a preview, not a re-implementation. Click it (or press
`enter`) and type into Claude exactly as if you'd attached yourself.
Keyboard and mouse both work.

```
 muster 4 agents · mesh ok         │ ╭ api-fix ────────────────────────────────
▍api                             + │
 ⚙ api-fix                     2m  │ > also bump the changelog when you're done
 ✔ review                      1m  │
▍docs                            + │ ⏺ Write(src/auth.ts) — patching token…
 ✋ docs                   ✉1  2m  │ ⏺ Bash(npm test) …
────────────────────────────────── │
api-fix · working · claude         │   (the agent's ACTUAL tmux session —
~/code/api__wt/fix-auth            │    type here, scroll here, it's live)
⎇ mstr/fix-auth                    │
$ claude --dangerously-skip-perm…  │
 [+ agent] [+ project]             │
 spawn: worktree ~/code/api__wt…   │
 a spawn · P project · enter type  │
```

More projects come from a repo **picker** (`P` — finds git repos under
`~/Repos`, `~/code`, … — type to filter, click to add; no path typing).
**Right-click** an agent for split right/down/left (pin several live
agents side by side), zoom, message, or kill; right-click a project for
"new agent" / "new worktree agent". Agents show their **model and
context%** in place, and the header carries your account's **5-hour
rate-limit window** — fed by Claude Code's own statusline, not scraping.

Clicking a project opens its **room**: every message any of its agents
sent or received in the last 24 hours, in one chat, with read receipts —
type at the bottom to message the whole team at once. `T` runs a
**standup**: every agent reports task / progress / blockers / next in
seconds. `M` opens the pipes view — mesh status, who's online, recent
team messages, schedules — and doubles as a guided connect screen when
ppz isn't set up yet (`ppz login` runs right in the agent pane).

`d` leaves the workspace running; `q` quits it (agents keep running
either way — the workspace is just a viewer). Full keymap: `?` in the
UI, or `docs/KEYMAP.md`.

## Commands

| | |
|---|---|
| `spawn <name> [--role txt] [-C dir \| --repo dir -b branch] [-e K=V]... [--] [cmd…]` | start an agent (worktree per branch, `.worktreeinclude` copied, `.muster/setup` run in-pane) |
| `q [cmd...]` | quick-spawn: auto-named `chat-XXXX` in the current dir, no project |
| `ls [--json] [--watch]` | fleet status: ⚙ working ⌛ stalled ✋ blocked ✔ idle ☠ dead |
| `recap <name>` | the 10-second catch-up: state, recent events, git, inbox |
| `attach <name>` / `menu` | jump to an agent / tmux popup picker |
| `resume <name> \| --all` | restart dead agents exactly as launched |
| `refresh <name>` | flush handoff notes → `/clear` → re-inject, without losing continuity |
| `done <name> [--squash] [--keep-branch] [--force]` | merge the worktree branch back, kill, clean up — guarded |
| `review <name> [--by reviewer]` | hand the branch to a reviewer-role agent over the mesh |
| `kill <name> [--rm [--force]]` | stop; optionally remove worktree (dirty-guarded) |
| `project add <path> [--name n] \| ls \| rm <name>` | register repos — the UI groups agents by project |
| `project conventions <name> [text \| --clear]` | per-project prompt injected into every agent spawned there |
| `send <name> <text>` / `broadcast <text>` / `inbox <name>` | message agents over the mesh |
| `standup` | every agent reports task/progress/blockers/next |
| `room <project> [--watch]` | the project's shared chat, all agent↔agent/you traffic |
| `cron add <name> (--every 4h \| --cron "0 9 * * 1" \| --at +10m) <prompt>` | durable server-side scheduled prompts |
| `cron ls` / `cron rm <id>` | list / remove schedules |
| `init` / `doctor` / `hook` | setup, environment checks, hook sink (internal) |

Run `muster help` any time for the exact, current usage string.

## Config

Env vars: `MUSTER_DEFAULT_CMD` (default spawn command, e.g. `claude`),
`MUSTER_PPZ` (path to the `ppz` binary if not on PATH), `MUSTER_STATE_DIR`,
`MUSTER_SKIP_PERMISSIONS=0` (re-enable permission prompts — on by
default), `MUSTER_STALL_MIN` (minutes of silence before ⌛, default 10, 0
disables), `MUSTER_REFRESH_PCT` (context% that triggers auto-refresh while
idle, default 75, 0 disables).

Same knobs persist in `<state>/config.json` (`skip_permissions`,
`repo_roots`, `repo_depth`, `stall_after_min`, `refresh_ctx_pct`) —
written with defaults by `muster init`, hand-editable after.

## How it works

- **One JSON spec per agent** (`~/.local/share/muster/agents/<name>.json`)
  stores your argv verbatim. Launch-time extras (`--session-id`,
  `--settings`, the mesh briefing) are recomputed on every start and never
  written back — that's what makes resume faithful.
- **Claude agents get a pre-pinned session UUID**, so resume is just
  `your exact argv + --resume <uuid>`. Deterministic, no transcript
  parsing.
- **No daemon.** State is the tmux server (live processes) + the ppz mesh
  (messaging, schedules, heartbeats) + flat JSON files. The only
  long-running piece is tmux itself.
- **On the mesh**, agents run wrapped in `ppz terminal share <handle>`:
  heartbeats, working/idle/blocked detection, and remote
  `ppz terminal watch <handle>` come for free. When an idle agent has
  unread mail, ppz nudges it to read and act on its own — correct
  read-receipt semantics, since the agent reads its own mail rather than
  a proxy reading it for them.

## Docs

Living design and status docs are in `docs/`: `STATUS.md` (where things
stand), `DESIGN.md` (decisions and revisions), `PLAN.md` (build phases),
`KEYMAP.md` (the full keymap), `RESEARCH.md`/`COMPETITORS.md` (the
prior-art investigation this design came from).
