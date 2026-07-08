# STATUS — muster

> Ongoing handoff doc. Any agent picking this up: read this file first, then
> `DESIGN.md` (decisions), `PLAN.md` (phases), `RESEARCH.md` (why).

**Last updated:** 2026-07-08 (session 2 — workspace mode: live interactive
agent pane, projects, mouse + forms. All E2E-tested headless.)

## Session 2: the workspace (why the TUI changed shape)

User feedback: couldn't type into the agent pane (it was a capture-pane
preview); wanted clickable spawn/add-project; goal restated: **obfuscate
the pipes/ppz complexity, keep the power** — jump in, everything at your
fingertips, no commands to remember.

What shipped (see DESIGN.md "Workspace mode" for rationale + rejected
alternatives):

- `muster` opens a dedicated `muster` tmux session: left = sidebar TUI
  (38 cols), right = **the selected agent's real terminal** (nested
  `TMUX= tmux attach` on the same server, retargeted via `switch-client
  -c <pane_tty>` as you move). Typing there is typing into claude. No PTY
  ownership — ground rule intact.
- Sidebar groups agents by **project** (`projects.json`, `muster project
  add|ls|rm`, auto-registered on `spawn --repo`). One line per agent
  (glyph/name/unread/age), detail block below (dir, ⎇ branch, $ cmd,
  block-reason), buttons `[+ agent] [+ project]`.
- Mouse everywhere (workspace session has `mouse on`): click row =
  select+retarget, click right pane = focus it and type, wheel scrolls,
  buttons/forms clickable. Keyboard parity: `a`/`S` spawn form, `P`
  project form, `enter` = type into agent (or resume if dead), `d` leave
  workspace running, `q` quit it. Fleet always survives.
- Spawn form: name / project selector (←/→) / branch / command. Branch ⇒
  git worktree via existing `--repo -b` path — worktrees stay first-class.
- Dead agent selected ⇒ right pane shows a resume-hint placeholder;
  `enter`/`r` resumes (exact argv) and the pane re-attaches itself.

E2E evidence (headless, scratch server `-L mstrtest` + scratch state dir):
typed into the right pane and the text executed in the real `mstr-worker1`
session; `j` and a synthetic SGR mouse click both retargeted the nested
client (verified via `list-clients` session + pane_title); spawn form
created `runner` with worktree `repoB__wt/feat-1` branch `mstr/feat-1`,
grouped under its project and auto-selected; `[+ project]` button click →
form → `projects.json` + new sidebar group; dead placeholder + enter-resume
verified; layout correct at 170x42 and 80x24; `q` killed only the
workspace. `go vet` + unit tests green.

Gotchas fixed this session (also in CLAUDE.md/memory):
- `set-option -t =name` fails, needs `=name:` (same as send-keys). Was
  silently skipping `status off` / `mouse on`.
- Replacing `~/.local/bin/muster` in place ⇒ macOS SIGKILLs the binary
  (signature cache). `rm` first, then copy.
- tmux `pane-border-status top` eats a row: pane_height = window height−1.

## What this is

tmux-first agent-army manager on the ppz mesh, built to surpass herdr for
keyboard/tmux users. Single Go binary, zero deps beyond charmbracelet, no
daemon. See README.md.

## Where we are

MVP (session 1) proven live: faithful resume (exact argv + pre-pinned
`--resume` uuid), hooks status (⚙/✋/✔/☠, no scraping), mesh send/reply
via ppz's own nudge pump, server-side cron, guarded worktrees. Session 2
added the workspace UI above. **New binary installed to ~/.local/bin/muster
(doctor: all ok, sees the user's 2 live agents).**

## Environment on this machine

- muster repo: `Repos/pipe-terminal/muster`. Build: `go build -o muster .`
- Local ppz mesh RUNNING: `ppz-server` dev-login :8080, NATS 127.0.0.1:4222,
  Postgres db `ppz` (homebrew postgresql@14). State: `muster/.dev/ppz-local/`
  (`nats.env` = trust root, NEVER regenerate; `seed/key-alpha.txt` = login
  key). Restart after reboot: `.dev/ppz-local/start.sh`. `ppz` symlinked at
  `~/.local/bin/ppz` (→ `Repos/pipe-terminal/ppz/bin/ppz`).
- **User's live fleet**: `pixel` + `tester` in the real state dir
  (`~/.local/share/muster/`). Their sessions predate `status off`-at-spawn;
  retarget now silences them idempotently. Don't clobber — test with
  MUSTER_STATE_DIR + `-L mstrtest` (see CLAUDE.md).

## Known issues / next session TODO

- **Workspace untested with a real attached human client** (all E2E was
  headless send-keys/capture-pane): mouse-focus of the right pane, drag,
  paste, and the `switch-client -l` path of `d` need a human eyeball.
- Sidebar scrolls but has no scrollbar/indicator when items overflow.
- Forms: no ppz on/off toggle (always mesh when available), command field
  splits on spaces (no shell quoting) — fine for `claude --flags`, not for
  quoted args. `muster spawn` CLI handles those cases.
- Inbox/schedules/help render clipped in the 38-col sidebar; a tmux
  display-popup would read better (untestable headless — deferred).
- tmux-resurrect interplay: stale `mstr-*` shells after reboot still make
  `resume` say "already running" (pre-existing; auto-detect someday).
- Refusing-agent nag loop + `ls` unread-cursor caveats: unchanged from
  session 1 (see git history of this file for detail).

## Candidate next steps (in value order)

1. Human dogfood pass on the workspace (real fleet, real mouse, real
   claude sessions) — then fix what feels off.
2. `muster done <name>`: merge branch → kill → rm worktree (workmux-style)
   — natural button next to `[+ agent]` once it exists.
3. Status-right segment (`muster status --format tmux`) for fleet health
   outside the workspace.
4. Point the mesh at hosted pipescloud.io for true always-on schedules.
