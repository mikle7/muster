# RESEARCH — findings that drive the design

> Compiled 2026-07-08 from four parallel investigations: ppz codebase, herdr
> codebase, herdr community friction, prior art + tech. Sections marked
> (pending) are still being collected.

## 1. ppz capability map (v0.51.0, built from source at `../ppz/bin/`)

Architecture: CLI ↔ local daemon (unix socket, newline-JSON IPC) ↔ ppz-server
(HTTP /api/v1 + embedded NATS/JetStream). Hosted = pipescloud.io (same binary).

**Integration path for us: shell out to the `ppz` CLI.** There is no
importable Go package (everything is `internal/`). The CLI is the stable
contract. Machine-readable: `ls --json`, `read/reread --json`, `subs * --json`,
`who --json`, `schedule ls --json`, `diagnostics --json`. `send` prints a
text line on stderr only.

**Session identity (critical):** per-session state (current handle, cursors)
is keyed by `PPZ_SESSION` env → else `sid-<getsid>` → tty → "default". Every
agent we manage must get `PPZ_SESSION=<agent-id>` pinned in its env at spawn.

**Primitives we build on:**
- Handles + pipes: `source create H` (auto `inbox`), `pipe create`, globs.
- Messaging: `send TGT PAYLOAD` (64KiB cap, send pointers not diffs),
  `read` (cursor-advancing) vs `reread` (replay), `--request-ack` read
  receipts (auto-emitted by reader's daemon, arrive as `subject=ack:read`,
  `in_reply_to=<id>`). Verified working locally.
- Blocking waits: `subs wait` (on subscription set), `ls --watch [PAT]`,
  `read --tail`. No timeout flag — treat spurious wakes as transient.
- **Scheduling (v0.51, server-side durable):** `ppz send --at T | --every DUR
  | --cron EXPR`, managed via `ppz schedule ls/rm`. Fired BY THE SERVER
  (at-least-once, missfire grace 60s, anchor grid, no catch-up bursts) even
  if laptop/daemon offline → this is the "always on" story. Scheduled msgs
  land in inboxes; agents drain on next `subs wait`. Verified locally.
- PTY binding: `terminal share H [-- CMD]` runs a command in a pty bound to
  handle H — publishes `H.stdout`, subscribes `H.stdin`, heartbeats.
  `terminal watch H` follows live; `ppz command H "text"` types into H's
  stdin remotely (dangerous, consent required). `ppz who --json` = roster of
  heartbeating pty agents (harness, model, owner, staleness).
- `ppz agent create NAME [PROMPT]` = handle + spawn AI harness
  (claude/copilot/codex/agy/pi) in a pty via terminal share.

**Lead/worker protocol taught by the bundled ppz-pipes skill:** lead owns
merge baton; workers PROPOSAL → ACK → IMPLEMENTED @ sha (--request-ack) →
lead merges, broadcasts DONE on main @ sha → workers VERIFIED ✓ in throwaway
worktree. Status verbs: ACK/PROPOSAL/IMPLEMENTED/DONE/VERIFIED/BLOCKED.

**Local dev mesh (running now):** server on :8080 (dev-login, seeded
users foo/bar, orgs alpha/beta), NATS on 127.0.0.1:4222, state under
`muster/.dev/ppz-local/` (nats.env is the trust root — do NOT regenerate),
Postgres db `ppz` on local homebrew postgres@14. Login:
`ppz login http://localhost:8080 -apikey "$(cat .dev/ppz-local/seed/key-alpha.txt)"`.
Smoke-tested: send/read/ack round-trip, schedule create/ls/rm.

## 2. herdr autopsy (v0.7.3, ~180K LOC Rust, AGPL)

**The resume flaw is confirmed and structural.** herdr persists the original
launch argv (`TerminalState.launch_argv`, serialized as
`PaneSnapshot.launch_argv`, `src/persist/snapshot.rs:107`) but the resume
planner `agent_resume::plan()` (`src/agent_resume.rs:115-197`) is a hardcoded
match table that rebuilds argv from scratch: for Claude it emits exactly
`["claude", "--resume", <session-id>]`. `restore.rs:765-774` never passes
`launch_argv` in. All original flags (`--dangerously-skip-permissions`,
`--model`, MCP config, etc.) are dropped by construction. No changelog entry
addresses it. **Our rule: the resume command = original argv + resume flag
appended (dedup any prior resume flag). Never reconstruct.**

**Agent detection** = screen-scraping TOML manifests over the terminal grid
(18 agents, 73 rules, priorities, regions like osc_title/prompt_box_body).
Constant churn in CHANGELOG fixing broken manifests after agent UI updates.
Optional per-agent hook scripts (13 agents) report state over the socket and
outrank scraping. Lesson: hooks/events first, scraping only as fallback.

**Why herdr is 180K LOC:** it owns the whole terminal: vendored Ghostty VT
engine (Zig, requires Zig toolchain), 6K LOC FFI glue, own PTY layer
(patched portable-pty), binary render protocol, Kitty graphics/keyboard,
copy mode, mouse encoding, cross-platform ConPTY. Sitting on tmux deletes
essentially all of it.

**Keyboard:** actually has a tmux-style prefix system (default ctrl+b) and
full TOML keybind config — but the product/onboarding/docs are explicitly
mouse-first, and sidebar/pane management is designed around clicking.

**Socket API:** rich JSON API (~90 methods) incl. `agent.start` (full argv!),
`pane.read` (with detection source), `pane.wait_for_output`, events. Ironic:
the spawn API preserves flags; only restore loses them.

**No scheduling/cron/routines feature exists in herdr.** Open space.

**Session state:** four paths (detach/restart/update±handoff). Server restart
kills processes; layout survives; convo survives only via native agent
resume (which is where flags get lost).

### 1b. ppz harness detection (verified in source + live)

`ppz terminal share` is not a dumb pty relay: `internal/harness/` contains
`identify.go` (which harness is running), `activity.go` (working/idle from
PTY byte causality — output while no recent user input = working; explicitly
"no screen parsing"; timing constants ported from herdr), and
`screen_claude.go` (blocked detection for Claude). Heartbeats carry
`agent_state: ""|idle|working|blocked`, `harness`, `child_pid`, `model`
(from `PPZ_AGENT_MODEL`), and emit EARLY on state transitions (60s ticks
otherwise). Verified live: `ppz who --json` showed a shared pty online with
full heartbeat metadata within seconds.

**Consequence: wrap every managed agent in `ppz terminal share <handle> --
<exact argv>` and status-at-a-glance comes from `ppz who --json` — no
screen-scraping in muster itself.** Claude Code hooks can layer on top for
richer signals (permission prompts, done).

## 3. herdr community friction (GitHub: ~14k stars, 1180+ issues, 318 discussions, 3.5 months old)

**The resume complaint is real, public, and declared intentional.**
Issue #965 ("Session restore relaunches agents without their original
flags, environment, and pane cwd") closed by maintainer: "it does not mean
replaying the original process launch. that boundary is intentional."
Discussion #1080 proposes reading `permissionMode` back from Claude's own
session JSONL; unshipped. #1093 (tmux-resurrect-style command persistence)
auto-closed NOT_PLANNED by triage bot. Affected users are exactly the
"unattended fleet" segment ("agents silently lose autonomy on every server
restart... froze at first approval prompt. Bit me twice in one afternoon").

**tmux-parity friction is chronic:** D#563 copy-mode `/` search — 22
upvotes, zero replies (top open discussion). #599 `bind -r` repeatable keys
(user maintains a fork). #382 join-pane parity (7 votes, no reply). #1009
keyboard nav for agents panel — bot-closed NOT_PLANNED. HN launch thread:
repeated "just use tmux?" pushback; users find leaving their tmux muscle
memory jarring. Third-party tmuxp-style layout tools (herdr-spreader,
herdr-sessionizer) appeared within a week of launch = unmet demand.

**Governance friction:** triage bot auto-closes non-templated issues as
NOT_PLANNED; first PRs gated on maintainer `/approve`; "opinionated" is in
CONTRIBUTING.md; fork ecosystem forming (120fps fork, bind -r fork, tab-bar
fork). D#515 multi-server (16 votes): "this is a big change i want to
tackle myself."

**Unserved demand relevant to us:** D#741 inter-agent communication +
@agent delegation (users hacking it with MCP libs); D#1101 multi-host agent
mesh (unanswered); scheduling absent entirely; D#480 jj/Jujutsu support (21
votes, no reply); #261 worktree layout pushback (default buries worktrees
under a hidden .herdr dir).

**Stability themes (herdr owns the terminal, pays for it):** flicker,
100% CPU spins, 60Hz cap (#1134, 20 votes), per-pane idle CPU on Windows,
input-protocol bugs (doubled Enter under Kitty protocol), SSH mouse-sequence
leaks, wrong agent-state detections (#979 hook report "accepted then
overwritten by screen detection" — authority inversion bug).

## 4. Prior art + tech stack

**Prior art (July 2026):**
- **claude-squad** (Go/bubbletea): tmux session + worktree per agent; stores
  launch profile in config.json; detection = capture-pane content hashing +
  prompt string matching (auto-taps Enter). Most popular; "clunky UI" rep.
- **workmux** (Rust): worktree ↔ tmux WINDOW; **hooks not scraping** — agent
  hooks write status files, rendered as 🤖/💬/✅ badges in tmux window names
  via format variables; `merge` = merge+kill window+rm worktree+rm branch in
  one shot. Best-in-class tmux-native UX; our closest stylistic template.
- **agent-deck** (Go/bubbletea over tmux): SQLite stores the ORIGINAL launch
  command; restore/resume incl. --resume inheritance — proves faithful
  persistence in the wild. Hybrid polling+hooks detection.
- **amux** (single-file Python web control plane over tmux): cron scheduler
  with UI, inter-session channels with @mentions, SQLite task board.
  Closest prior art for scheduling+messaging (but web, not TUI).
- **uzi** (Go CLI): swarm ergonomics (`--agents claude:3`), checkpoint =
  commit+rebase-back. **crystal** (Electron, SDK/stream-json based states,
  now deprecated) and **Conductor** (macOS GUI) show GUI approaches; tmux
  users reject them. **Tmux-Orchestrator**: agents self-schedule via
  detached `sleep && tmux send-keys` — influential, fragile (races into
  busy panes → inject only when idle).
- **Claude Code native agent teams** (experimental env flag): SendMessage
  mailboxes, file-locked shared task list, tmux teammate mode; but teams
  don't survive /resume, one team per session, fixed lead → our tool should
  interop later, and its gaps are our opening.
- **zellij**: richer plugin events but no control mode, no stable CLI pane
  handles, and the audience lives in tmux. Verdict: tmux, unambiguously.

**Tech verdict from the report (matches our lean):**
- Sit ON TOP of tmux; never own PTYs (crystal's deprecation = cautionary
  tale). "Attach" = switch-client, never re-rendering.
- Go: claude-squad/uzi/agent-deck precedent, single static binary,
  bubbletea available later. TS rejected (distribution), Rust fine but no
  benefit for exec-orchestration glue.
- Claude Code detection WITHOUT scraping (2026 surface): hooks —
  PermissionRequest (blocked), Notification with matchers
  permission_prompt/idle_prompt/agent_needs_input/agent_completed, Stop
  (idle/done), StopFailure w/ rate_limit|billing_error matchers (error),
  Pre/PostToolUse (working heartbeat), SessionStart/End. Hook payload JSON
  carries session_id/cwd. Hooks can be type "command" (or "http" — POST to
  localhost). Statusline JSON adds context %/cost telemetry per session.
- Faithful resume pattern endorsed: pre-generate `--session-id <uuid>` at
  spawn; store {uuid, argv, cwd, env}; resume = stored argv + `--resume
  <uuid>` (resume lookup scoped to project dir AND its worktrees);
  `--fork-session` = clone-an-agent.
- Worktrees: support `.worktreeinclude` copy of gitignored files (top
  complaint everywhere is missing .env); branch prefix inventory
  (`mstr/*`); cleanup only when no uncommitted/untracked/unpushed; lock
  while agent live; one-command finish à la workmux merge.
- tmux control mode (`tmux -C`) is reliable (iTerm2 decade) but the
  firehose/octal-escaping is overkill for us — plain tmux commands +
  hook-driven status files suffice for MVP; control mode is a later
  optimization for a live dashboard.

**Product gap (verbatim conclusion):** nothing today combines
tmux-window-per-agent ergonomics + faithful command-exact persistence +
hook-driven 4-state attention model + agent mailbox interop + local cron
scheduling. That's muster.

## Implications for muster (running list)

1. tmux-first kills ~90% of herdr's complexity and inherits users' configs,
   copy-mode, plugins, SSH story for free.
2. Faithful resume is trivially achievable (store argv verbatim; append
   resume flag) — herdr's flaw is a design choice, not a hard problem.
3. Status detection: prefer Claude Code hooks (Stop/Notification/PreToolUse)
   writing to a socket/file or ppz pipe; screen-scrape only as fallback for
   agents without hooks.
4. ppz gives us: cross-repo agent messaging, durable scheduling ("check the
   latest release" style recurring tasks), heartbeat roster, remote stdin,
   PTY streaming to the mesh — all via CLI shell-outs with PPZ_SESSION pinned.
5. The scheduling + always-on story (hosted pipescloud.io) is a
   differentiator herdr entirely lacks.

## Session 5 addendum: the 14-competitor pain sweep (2026-07-09)

Full evidence (per-tool findings, ranked cross-tool pain list, ~80 source
URLs): **docs/COMPETITORS.md**. What it changed here:

1. The market's #1 bug class (capture-pane scraping: claude-squad's top
   five issues, uzi's races) re-validates decision 3 above — and exposed
   the next gap: hooks tell you "working", but not "working *silently for
   25 minutes*". Hence the derived `stalled` state (read-time, pure fn).
2. Review — not spawning, not parallelism — is the bottleneck every
   practitioner names (Willison, Omnara HN, vibe-kanban launch). Tools
   with anonymous sessions can't route review anywhere; muster has named
   agents with roles, so `review` = one mesh message. `done` closes the
   other end (uzi's `checkpoint` was the most-praised merge flow in the
   space; vibe-kanban's "worktree not cleaned after merge" its top bug).
3. "Worktrees isolate code, not environments" (claude-squad #260,
   Conductor's whole launch thread, Sculptor's raison d'être) →
   `.muster/setup` in-pane setup hook. In-pane matters: visible install
   output was praised, hidden setup phases (Codex, Jules) hated.
4. Re-orientation cost (TDS, Sculptor praise) → event history + recap.
   The events file also future-proofs comparative review (post-MVP).
5. Anti-goals confirmed by corpses: don't hide the harness (Conductor
   "lost the feel"), don't own auth (OpenCode blocked by Anthropic),
   don't clone-from-GitHub (Conductor top complaint), don't phone-home
   (vibe-kanban telemetry revolt), keep shipping (claude-squad died of
   silence). muster's principles already encode all five — they are the
   moat, not features to trade away.
