# DOGFOOD — the full walkthrough (sessions 5–7 features)

> Setup: `./.dev/dogfood.sh` — rebuilds + installs muster, kills every
> agent and the workspace, wipes `~/.local/share/muster`, re-inits,
> checks the mesh, creates `~/muster-dogfood/{webshop,gamesrv}` and
> spawns three sonnet agents: **peter** (reviewer, webshop), **dave**
> (worktree `qty-fix` on webshop — his spawn runs the `.muster/setup`
> hook), **alice** (gamesrv). webshop's `server.js` has a planted bug
> (cart total ignores quantity) — that's dave's task and peter's review.

Start: `cd ~/muster-dogfood/webshop && muster`

## 1. First glance (spaces, triage header, setup hook)

- Sidebar groups agents under **webshop** (● — it's your space, floated
  first) and **gamesrv**.
- Header shows per-state counts (e.g. `⚙1 ✔2`) — worst state first —
  plus `pipes ok` and, once agents respond, the 5h window.
- Select **dave**, scroll his pane back (`prefix prefix [`): you should
  see the `[muster setup]` lines — env copied, fake install — BEFORE
  claude started. That's the worktree env hook.
- `muster ls` in another terminal: dave's dir is
  `~/muster-dogfood/webshop__wt/qty-fix`, branch `mstr/qty-fix`.

## 2. Give dave the task (real work for later steps)

Click dave's pane (or `enter`) and type:

> Run server_test.js (node server_test.js) — it fails. Fix cartTotal in
> server.js so quantity is respected, verify the test passes, then
> commit on your branch with a clear message. Announce in one line to
> the room when done.

Watch dave go ⚙ working in the sidebar while you continue.

## 3. Triage keys

- `o` — attention sort (flat, blocked→stalled→working→idle).
- `u` — jump to whoever needs you most ("nobody needs you — all quiet"
  when everyone's fine).
- `1`–`9` — positional jumps; `j/k` still move.
- `/` then `game` — fleet filter matches alice by role; header shows
  `/game`; `enter` keeps it, `esc` clears. Try `/review` (matches peter)
  and a garbage query (clean empty-state).

## 4. Recap + inbox popups

- Select dave, press `e`: full-width popup — state, role, usage, dir/⎇,
  the hook-event timeline, worktree diffstat once he's committed, recent
  inbox. This is the "what did I miss" surface.
- `i` on any agent: inbox in a popup (no more 38-col clipping).
- Right-click an agent PANE: menu should include recap / review handoff /
  done (worktree agents only) / inbox popup.

## 5. Rooms (session 6 — shared pipe, one send)

- Click the **webshop** project row → room chat. Type:
  `status check — dave is on the qty bug, peter please be ready to review. CLAIMING: coordination`
- Verify: message renders `you → #webshop`; BOTH dave and peter receive
  it (their panes get the subs-read nudge when idle); replies come back
  in-room (`dave → #webshop`), not as DMs; only the addressed agent does
  the work (etiquette briefing).
- `T` for a standup, then `M` — replies collect in the mesh view.

## 6. Stalled state (session 7)

Real agents rarely stall on demand. To see it: quit the workspace (`q`),
run `MUSTER_STALL_MIN=1 muster`, then give an agent a long quiet task
(e.g. tell alice "think silently for 3 minutes, then say done") — after
~1 min of no hook events she flips to `⌛ stalled (no events for 1m)`
and sorts just under blocked. Restart without the env var afterwards.

## 7. The Peter-reviews-a-PR loop (session 7)

Once dave says he's committed:

- Select dave, press `w` (or right-click → review handoff). Status line:
  "review handed to peter".
- Watch peter's pane: he gets the branch, worktree path, commits and
  diffstat, reads the actual checkout, and replies APPROVE/CHANGES to
  mstrctl. Check `M` (or `i` on peter) for the verdict.
- If CHANGES: relay to dave (`s` on dave), let him fix, `w` again.

## 8. Done — merge back & clean up (session 7)

After APPROVE:

- Select dave, press `D`, type `y` (or right-click → done, confirm).
- Verify: sidebar loses dave; `git -C ~/muster-dogfood/webshop log
  --oneline` shows the merge commit; `webshop__wt/` is gone; branch
  `mstr/qty-fix` deleted; `node server_test.js` prints PASS on main.
- Guards (optional): before pressing y, touch an uncommitted file in the
  worktree — done must refuse with "tell the agent to commit".

## 9. Faithful resume (regression check)

- `K` on alice, `y` — killed, spec kept, row goes ☠.
- `r` — she returns with her EXACT argv (`claude --model sonnet`),
  conversation intact (ask her what she was doing).

## 10. Odds and ends

- Right-click sidebar rows: menu should open AT the pointer (coords now
  computed from pane geometry, not guessed) — try it scrolled, zoomed
  (`z`), and with a `t` terminal strip open.
- Sidebar right-click → split right/down/left: the pinned pane's border
  title must show the AGENT'S NAME, not your hostname.
- `v` / `V` file viewer on dave after he mentions server.js.
- Blocked notification: `MUSTER_SKIP_PERMISSIONS=0 muster spawn probe
  -C ~/muster-dogfood/gamesrv -- claude --model sonnet`, ask it to run a
  command → macOS banner "muster: probe needs you" on the permission
  prompt, row goes ✋, `u` jumps to it. Kill it after.

## Cleanup

`muster kill <name> --rm` per agent (worktrees dirty-guarded), or keep
the fleet — that's the point. `rm -rf ~/muster-dogfood` when done.

If anything misbehaves, `muster recap <name>` + `docs/FOLLOWUP.md` §5
first; file findings back into STATUS.md "Known issues".
