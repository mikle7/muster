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
  scratch state: `export MUSTER_STATE_DIR=/tmp/...`. The user's real tmux
  auto-restores sessions via continuum — never test on their server.
- Visual TUI testing: run `./muster` in a detached scratch tmux, drive
  with `tmux send-keys`, read frames with `capture-pane -p` (`-e` for
  colors). Fake fleet states by editing `agents/*.json`
  (harness/session_uuid) and writing `status/<uuid>.json` by hand.
- Real-claude E2E only when needed: use `--model haiku`, tiny prompts.

## Local ppz mesh (dev)

`ppz-server` (dev-login) on :8080 + NATS 127.0.0.1:4222 + homebrew
Postgres db `ppz`. After a reboot: `.dev/ppz-local/start.sh`. The
`.dev/ppz-local/nats.env` trust root must NEVER be regenerated (it
invalidates every login). ppz CLI is symlinked at `~/.local/bin/ppz`
from `../ppz/bin/ppz` (rebuild there with `make build`).
