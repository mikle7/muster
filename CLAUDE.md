# muster — agent instructions

**Start by reading `docs/STATUS.md`** — it is the living handoff doc (where
things stand, known issues, next steps). Then as needed: `docs/DESIGN.md`
(decisions + mid-build revisions), `docs/PLAN.md` (what's built, all
live-tested), `docs/RESEARCH.md` (the herdr/ppz/prior-art investigation
behind every decision).

## Ground rules

- **Faithful resume is sacred.** `AgentSpec.Argv` stores the user's command
  VERBATIM; muster-injected flags (`--session-id`, `--settings`,
  `--append-system-prompt` briefing) are composed at launch in
  `spec.go` and NEVER written into Argv. Guarded by `spec_test.go` —
  keep those tests passing.
- **Never own a PTY, never screen-scrape.** tmux is the runtime; status
  comes from Claude hooks (`muster hook` sink) + ppz heartbeats.
- **TUI actions self-exec the CLI** (`runSelf` in tui.go) so UI and CLI
  cannot disagree. Keep new actions on that path.
- Zero runtime deps beyond charmbracelet (bubbletea/bubbles/lipgloss).
  Stdlib first.
- `go vet ./... && go test ./... && gofmt -l .` before committing.

## Test without burning API quota

- Isolated tmux: `export MUSTER_TMUX_ARGS="-L mstrtest -f /dev/null"`,
  scratch state: `export MUSTER_STATE_DIR=/tmp/...`, and
  `export MUSTER_NOTIFY=0` (blocked-agent hooks otherwise pop REAL macOS
  notifications during tests). The user's real tmux auto-restores
  sessions via continuum — never test on their server.
- When driving forms with send-keys, sleep ~0.3s between Tab and text —
  batched input can swallow a keypress. Long values in textinput fields
  scroll horizontally; captures showing a clipped head are NOT a bug.
- Visual TUI testing: with the scratch env set, `./muster ui` bootstraps
  the `muster` workspace session headless (the final self-attach fails
  without a tty — expected). `resize-window -t '=muster:' -x 170 -y 42`,
  drive the SIDEBAR pane with `send-keys`, type into agents via the RIGHT
  pane, read frames with `capture-pane -p` (`-e` for colors). Mouse events:
  send SGR literals, e.g. `send-keys -l "$(printf '\033[<0;5;3M\033[<0;5;3m')"`
  (x=5,y=3, 1-based). Verify retargeting via `list-clients -F
  '#{client_session}'` + `#{pane_title}`. Fake fleet states by editing
  `agents/*.json` and writing `status/<uuid>.json` by hand.
- Real-claude E2E only when needed: use `--model haiku`, tiny prompts.

## tmux gotchas (hard-won)

- Exact-match targets: `has-session -t =name` is fine, but `send-keys`
  and `set-option` reject bare `=name` — use `=name:`.
- `pane-border-status top` costs one row: pane_height = window height − 1.
- Replacing `~/.local/bin/muster` in place gets the binary SIGKILLed on
  macOS (signature cache) — `rm` first, then copy.

## Local ppz mesh (dev)

`ppz-server` (dev-login) on :8080 + NATS 127.0.0.1:4222 + homebrew
Postgres db `ppz`. After a reboot: `.dev/ppz-local/start.sh`. The
`.dev/ppz-local/nats.env` trust root must NEVER be regenerated (it
invalidates every login). ppz CLI is symlinked at `~/.local/bin/ppz`
from `../ppz/bin/ppz` (rebuild there with `make build`).
