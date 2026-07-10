# FOLLOWUP — needs the user's machine (written 2026-07-10, session 7)

> This session ran in a sandboxed Linux VM with only the muster repo
> mounted: no `ppz` binary, no local mesh (server :8080 / NATS / Postgres
> live on the Mac), no macOS, no real attached tmux client (menus/popups
> are client-screen overlays — invisible headless), no browser for device
> flows. Everything below is what that blocked. A terminal-based agent on
> the Mac (or the user) should work this list top to bottom.

## 1. Live-mesh E2E of the new workflows (candidate step 1, the real half)

Headless dogfood covered states/sort/filter/jumps/done-guards. Still
unproven against a live mesh — start it first (`.dev/ppz-local/start.sh`
after a reboot; NEVER regenerate `.dev/ppz-local/nats.env`):

- **Rooms on the shared pipe (session 6, pulled from master WIP):** spawn
  2–3 real haiku agents in one project, send from the room compose line,
  verify ONE pipe message (not N DMs), agents reply in-room, CLAIMING
  etiquette prevents duplicate fetches. Master's agent live-tested the
  pipe basics; the multi-agent etiquette conversation is untested.
- **The Peter-reviews-a-PR loop (session 7 `review`):** worktree agent
  dave commits work → `muster review dave` (or `w`) → reviewer-role agent
  peter reads the worktree, replies APPROVE/CHANGES to mstrctl → visible
  in M view → `muster done dave` merges. The mesh message is composed and
  sent correctly (unit-level verified); the agent-behavior half needs
  real claude. Use `--model haiku`, tiny diffs.
- **Standup + room chat during a real multi-agent task** (original
  candidate wording) — after the two above.
- `recap`'s inbox section (ppzReread path) — code mirrors the mesh view's
  proven calls, but eyeball it once live.

## 2. Real-mouse dogfood (candidate step 4, the unfinishable-here half)

Menus and popups only render on an attached client; a sandbox cannot see
where they open.

- Sidebar right-click menus now compute client coords from
  `#{pane_left}/#{pane_top}` instead of the hardcoded "+2" (same method
  rmenu uses). Verify placement feels right, including: zoomed pane,
  after a `t` terminal strip changes the layout, scrolled list.
- rmenu (agent-pane right-click) placement: session-4 note said raw
  coords "may need nudging" — judge by feel.
- `e` recap and `i` inbox popups: size (80%×70%) and readability.
- wpin: split right/down/left from the sidebar menu — pane border should
  show the agent's NAME now, not the hostname.

## 3. Hosted mesh (candidate step 5) — pipescloud.io

Operational, not code: `ppz login pipescloud.io` is an interactive device
flow (browser) — run it in the agent pane via the M-view connect screen,
or any terminal. Then verify a `muster cron --at +2m` schedule fires with
the laptop lid closed (the whole point of hosted). Watch for: the local
`.dev/ppz-local` trust root must stay untouched; hosted login is a
separate ppz profile/server, confirm which one `ppz status` points at
before spawning a fleet.

## 4. Build + install on the Mac

```
cd ~/Repos/pipe-terminal/muster
git merge session5-competitive      # after review; commit master's WIP first
go vet ./... && go test ./... && go build -o muster .
rm ~/.local/bin/muster && cp muster ~/.local/bin/   # rm first: macOS signature cache
```
Existing agents pick up new hooks/settings at next resume (settings
regenerate every launch); agents needing the room subscription or
skip-permissions must be kill+resumed (see session 5–6 notes).

## 5. Smaller machine-bound checks

- **macOS notifications**: blocked-transition banner (`notifyBlocked` is
  darwin-only; the sandbox is Linux so it was never exercised). Decide
  whether stalled should also notify (currently doesn't, by design).
- **Stall threshold**: 10m default will false-positive on long single
  tool calls (big builds). Tune `stall_after_min` after real workloads.
- **Unread badge**: known bug (lifetime count, cursor never advances —
  master session-5 notes). Fix wants a cursor-advancing read when a
  room/agent view opens; verify ack/cursor semantics live before wiring.
- **`.git/stale-locks-cleanme/`**: sandbox couldn't unlink git lock
  files, so they're quarantined there. `rm -rf .git/stale-locks-cleanme`
  on the Mac. Harmless meanwhile.
- **Master coordination**: master's agent had uncommitted sessions-5/6
  work that was reviewed and applied to this branch verbatim. If they've
  since committed (same content), the merge resolves clean; if they kept
  editing, prefer their side on master files, this branch's on the new
  files (fleet.go, session5_test.go, ppz_test.go, docs/COMPETITORS.md,
  docs/FOLLOWUP.md).
