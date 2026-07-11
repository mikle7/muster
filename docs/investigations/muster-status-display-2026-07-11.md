# Investigation: muster's status display diverging from ppz truth

Agent: chud (opus). Companion to the ppz-side relay investigation
(mikle7/ppz#1, agent diagnose). greg's addendum split off a **second,
distinct** hypothesis: some "blocked, N unread" readings in `muster ls`
are muster's OWN display being wrong, NOT the ppz relay-forwarding bug.
This doc confirms that split. Two independent muster-side defects, neither
is the relay bug.

## Symptom (greg, 2026-07-11)

`muster ls` reported an agent as `blocked, 12 unread`; the agent checked
directly (`ppz subs ls`, `ppz who`) and was genuinely online/working with
**0** unread. Both halves ("blocked" and "12 unread") were wrong, and they
are wrong for two *separate* reasons.

## Defect 1 — phantom unread (CONFIRMED empirically)

`ppzUnreadCounts()` (`ppz.go:333`) feeds the `muster ls` "✉N" badge. It
runs `ppz ls --json` **under the `muster-ctl` session** and reads each
`<handle>.inbox` row's `unread`. That number is computed daemon-side
against the *requesting* session's cursor — i.e. `muster-ctl`'s cursor,
NOT the agent's own.

muster never advances any `muster-ctl` inbox cursor: the only
cursor-advancing inbox reader, `ppzReadInbox` (`ppz.go:247`), has **zero
callers** — dead code. Everything muster does against inboxes
(`ppzUnreadCounts`→`ppz ls`, `cmds.go:615`→`ppz reread`) is
non-advancing.

So `muster-ctl`'s cursor sits at its origin forever and `unread` collapses
to "total retained in the inbox." Confirmed live (read-only, on the real
daemon):

```
# ppz ls --json  as PPZ_SESSION=muster-ctl
arthur.inbox    total=65  unread=65
mstrctl.inbox   total=47  unread=47   (via subs ls, muster-ctl's own inbox)
```

`unread == total` on every inbox. The badge therefore:
- never drops to 0 while messages are retained (the agent reading its
  *own* inbox advances the *agent's* cursor, not `muster-ctl`'s),
- grows monotonically with traffic,
- is completely decoupled from what the agent has actually read.

"12 unread" was just that inbox's retained count. Not a delivery bug.

### Fix options (NOT applied — needs a semantics call from greg)
The daemon computes unread per requesting-session cursor; muster can't ask
"what's unread from *agent X's* cursor" through `ppz ls`. Options:
1. **Drop the badge's "unread" pretense** — relabel it "retained" / show
   inbox depth, which is what it actually is. Cheapest, honest.
2. **Expose agent-cursor unread in ppz** — a daemon field for "unread from
   the pipe-owner's own cursor" that `ppz ls` can return. Correct but a
   ppz change (coordinate with diagnose/the ppz owner).
3. **Have muster-ctl advance a cursor per inbox** — wrong; it'd always
   read 0 and defeat the point.
Recommend (1) now, (2) if a real per-agent unread signal is wanted later.

## Defect 2 — sticky "blocked" outranks live heartbeat (structural)

`liveState` (`status.go:447`) precedence:
1. no tmux session → `dead`
2. **hook status file, if newer than launch → use it** (`status.go:456`)
3. else ppz heartbeat state → use it (`status.go:461`)
4. else `unknown`

The ppz heartbeat carries a live `agent_state` every ~60s (verified: the
heartbeat payload includes `"agent_state":"idle"` etc). But step 2 returns
*before* step 3 whenever a hook file exists and is newer than launch. A
`blocked` Notification hook is "newer than launch" for the rest of the
session, so it **wins over a 30-second-old heartbeat that says the agent
is working.**

Hooks are best-effort (`MUSTER_NOTIFY`, sink availability). If the event
that would clear "blocked" is missed/dropped, muster shows `blocked`
indefinitely while the heartbeat *knows* the agent is alive and working —
the heartbeat is only ever a fallback, never a corrective.

Not reproduced against greg's specific sighting (would need that agent's
status file TS + heartbeat TS from the moment), but the precedence gap is
plain in the code. This is the likely "blocked-when-actually-working" half.

### Fix direction (NOT applied)
When a heartbeat is fresher than the hook status file, let it correct a
stale terminal-ish state — at minimum, a fresh heartbeat reporting
`working`/`idle` should clear a stale `blocked`. Keep it a pure tweak to
`liveState`/`applyStall` so it stays unit-testable. Touches core sidebar
code — coordinate with wren (mesh-in-sidebar) / otto (sidebar UX) to avoid
collision before applying.

## What this does NOT explain

echo's 3 real messages to greg that never surfaced *to greg-the-agent*
(16:19–16:22, inside greg's daemon swap window). That is delivery/nudge,
which is **ppz-side** — muster doesn't nudge; it relies on ppz's built-in
subs-alert nudge (`spec.go:176`, `cmds.go:159`). That incident stays with
mikle7/ppz#1. My read of the ppz snapshot path (`subsSnapshot` →
`buildFilteredList` → `streamInfoByName`) is that a mid-swap JetStream
failure surfaces as `ENATSUnreachable` (bubbled up), not a silent
under-report — so the "snapshot silently reports 0 unread" lead is weaker
than it looked, pointing back at the nudge/delivery path or coincidental
swap-window correlation rather than the snapshot builder.

## One-line takeaway

greg was right to split the hypotheses. The `muster ls` "blocked, N
unread" readings are two muster-side display defects (phantom-unread cursor
+ sticky-blocked precedence), independent of the ppz relay bug. The genuine
lost-delivery incident (echo→greg) remains ppz-side and open.

## Fix applied (2026-07-11, branch fix/muster-status-display off master)

Both muster-side defects fixed per greg's direction (relabel honestly; let a
fresh heartbeat clear stale terminal state). No ppz change; no new deps.

- **Defect 1 — phantom unread:** `ppzUnreadCounts` → `ppzInboxDepth`, now
  reads `Total` (retained inbox depth), not the meaningless muster-ctl-cursor
  `unread`. Renamed the `lsRow.Unread` field (+`json:"unread"`) → `Inbox`
  (`json:"inbox"`) so the code stops lying; sidebar/detail badge glyph `✉` →
  `▤` and CLI/help copy relabeled "inbox depth". The `→ idle+unread` marker
  (otto's #2) now rides on inbox depth — relabeled `→ idle+mail`, and it fires
  for any idle agent with inbox history, so it's a weak "needs attention" cue
  until real per-agent unread exists (flagged to otto).
- **Defect 2 — sticky blocked:** new pure helper `correctStickyBlock`
  (status.go) lets a `working` heartbeat NEWER than the hook event override a
  stale hook `blocked`. Only `working` (not `idle`, which is ambiguous)
  overrides. Threaded heartbeat `ts` into `ppzHeartbeat`. Unit test
  `TestCorrectStickyBlock` covers the 6 cases (working-newer clears;
  idle/older/no-hb/blocked-hb don't; non-blocked untouched).

go vet + go build + go test + gofmt all clean.
