# STATUS — muster

> Ongoing handoff doc. Any agent picking this up: read this file first, then
> `DESIGN.md` (decisions), `PLAN.md` (phases), `RESEARCH.md` (why).

**Last updated:** 2026-07-20 (origin/master merged into the PR branch —
brings in `muster serve`, the HQ gateway, see below. 2026-07-17: session
13 — the dispatch/ask rethink. Session 12 same branch: task-first
dispatch. Same-day earlier: round 2 — see "Round-2 sidebar audit"; the
one durable decision change there is the working-state spinner overriding
DESIGN.md's "spinners lie" stance, Michael's explicit call.)

## Session 13: the rethink — ask mode DELETED, /clear re-briefs, merged⇒auto-reset

Live-use verdict from Michael on session 12's two new surfaces: the popup
palette risks losing long-typed tasks (worst possible failure), and the
`claude -p` ask lane was unreplyable and styled unlike the rest of the app —
he /clear'd and used herdr instead. Root cause: both were parallel universes
inside an app whose thesis is "real tmux + real claude". Decisions locked
with him (full detail: ../HANDOFF-dispatch-palette.md §Decisions): workflow
is DIRECT-FIRST (talk to the agent; Greg the workspace-level manager only
for cross-project epics — and workers DO keep reporting back to him, that
correction is explicit), ask mode deleted, lifecycle policy over new
surfaces. What shipped this session:

- **Ask mode deleted** (was ask.go): commands ask/answers/answer/ask-run/
  ask-rm, the JSON answer store, ASKS sidebar section, @ask + trailing-`?`
  routing, config ask_model/ask_keep_pane — all gone. `notify()` moved to
  status.go. The niche is covered by /clear-and-type on any seeded agent,
  retask, and warm spares.
- **/clear hook rework** (status.go clearMode/clearHookJSON): a /clear is
  classified by markers — retask mark → NEW-task injection; refresh mark
  (auto-refresh ≥75% ctx) → SAME-task handoff re-injection; NO mark = a
  human typed /clear → fresh start: handoff parked as .prev (kept), primer
  + lessons injected. EVERY mode now re-injects a recomputed
  identityPrompt() (spec.go, shared with spawn): the --append-system-prompt
  briefing only exists on FIRST-launch processes (firstLaunch gate skips it
  on resume), so a /clear on any kill+resumed agent was amnesia before
  this. Roster comes out fresher than the spawn snapshot as a bonus.
- **Merged ⇒ auto-reset** (autoreset.go, TUI tick next to maybeWarmSpares):
  idle ≥15m + branch committed-during-tenure + clean worktree + fully
  merged locally (!branchAhead) → background `retask <name> --spare` →
  fresh seeded context, claimable spare. Full-auto, Michael's explicit
  call — ends the fleet of half-context agents dangling for days. Known
  ceiling (ponytail comment in workShipped): squash-merged GitHub PRs are
  invisible to ancestry — needs a throttled `gh pr view` fallback if that
  flow matters. `retask --spare` flag added.
- Tests: session13_test.go (clearMode classification, manual-clear parking
  + re-brief, every-mode re-brief, workShipped git fixtures).

**Still open (agreed, not built):** palette → persistent tmux window
(`;` = select-window, esc back, draft autosave, ctrl+E → $EDITOR — NO
transient popup composition); grammar diet (keep @target + !model, delete
the /skill token, #project must refuse loudly on no match, never silent
cwd fallback).

## muster serve: the HQ gateway (2026-07-17, merged in from master 2026-07-20)

Branch `claude/muster-hq-mobile-messaging-74rn18`, paired with the
Muster HQ mobile work in `mikle7/muster-voice` (same branch name there).
`muster serve [--addr :7777] [--token t]` (serve.go) is a small HTTP
surface over the exact CLI calls any local client already makes — for
remote clients that cannot spawn processes, i.e. the HQ phone app on the
tailnet. Design rules match the house style: muster verbs SELF-EXEC this
binary (HTTP and CLI cannot disagree — the runSelf philosophy applied to
a server), ppz verbs ride ppzOut with its hard timeout, and responses
are the raw CLI bytes passed through untouched so HQ's existing parsers
work identically over either transport. Endpoints: `/v1/fleet` (self ls
--json), `/v1/reread/<pipe|name.inbox>` (validated against a safe name
regex; limit clamped), `/v1/mesh`, `/v1/pipes`, `/v1/handoff/<name>`
(reads handoffPath), `/v1/recap/<name>`, `/v1/diffs` (the HQ worktree
diff sweep, server side), `/v1/send/agent|pipe/<name>` (POST body =
text), `/v1/pipe/<name>` (create), `/v1/act/review|refresh|done|kill/
<name>` (202, detached — refresh can wait minutes), `/v1/spawn` (202,
JSON body; first task sent after spawn succeeds, the HQ dispatcher moved
server-side), `/v1/ping`. No TLS/accounts by design — the tailnet is the
boundary; optional bearer token (--token / MUSTER_HQ_TOKEN) as a second
factor. serve_test.go covers routing, passthrough, name/limit
validation, act/spawn argv composition (async via a call-recording fake
runner), token auth, and CLI-failure→502. vet/test/gofmt green. Live
E2E: ran the real binary's serve against a fake ppz mesh, and the HQ
app (gateway mode) rendered the fleet/chat over HTTP end-to-end.

## Session 12: task-first dispatch, context packs, the ask lane

Branch `claude/muster-workflow-context-13xlha` (PR #19), plus ppz PR #4.
Design doc: docs/proposals/fast-dispatch-2026-07-16.md (written first,
then built in full the same day on Michael's go). The premise: muster's
unit of interaction was the agent; Michael's unit of thought is the task —
hence the master-agent dispatcher bottleneck, the herdr returns for
fresh-context starts, and the 40-minute live-ops incident. What shipped:

- **`;` dispatch palette** (palette.go, `muster palette` in a
  display-popup): task text first, inline routing tokens (@agent/
  @template/@ask, !model, /skill, #proj), trailing `?` → ask lane,
  header + `- ` bullets fan an epic out with token inheritance. The live
  preview calls the SAME resolveDispatch the executor runs. `[task]`
  button + project right-click "dispatch a task…" reach it by mouse.
- **Deterministic dispatch** (dispatch.go): idle agent → retask (fresh
  seeded context); busy → queue (mesh send, or typed — runs after its
  turn); template pool → warm-spare claim → freshest-idle retask → grow
  under cap → LOUD refusal. Never an LLM, never silent queueing.
- **`muster retask` / `F`** (retask.go): the missing verb between
  refresh (same task) and kill+spawn. Waits idle if needed, rotates the
  handoff to .prev, /clear, injects primer+lessons INSTEAD of handoff
  (retask marker consumed by the hook), optional --model switch, types
  the new task.
- **Context packs** (packs.go): committed `.muster/primer.md` with
  role-focused `## <template>` sections + `muster lesson add` append-only
  per-project caveats. In the spawn system prompt (survives /clear) and
  topped up via SessionStart (lessons grow after spawn).
- **The ask lane** (ask.go): `muster ask --skill live-triage "user 4821
  can't get in?"` → mesh-less `claude -p` one-shot (sonnet default,
  config ask_model) in a visible mstr-ask-<id> pane; answer captured
  from --output-format json, desktop notification, pane auto-reaps
  (errors keep it), ASKS sidebar section, enter/right-pane shows the
  Q&A, `u` counts unread answers as needs-you. No handle, no room, no
  roster: it structurally cannot be pulled into the committee that
  burned 40 minutes.
- **Templates/pools/spares** (template.go): role+model+skills+project+
  cap+warm bundles; `spawn --as backend` auto-numbers; warm templates
  keep one booted+primed spare (TUI tick, in-flight marker); claiming
  types the task instantly and backfills in the background.
- **spec.Model** (spec.go): --model adopted out of Argv like
  --session-id, composed at every launch/resume; `muster model <name>
  [alias]` (live /model + spec) + `m` menu. Faithful-resume tests
  extended, not weakened.
- **SessionStart source=startup → idle** (status.go): a fresh claude at
  its prompt IS idle — the signal spares are claimable on; clear/resume
  keep reporting working (mid-flow). Events pipe unaffected (idle
  publishes nothing).
- **Speed**: launch's ppz block parallelized (room ensure+subscribe ∥
  source-exists); dispatch spawns write pending markers → instant
  `·boot` spinner rows; `deliver` types briefs when the SessionStart
  hook lands (no blind sleeps); optimistic row + immediate post-palette
  refresh.
- **ppz PR #4** (additive): heartbeat `specialty` from
  PPZ_AGENT_SPECIALTY (muster stamps the template), `ppz who
  --specialty/--free` bench filters; muster reads specialty back into
  remote rows' Template.

vet/test/gofmt green both repos; 19 new unit tests (session12_test.go) +
ppz who_specialty_test.go. Headless E2E on a scratch server with a stub
claude: template spawn composed the full seeded briefing (verbatim Argv
untouched), dispatch dry-run fanned an epic correctly, a real ask
round-tripped question→JSON answer→answers list→sidebar ✓ row→right-pane
Q&A, warm spare auto-spawned from the TUI tick with the `·warm` tag,
`muster model` flipped spec+live, spawn form's template selector and the
palette's live preview render correctly.

### Session 12 open items for live dogfood

- The palette blocks the TUI while open (same display-popup pattern as
  recap/inbox — fine there, worth feeling out on a real fleet).
- Real-claude checks: does a warm spare actually report idle via
  SessionStart(startup) hooks (new mapping); retask's /clear + /model
  sequencing against live autocomplete; ask -p skill expansion against
  the real live-triage skills; notification UX on answer.
- Dispatch send-fallback types into a BUSY agent's input box when off-
  mesh (queues until its turn ends) — verify the feel; mesh path nudges
  on idle as before.
- Cross-machine dispatch (ppz --free/--specialty) is plumbing-ready but
  NOT wired into resolveDispatch — local-only routing for now, by
  design; wire it once single-machine dispatch has bedded in.
- `muster template` has no TUI editor (JSON + CLI only) — deliberate;
  revisit if templates churn more than expected.
||||||| d9d4918
**Last updated:** 2026-07-16 (round 2 — see "Round-2 sidebar audit" below
for the full list; the one durable decision change is the working-state
spinner overriding DESIGN.md's "spinners lie" stance, Michael's explicit
call.)

**Previously:** 2026-07-10 (session 10 — remote rows now follow via a
persistent background mesh proxy (issue #16, pause+jump): glancing away
and back is a cheap switch-client, same as local agents, not a fresh
connect + scrollback replay. vet/test/gofmt green; live E2E PASSED —
found and fixed three real bugs, all only visible under live testing
(a false-follow-on-cold-boot safety hole, an over-broad fix for that hole
that then broke deliberate filtering, and a missing remain-on-exit that
silently defeated the crash self-heal). Branch `feat/remote-attach`,
uncommitted here — pushed for review.)

## feat/event-stream-api: generic Muster event API (Phase 1)

Herald, on branch `feat/event-stream-api` (uncommitted here). This is the
"Muster event API" bullet from Phase 1 of the voice-interface design doc
— which lives in `mikle7/muster-voice`'s `DESIGN.md`, NOT in this repo
(greg corrected the original brief). Scope: publish a generic structured
event stream any external client can subscribe to. Zero voice/TTS/STT/
hardware code belongs here — that's muster-voice's job as one client
among many ("everything is a client").

Shipped as a new pipe on the existing ppz mesh transport, not a new
protocol — same idiom as the per-project room pipes (`room.go`), just
one shared global pipe: **`muster-events`**. `events.go` adds `AgentEvent`
(matches DESIGN.md's three shapes: `agent.notification` {priority,
message}, `agent.question` {question}, `agent.progress` {status}) and
wires publishing into the existing hook sink (`cmdHook` in `status.go`),
gated on genuine state TRANSITIONS only (`eventForTransition`) — hook
events fire on every tool call, but a voice client doesn't want a spoken
update per tool call. Mapping: `working` (from a different prior state)
→ `agent.progress{status:"working"}`; `blocked` → `agent.question`
(question = the hook's reason text, or a generic fallback); `idle` →
`agent.notification{message:"task complete"}`; `ended` →
`agent.notification{message:"session ended"}`. `error`/`stalled`/`dead`
are read-time-only derivations (`liveState`) never written by the hook
sink, so they're not in the mapping.

External clients: `ppz subs add muster-events` + `ppz subs read` to
tail live, or `ppz reread muster-events --json --since 1h` to replay/
catch up without moving a cursor — no new SDK, just the `ppz` CLI
they'd use for anything else on the mesh.

vet/test/gofmt green. Live E2E over the real dev mesh (not just unit
tests): simulated a SessionStart→PreToolUse→PermissionRequest→Stop hook
sequence and read back `muster-events` via `ppz reread --json` —
confirmed working→blocked→idle published exactly once each with the
right shape, and the same-state PreToolUse re-report correctly produced
NO extra publish.

Found but NOT fixing here (pre-existing, cross-cutting, out of scope):
`ppzCmd`'s env override only sets `PPZ_SESSION`; it doesn't clear
`PPZ_CURRENT_HANDLE`, which "wins" per `ppz status`'s own warning
("current source is set twice, env takes precedence") when the calling
shell already has it set — e.g. inside an agent's own pane. Effect: a
`muster-events` publish (or a room-pipe send) issued from inside an
agent's pane shows that agent as the envelope `sender` instead of
`mstrctl`. Cosmetic only — the event JSON's own `agent` field (from
`MUSTER_AGENT`, unaffected) is what a subscriber actually keys off of,
and delivery itself is unaffected — but worth a real fix in `ppzCmd`
(clear/override `PPZ_CURRENT_HANDLE` too) since it likely also affects
existing room-message attribution. Flagged to the team, not blocking.

Next: ping echo (muster-voice) with the pipe name + shape now that it's
live; no CLI subcommand added for humans to watch it (`ppz reread
muster-events --json` already does the job — ponytail: existing tool
covers it, skip the wrapper).

### Addendum (2026-07-12 ~12:20): agent.progress tool/target enrichment

Follow-on, not a reopen — chud (muster-voice, DEEP-tier voice router) hit
real silence during a live agent turn and needed `agent.progress` to
carry WHAT the agent is doing, not just that it's `working`, so a voice
client can narrate ("reading auth.ts... running tests...") instead of a
generic filler. Michael flagged the silence as unacceptable; greg
blessed doing this before chud's separate reply-chunk question (that one
stays design-only — see below, resolved as NOT needing any change here).

Corrected chud's original premise first: muster did NOT already log tool
name/target anywhere (`cmdHook`'s payload struct only read
`hook_event_name`/`session_id`/`source`/`message`) — this was new work,
not exposing something that already existed.

Added: `AgentStatus.Tool`/`.Target` (status.go) populated from the raw
Claude Code PreToolUse/PostToolUse hook JSON's `tool_name`/`tool_input`
(previously parsed but discarded); a new `toolTarget()` helper does
best-effort per-tool-type extraction (`file_path` for Read/Edit/Write/
NotebookEdit, `command` for Bash, `pattern` for Grep/Glob, `url` for
WebFetch, `query` for WebSearch, `description` for Task; `""` for
anything unrecognized rather than guessing wrong). `AgentEvent.Tool`/
`.Target` (events.go) carry the same, `omitempty`, `agent.progress` only.

The real design change (not just plumbing): `eventForTransition`'s dedup
gate used to fire ONLY on a state change, so a `working`→`working`
re-report (tool 2, 3, 4... of one streak) was always suppressed —
enrichment alone would have gone stale after the first tool call, the
opposite of what narration needs. Widened the gate: within a `working`
streak, also fires when `Tool` or `Target` changes vs. the previous
write. A PreToolUse/PostToolUse pair for the SAME call still collapses
into one event (tool+target unchanged between them) — no flood, same
spirit as the original transition-only gate, just scoped to "did the
thing worth narrating change" instead of "did the state change."

vet/test/gofmt green; 7 new unit tests (`TestToolTarget` +
`TestEventForTransition*`) cover: each known tool's extraction, an
unrecognized tool, malformed/empty `tool_input`, tool-change-within-a-
streak firing, target-change-with-same-tool firing, same-tool-same-target
suppression (the Pre/Post collapse), and a `working`-with-no-tool-name
re-report (`UserPromptSubmit`/`SessionStart`) staying suppressed.

**Deliberately skipped a live publish-to-the-shared-pipe E2E** this time
(unlike the original Phase 1 ship above) — `muster-events` is the same
real pipe echo's live voice consumer may be actively tailing tonight for
the actual demo; a synthetic test event risked getting spoken aloud
mid-demo for no real benefit over the unit coverage. The changed surface
is small/mechanical (two new struct fields, one new pure-function helper,
one widened boolean gate) and thoroughly unit-tested; recommend the real
validation be a live agent doing a real multi-tool turn once convenient,
not a synthetic probe.

Sample payload, sent to chud as the final field names (ppz id `2b80c5d2`
pre-build, confirmed post-build separately):
```json
{"type":"agent.progress","agent":"planner","status":"working","tool":"Read","target":"auth.ts","ts":"..."}
```

Not fixed here either (chud's #2, reply-chunk streaming): resolved as a
non-issue for this repo — chud confirmed (ppz, 12:18) a Claude turn only
emits its whole text block at turn-end, so the realistic streaming path
is the voice service itself running a headless `claude -p --stream-json`
turn and chunking deltas client-side, same mechanism LIGHT already uses.
No reply-chunk event type needed on `muster-events`, ever, either way —
confirmed closed, not just deferred.

## Session 10 addendum 2: persistent mesh proxies (issue #16)

Chud shipped `ppz terminal attach --embedded` (Ctrl-\ swallowed, never
forwarded to the remote as SIGQUIT either — closes a latent bug — so a
persistent proxy's connection literally can't be broken by a keystroke).
Design agreed with chud before writing code (this is `mstr-mesh-<name>`,
distinct from `mstr-<name>` real local sessions): lazy creation (only on
first follow, never pre-spawned for the roster), LRU-3 eviction via a
plain `map[string]time.Time`, kill-on-dead when an agent's heartbeat goes
classified-offline, self-heal (respawn in place) only on a genuine crash —
never on Ctrl-\, which `--embedded` makes impossible from inside a proxy.

The proxy model let local and remote rows converge onto the SAME
switch-client code path in `workspacePanes.retarget()` — once a proxy
exists, it's just another tmux session to nest a client into, exactly
like a real local agent's session. This deleted most of the "attach:"-
specific bookkeeping from the previous addendum (the liveness-check block,
the placeholder-swap guard) — no longer needed once `wp.right` is always
just a nested client, never a process directly running `ppz terminal
attach` itself.

**Three real bugs, all found only by testing the live behavior — none
visible from reading the code:**

1. **Accidentally followed a real teammate on cold boot.** The sidebar's
   existing "nothing selected → default to whatever sorts first"
   fallback (in `rebuild()`) landed on `chud` (alphabetically first
   remote row) the instant a fresh scratch instance booted — before any
   deliberate navigation — and the new auto-follow-on-select design
   started creating a REAL background proxy connection to a teammate's
   live session, unprompted. This existed as a latent risk in the
   session-10 (non-proxy) debounce work too, but was harmless there
   (ephemeral — killed the moment selection moved away); with a
   PERSISTENT proxy it could linger unnoticed. Caught only because a
   `list-sessions` happened to show `mstr-mesh-chud` mid-test — killed it
   immediately, then fixed at the root: `rebuild()`'s fallback now
   pre-marks the defaulted row as "already scheduled" so `retarget()`
   never auto-follows it, while an explicit enter/l still can.
2. **That fix was too broad — it broke deliberate filtering.** The
   original fix didn't check whether a filter was active, and typing a
   filter query character-by-character (e.g. `/px-one`) ALSO repeatedly
   hits the exact same "selection vanished → fallback" path as each
   partial match narrows the field — the fix was suppressing every one
   of those too, so a row you'd deliberately filtered down to would just
   never follow. Refined: the fallback only suppresses when `m.filter ==
   ""` (truly passive — nothing narrowing the field explains the pick);
   a non-empty filter is deliberate intent, and its own keystroke churn
   is already handled correctly by the settle timer's own supersession
   (gen token), not this guard.
3. **Self-heal was silently defeated by a missing `remain-on-exit`.**
   `ensureMeshProxy` never set it on the proxy session, so a genuine
   crash of the attach process closed its pane — the session's only
   one — destroying the WHOLE proxy session instead of leaving a frozen,
   detectable pane for `meshProxyPaneDead`/`respawnMeshProxy` to find.
   Confirmed via a direct `kill -9` on the attach process: the session
   vanished from `tmux list-sessions` entirely rather than showing
   `pane_dead=1`. Fixed by setting `remain-on-exit on` right after
   creation, mirroring the right pane's own existing setup in
   `ensureRightPane`.

Also found, NOT caused by this work: `ppzWho()` returns an empty result on
any transient subprocess failure (this Mac's daemon had a persistent
"out of sync with ppz cli" warning through much of this session) — the
FIRST version of the kill-on-dead check treated "agent missing from this
one poll's rows" as equivalent to dead, which would mass-reap every
tracked proxy simultaneously over a single hiccup. Fixed before it caused
real damage in testing (only one proxy was lost to it) by requiring the
row's state be the CLASSIFIED `"dead"` (a real offline determination),
never inferred from absence alone.

### Session 10 addendum 2 E2E evidence

Same safety discipline throughout: real ppz, disposable
`ppz terminal share <throwaway> -- sh` targets created/destroyed per run,
selection always confirmed via the `/` filter before any interaction.
Verified: cold boot with no filter selects a row but does NOT create a
proxy for it (even after 5+ seconds / multiple ticks); filtering to a
specific row DOES follow it once settled; switching between two
ALREADY-existing proxies is a true no-op respawn-wise (`pane_pid`
identical before/after, both for the right pane's nested client and the
proxy's own process) — the one-time respawn on a BRAND NEW proxy's first
view is expected and distinct from this; LRU-3 correctly evicted the
least-recently-viewed proxy on creating a 4th, confirmed both untracked
AND the underlying tmux session actually killed; kill-on-dead relies on
ppz's own online→stale→offline heartbeat classification, which has its
own ~60s+ staleness delay independent of this code — not practically
waitable-out in a test session, but reuses the exact `state=="dead"`
signal already proven correct elsewhere in this session's testing;
self-heal verified via a direct `kill -9` on the proxy's attach process —
session survived (`remain-on-exit`), tick detected `pane_dead=1`,
respawned with a new PID, live shell restored. Local rows re-verified
unaffected: instant embed on select, unchanged. go vet/build/test/gofmt
clean throughout.

## Session 10 addendum: debounced auto-attach (no enter required)

Michael's feedback on the enter/l-to-attach design above: he wants it to
feel automatic on selection, like local rows — but agreed with the reason
it wasn't built that way naively (spawn/kill per row scrolled past). Greg
translated that into a concrete spec: a cancelable ~250ms settle timer
keyed to the selected row, immediate-attach on enter/l as an override.
Chud (who owns `ppz terminal attach`) then refined the interval (200-250ms,
not longer — attach's own connect latency, dial+subscribe+JetStream replay,
stacks on top of the settle before the user sees anything) and the
bubbletea pattern (generation-token, not just name-matching, since a
tea.Cmd can't be canceled once scheduled — a stale one just gets dropped on
arrival by comparing its token against the model's current one).

Shipped:

- **`selGen`/`lastSelForAttach` on tuiModel**, `attachSettleMsg`/
  `attachSettleCmd` (250ms `tea.Tick`, `const attachSettleDelay`).
  `retarget()` (both the tuiModel wrapper and `workspacePanes`) now returns
  `tea.Cmd`, threaded through all ~13 call sites (j/k, mouse click,
  right-click select, filter narrowing at every keystroke, jump-to-
  attention, 1-9 jump, room-view-leave, the periodic 2s tick). Scheduling
  is deduped inside `retarget()` against `lastSelForAttach`, so an
  unchanged selection — notably the periodic tick — never reschedules;
  only an actual change in which row is selected does.
- **enter/l/tab is now an override, not the trigger**: skips the wait, and
  no-ops if the debounce already settled (checked via
  `wp.lastTarget != "attach:"+name`) so pressing it on an already-live row
  doesn't force a pointless respawn/reconnect flicker.
- **A real bug found only by testing the interaction, not either piece in
  isolation**: `workspacePanes.retarget()`'s remote-row placeholder swap
  ran unconditionally on every selection change — so scrolling away from
  an attached row and back within the settle window killed and respawned
  the attach anyway (different PID, verified), exactly the flicker chud
  said to avoid. Fixed: that placeholder swap now no-ops whenever
  something's already attached (`lastTarget` starts with `"attach:"`),
  local rows unaffected — leaving `lastTarget` untouched so a later settle
  for the SAME row still recognizes "already attached" and skips too. Only
  the debounce's own settle-fire (not mere selection) now ever kills/
  respawns a live attach.

### Session 10 addendum E2E evidence

Same safety discipline as before: real ppz, disposable
`ppz terminal share <throwaway> -- sh` targets created/destroyed per run,
selection always via the `/` filter narrowed to exactly the safe target(s)
before any interaction. Verified: selecting a remote row auto-attached
within ~150-250ms with zero keypresses; rapid j/k bouncing between two
safe rows (80ms apart, well under the settle) produced no attach at all
until navigation stopped; letting it settle then attached correctly;
bouncing away from an ALREADY-attached row and back within the window left
the exact same OS process running (`pane_pid` identical before/after —
this is what caught the bug above, a title/content check alone wouldn't
have); settling on a genuinely different row correctly killed the old
attach and connected fresh (different content, correct title). Local rows
re-verified unaffected: instant embed on select, no debounce delay,
unchanged by any of the ~13 call-site edits. go vet/build/test/gofmt clean
throughout.

## Session 10: real attach instead of watch-only, for remote rows

Follow-up to session 9 (mesh sidebar) + session 9.5 (project bucketing,
merged): chud shipped `ppz terminal attach` — real bidirectional keystroke
forwarding, resize, Ctrl-C passthrough to the remote process, Ctrl-\
detaches. Greg's ask: wire it into muster instead of the popup-based
`watch` from session 9.

Design (a mid-build revision from chud's own review — greg's literal spec
was "select embeds, same as local rows"; chud flagged that local's
select-embeds is cheap (switch-client onto an already-running session) but
remote has no such cheap path — `ppz terminal attach` is a fresh process
every time, so embedding on mere `j`/`k` scroll would spawn/kill one per
row scrolled past):

- **Select shows a placeholder**, same as before (now describing attach
  instead of watch). **enter/l/tab spawns the actual attach**
  (`workspacePanes.attachRemote`), respawn-pane into the right pane, same
  mechanism as local rows' nested-attach embed — just deferred to explicit
  intent instead of firing on selection.
- **Two bugs found live-testing, both fixed**:
  1. The generic target-selection logic in `retarget()` always derives
     `"remote:"+name` for any live remote row regardless of attach state
     (tmuxSess is always empty for remote rows) — so once attached
     (`lastTarget = "attach:"+name`), the NEXT 2s tick would recompute
     `target = "remote:"+name`, see it mismatch `lastTarget`, and silently
     stomp the live attach back to the placeholder. Every single tick.
     Fixed: a liveness check at the top of `retarget()` now short-circuits
     and returns immediately when still-attached-and-healthy, before the
     generic logic ever runs.
  2. That same liveness check first tried `pane_current_command !=
     "ppz"` to detect a dead attach (Ctrl-\ detach, crash) — wrong signal:
     `pane_current_command` freezes at its last value once a process exits
     (remain-on-exit keeps the pane around, tmux never updates "current"
     command to reflect nothing running), so it kept reading "ppz" long
     after a real detach. Fixed: use `#{pane_dead}` instead, tmux's actual
     purpose-built liveness flag.
- **Discoverability** (the ask was specifically about "stuck, had to
  restart muster" confusion): pane title shows `name (mesh · Ctrl-\
  detach)` while attached — persistently visible via
  `pane-border-status top`, exactly where the user's eyes are when
  they're stuck, not a one-off message they could've missed. Also: a
  one-time sidebar status line on attach, the detail-panel hint line, the
  `?` help screen, and the right-click menu label all updated to match.
- **Self-attach footgun**: found by accident during live testing — mis-
  navigated and ran `ppz terminal attach` against my OWN live session
  (wren), which created a real bidirectional feedback loop (garbled
  render; contributed to needing a kill+resume to recover). Muster's
  detach-recovery caught it correctly once the process exited on its own,
  but the real fix belongs in `attach` itself — flagged to chud, who
  shipped a guard same-day (refuses pre-dial if the target handle equals
  the caller's own `PPZ_SESSION`).

No new unit tests: `workspace.go` has none anywhere in the codebase — it's
tmux-subprocess-heavy and covered by live/headless E2E only, matching the
existing pattern.

### Session 10 E2E evidence

Real ppz binary (not faked), real mesh. Safety note: after the near-miss
above, all interactive keystroke tests below ran ONLY against disposable
`ppz terminal share <throwaway> -- sh` targets I created and destroyed
myself (`PPZ_AGENT_HARNESS=claude` env-forced so they'd surface as sidebar
rows) — never against a real teammate's session, and selection was always
confirmed via the sidebar's `/` filter (not counted `j` presses) before
any keystroke went out.

Headless scratch tmux + real ppz: selecting a remote row showed the
select-only placeholder (no process spawned); enter embedded a real
`ppz terminal attach` (confirmed via `pane_current_command`); typing into
muster's right pane landed on the actual target's own pane (verified by
capturing both sides — byte-identical); survived 8+ seconds / 4 tick
cycles without the target-selection bug re-triggering (post-fix); a real
Ctrl-\ detached the process, and after the fix above the next tick
correctly swapped to "detached — enter or l re-attaches" instead of
tmux's native "Pane is dead" freeze; re-attaching afterward worked;
switching to a different remote row while attached cleanly killed the old
attach process (`respawn-pane -k`) before showing the new row. Separately
confirmed a local spawned agent's live-attach path (untouched by this
diff) is unaffected: immediate embed on select, unchanged across 5s of
tick cycles, no respawn flicker. go vet/build/test/gofmt clean throughout.

One live incident during testing, not caused by this work: chud/greg
redeployed the ppz daemon mid-session (shipping the self-attach guard
above), which transiently emptied `ppz who` for a few seconds — recognized
via the daemon's version string / token-refresh age jumping, not a code
bug; waited it out and recreated throwaway targets on the far side.

## Session 9: mesh agents in the main sidebar

The user runs muster across 3 machines (this Mac + a Linux desktop + a Linux
server) sharing one ppz-server. Cross-machine agents already worked fine via
ppz directly (who/send/terminal watch), and the M mesh view already listed
everyone — but the main sidebar (`gatherRows` in cmds.go) built its row list
purely from local spec files (`listSpecs()`), so an agent running on a
*different* machine never appeared there.

Shipped:

- **`buildRows`** (cmds.go): `gatherRows` split into itself (I/O: listSpecs +
  ppzWho + ppzUnreadCounts) and a pure `buildRows(specs, hb, unread)` for
  testability. After the usual local rows, `remoteRows` appends a synthetic
  `lsRow` for every `ppzWho()` handle with no matching local spec — same
  "ours" distinction meshBody already computes, narrowed to actual agents
  (`heartbeat.harness != ""`; a bare human ppz login or muster's own
  `mstrctl` control handle isn't an agent and stays off the sidebar). New
  `lsRow.Remote`/`.Host` fields; Tmux/Dir/Branch/Wt stay zero — there's no
  local process or worktree behind these rows. An offline heartbeat maps to
  state `dead` regardless of its last-known `agent_state`.
- **Remote rows are visually marked** (`·ext`, meshBody's existing
  convention) in both the row list and the detail panel, which also swaps
  the (empty) dir/branch/cmd lines for host + a one-line action hint.
- **enter/l/tab** on a remote row pops up `ppz terminal watch <name>`
  (popupCmd, a new arbitrary-command sibling to the existing popupSelf)
  instead of trying to focus a local pane that doesn't exist. Dead-remote
  shows a "nothing to resume from here" status instead of attempting
  `muster resume` (which needs a local spec).
- **Local-only actions guarded** for a selected remote row instead of
  erroring: recap (e), refresh-context (f), review handoff (w), file menu
  (v/V), kill (K), resume (r) all show a plain-English "local-only" status
  message. `t` (terminal here) no longer silently blanks `m.space` when the
  selection is remote. Right-click's agent menu gets a third branch
  (remote / dead / local-live) instead of showing local-only items that
  would just fail.
- **Two bugs beyond the original plan, found while verifying it and fixed**:
  (1) `agentHandle()` (cmds.go) resolved a name to its ppz handle by
  requiring a *local* spec file — so `muster send`/`inbox`/`cron add`
  against a remote-only agent failed "no such agent" even though the row is
  visible and selectable. Now falls back to treating the name as a live
  mesh handle directly when no local spec exists. (2)
  `workspacePanes.retarget` (workspace.go) tried to tmux-attach an *empty*
  session string the instant a remote row was merely selected (not just
  entered) — `j`/`k` onto a remote row broke the right pane before this fix.
  Added a `remote:` target case with a friendly placeholder, mirroring the
  existing `dead:` case.

Tests: `session9_test.go` — `buildRows`/`remoteRows` (remote row shape,
no duplicate for a locally-spawned handle, non-agent/self filtering,
offline→dead, unread passthrough) and `agentHandle`'s mesh fallback via a
faked `ppz` binary (mirrors `TestPpzRunTimesOut`'s pattern).

### Session 9 E2E evidence

Headless local (scratch tmux + fake ppz binary simulating a mixed
local+remote mesh): sidebar showed a dead local agent and one `·ext` remote
row correctly bucketed and unduplicated; selecting the remote row rendered
the mesh-only detail panel and placeholder right pane (no broken tmux
attach); `K`/`e` on it produced the local-only status messages instead of
erroring; the pre-existing local kill-confirm prompt was unaffected.

**Live cross-machine** (real shared mesh, real agents): built + rsynced the
branch to an isolated scratch dir on the Linux desktop (mikle-linux.local)
over SSH, ran it under an isolated tmux server (`-L mstrtest`) + scratch
state dir so the user's real, attached `muster` session there was never
touched. `muster ls --json` and the live TUI both showed pixel-studios'
real ivy/jack/quinn/remy (running on the Mac) as `remote:true` rows with
correct host/harness/state/unread — go vet/build clean on Linux too. `s`
(send) on a remote row delivered a real mesh message to ivy end-to-end,
confirming the `agentHandle` fallback (ivy sent a heads-up afterward: it
was a labelled, harmless test message). `ppz terminal watch ivy` verified
standalone to stream her real live terminal. Scratch tmux server + dir
torn down after; the user's real `muster` session and `~/repos/muster`
checkout on that machine were never touched.

## Session 8: what's in that terminal + clear-not-compact

Two user asks: (1) herdr showed the branch/worktree per agent — muster's
sidebar only had names; (2) the user's context workflow (rolling handoff
md + /clear at high ctx%, never /compact) should be first-class and
automated. Verified against current Claude Code docs
(code.claude.com/docs/en/sessions, /hooks) before building:
**/clear ROTATES the session id**; SessionStart fires with
`source:"clear"` + the new id; SessionStart hook stdout
(hookSpecificOutput.additionalContext, 10k cap) is injected into the
fresh context; `--append-system-prompt` is a process flag so the
identity briefing survives /clear on its own. Note: docs don't
explicitly confirm the flag-persistence point — confirm during live
dogfood (agent should still know its name/role after a manual /clear).

Shipped (all tested, session8_test.go):

- **⎇ branch sub-line in the sidebar**: every agent row grows a dim
  second line with the LIVE checked-out branch (`liveBranch`: pure file
  reads of .git/HEAD, walks up from subdirs, resolves worktree gitdir
  files, detached → short sha — no git subprocess on the 2s tick).
  Worktree agents marked `·wt`. Implemented as its own sideItem kind
  ("sub") so hit-testing stays 1-line-per-item; clicking it selects its
  agent; j/k/1-9/u skip it. **Guard fix**: `done`/`D` and the menu item
  now key off the new lsRow.Wt (muster-created worktree), not
  Branch != "" — live Branch is set for ANY git checkout now.
- **Session-id adoption** (status.go): the hook sink, on SessionStart,
  re-points the spec at a rotated session id (MUSTER_AGENT names the
  agent; Argv untouched — faithful-resume tests still pin that). Events
  history migrates to the new uuid; stale status/usage dropped. Before
  this, a manual /clear silently froze status/ctx% and left resume
  targeting the pre-clear snapshot.
- **Handoff re-injection**: briefing now instructs agents to maintain
  `<state>/handoff/<name>.md` continuously (keyed by NAME — survives
  rotation). On SessionStart source=clear the sink emits
  additionalContext with the handoff tail (9k cap, latest wins) +
  orientation preamble. Missing file → guidance to reconstruct from git.
- **`muster refresh <name>`** (refresh.go; sidebar `f`, right-click
  "refresh context…"): flush prompt → wait idle (MUSTER_REFRESH_WAIT_S,
  default 300s; aborts safely pre-/clear on timeout) → /clear → wait for
  id rotation (20s, warns if none) → "continue from handoff" kick.
  In-flight marker `<state>/refresh/<name>.json` (10m TTL) prevents
  double-fires.
- **Auto-refresh**: TUI tick fires the cycle when an agent is
  claude+idle+ctx% ≥ threshold (`refresh_ctx_pct` config /
  MUSTER_REFRESH_PCT, default 75, 0 off). Idle-only = never yanks a
  working agent; it catches them next time they surface. Background
  self-exec so the tick never blocks.

### Session 8 E2E evidence (headless, Linux sandbox, scratch server)

`ls --json` showed the live branch and tracked an agent-side
`git checkout -b` mid-session; TUI capture showed `⎇ hotfix/live-switch`
under alice and `⎇ mstr/fix2 ·wt` under a worktree agent. Hook-driven:
PreToolUse under old uuid → SessionStart source=clear with rotated id →
spec re-pointed, argv untouched, events migrated
(PreToolUse+SessionStart under new uuid), old status/usage gone,
additionalContext JSON contained the handoff note; source=startup
emitted nothing. Full `muster refresh` cycle with faked status
transitions: flush prompt + /clear + kick all landed in the pane in
order, exit 0, marker cleaned. go vet/test/gofmt clean (14 new tests).

### Session 8 open questions for live dogfood

- Confirm briefing survival after manual /clear (docs silent on the
  flag-persistence point — ask alice who she is post-clear).
- `/clear` is typed into claude's input via send-keys; if a fuzzy
  autocomplete ever ranks another slash command above the exact match,
  the Enter would fire the wrong one. Watch the first live run.
- Auto-refresh threshold 75% is a first guess (statusline ctx% arrives
  only for claude agents). Tune like stall_after_min.
- ppz-side: consider a handoff-flush nudge via mesh instead of
  send-keys if typed prompts ever collide with a user mid-composition
  (agent input box is shared with the user by design).

## Session 7: what the rest of the market taught us

Research first: 4 parallel sweeps over ~80 primary sources across 14
competitors (herdr, claude-squad, uzi, Tmux-Orchestrator, agent-farm,
vibe-kanban, Crystal, Conductor, Sculptor, Omnara, OpenCode, Terragon,
Cursor BG agents, Codex cloud, Jules, container-use). Full evidence with
URLs: **docs/COMPETITORS.md**. Headlines: scraping-based status is every
tmux tool's top bug source (we're immune, keep it that way); "which agent
needs me" triage is the product; worktrees isolate code not environments;
review/merge is the real bottleneck; wrappers that hide the harness die.

(Section written as "session 5" before master's agent claimed 5–6;
renumbered to 7. The branch name stays session5-competitive.)

Shipped (developed on worktree .wt/session5, branch session5-competitive,
now merged here; all guarded by tests):

- **Stalled state (⌛)**: hooks say working but no event for
  `stall_after_min` (default 10; MUSTER_STALL_MIN; 0 off) → derived
  `stalled` at read time (applyStall, pure fn, tested). Ranks between
  blocked and working in attention sort; red in the sidebar; counted in
  the new header triage counts ("✋2 ⌛1 ⚙3").
- **Event history + recap**: hook sink appends every event to
  `status/<uuid>.events.jsonl` (128KiB trim → last 200). `muster recap
  <name>` = state+reason, role, usage, dir/⎇, cmd, worktree
  diffstat/uncommitted/last-commits, event timeline, recent inbox.
  Sidebar `e` and right-click "recap" open it in a display-popup.
- **`muster done <name>` [--squash|--keep-branch|--force]** (sidebar `D`,
  right-click on worktree agents): merge into the repo's checked-out
  branch → kill → remove worktree → delete branch → drop spec. Refuses on
  uncommitted worktree (msg suggests `muster send <name> 'commit…'`),
  refuses on dirty repo, aborts conflicts cleanly ("ask the agent to
  rebase"). Merge helpers unit-tested against real temp repos (happy,
  dirty-refusal, conflict-abort).
- **`muster review <name> [--by r]`** (sidebar `w`, right-click): mesh
  message to a reviewer agent (default: first live agent with "review" in
  its role) with branch, checkout path, commits, diffstat vs the repo's
  HEAD branch, and the reply protocol (APPROVE/CHANGES to mstrctl).
- **Worktree setup hook**: `.muster/setup` or `.muster-setup.sh` at repo
  root runs IN the pane, in the fresh worktree, BEFORE the agent (visible;
  best-effort; spawn-only; never in Argv — TestSetupNeverInArgv pins it).
- **Fleet triage**: `/` live filter (name/role/state/branch/dir), `u`
  jump-to-attention (blocked → stalled → unread), `1`–`9` positional
  jumps, header per-state counts. Sidebar `i` inbox + `e` recap use
  display-popups (kills the 38-col clip known-issue); pinned splits now
  titled with the agent name (`muster wpin`, self-exec'd from menus).

### Session 7 candidate-next-steps pass (2026-07-10)

The user asked for all of session 4's "candidate next steps". Steps 2–3
(done/review, filter/jump keys) shipped above. The rest:

- **Step 1 (dogfood), headless half DONE**: 3-agent fleet with roles
  across 2 projects, faked claude statuses — header `✋1 ⌛1 ⚙1`, `o`
  sort (blocked→stalled→working), `u` jump to dave with promoted
  permission_prompt reason, `3` jump to stalled peter, `/game` filter to
  dave by role, `D` on a non-worktree agent shows the friendly guard,
  TUI survives `e`/popup keys with no client attached. The live-mesh
  half (rooms etiquette, Peter-reviews-a-PR, real standup) CANNOT run
  here — no ppz binary/mesh in the sandbox → **docs/FOLLOWUP.md**.
- **Step 4 (menu tuning), code half DONE**: sidebar right-click menus now
  compute client coords from `#{pane_left}/#{pane_top}` (verified =0/1 in
  the workspace layout, matching the old +2 guess) instead of hardcoding;
  robust under zoom/splits. The by-feel placement check needs a real
  client → FOLLOWUP.md. Pinned-pane titles were fixed above (wpin).
- **Step 5 (pipescloud.io)**: purely operational (interactive browser
  device flow on the user's machine) — nothing to code; steps written in
  FOLLOWUP.md §3.

### Session 7 E2E evidence (headless, Linux sandbox, scratch server)

Spawned a worktree agent from a repo with `.muster/setup` → marker file
present in the worktree before the agent ran; `done` refused while
`b.txt` was uncommitted (guard msg), then merged `mstr/feat` into main
(--no-ff commit visible in log), removed worktree + branch + spec. Faked
a 25m-stale working status → `ls` showed `⌛ stalled (no events for
25m)`; `recap` rendered the event timeline (working→blocked→working);
TUI header showed `⌛1`, `/xyz` filter narrowed to the
nothing-matches empty state with the filter echoed in the header and the
`/ ›` input at the bottom. go vet + go test (9 new tests) + gofmt clean.
NOTE: sandbox tmux servers die between test shells — kill-path prints
weren't exercised; cmdDone reuses tmuxKillSession (session-1 tested).

### Review checklist for the user (this uncommitted merge)

1. `git diff --cached master` (the whole merge is staged, nothing committed).
2. Run `.dev/dogfood.sh`, then follow docs/DOGFOOD.md — it exercises every
   session-5/6/7 feature on a fresh fleet.
3. If good: `git commit` (the prepared merge message is in .git/MERGE_MSG).

## Session 6: rooms become a real shared channel (uncollared ppz pipe)

User demoed the token-doubling bug: "message everyone" fanned out N unicast
sends (`sendRoomCmd` loop), so each agent got what looked like a private DM,
couldn't see peers' replies, and independently fetched the same GitHub issue
list. Deep dive into ppz (WIRE.md, CHANGELOG, e2e tests, read.go) found the
proper primitive: **uncollared pipes** (v0.31+) are symmetric many-to-many
channels — per-session cursors (`cursors/<session>.json`), sender-attributed
tabular render (read.go treats uncollared like inbox), `ppz send LEAF`
resolves the uncollared pipe first. The old `broadcast` auto-pipe was removed
in v0.30 — the room.go comment citing it was stale. ppz has NO turn-taking/
locks/dedup; coordination is prompt-level protocol.

Changes (all live-verified: scratch tmux + scratch state + real local mesh,
`room-zztest` pipe, TUI send → agent `subs read` → in-room reply → TUI):

- **`roomPipe(proj)`** (ppz.go): `room-<proj>` squeezed into ppz's segment
  regex (32-char cap, dash rules) + `TestRoomPipe`. `ensureRoomPipe` creates
  it idempotently (E_PIPE_TAKEN ok; E_NAME_TAKEN = handle clash, surfaced).
  `subscribeRoom(session, pipe)` = `ppz subs add` under the agent's session.
- **`sendRoomCmd`** (room.go): ONE send to the room pipe (was N unicasts).
- **`gatherRoom`** unions the room-pipe history (`reread --since 24h`) with
  the existing member-inbox scan, so DMs/standup replies still show. Room
  messages render `sender → #proj`, no ✓✓ (shared-pipe ack semantics are
  per-reader and unverified — check before wiring ticks to rooms).
- **`launch()`** (cmds.go): creates the project's room pipe and subscribes
  the agent's ppz session before the harness starts — so ppz's built-in
  subs-alert nudge delivers room traffic. Agents spawned BEFORE this build
  need kill+resume to join their room (same operational note as session 5's
  skip-permissions).
- **`meshBriefing`** (spec.go): TEAM ROOM paragraph — reply in-room not to
  sender's inbox; addressed-agent-acts-alone; `CLAIMING: <task>` before
  whole-room work (kills the duplicate-fetch behavior); discuss and divide.

## Session 5: room chat becomes interactive, ppz-usage audit

User reported agents still prompting for permission despite the session-4
skip-permissions feature. Root cause: that feature landed at 16:28 today: it
only takes effect at spawn/resume (`composeSpawn`/`composeResume` in
spec.go), and muster never respawns a *live* agent — selecting one just
`switch-client`s to its existing tmux pane (`workspace.go` `retarget`). The
user's alice/george/terry were spawned at 09:15-09:23, hours before that
code existed, and are still running the old argv (verified via `ps`: no
`--dangerously-skip-permissions` on their live `claude` processes). Fix is
operational, not code: `muster kill <name>` then `muster resume <name>` (or
`r` in the sidebar on a killed agent) relaunches with current `injected()`
logic and picks up the flag; conversation is preserved via `--resume
<uuid>`. Not yet applied — killing a live, in-progress agent is the user's
call, left to them.

Also audited every ppz CLI call muster makes (ppz.go, cmds.go, room.go,
tui.go) against ppz's actual source (cmd/ppz, internal/cli, internal/daemon)
— subcommands/flags, JSON shapes, ack:read semantics, the terminal-share
handle-exists branch, `ppz subs read` vs muster's own `read`/`reread`, env
vars. All correct except one real bug, fixed:

- **`ppzReady()` false positive** (ppz.go): checked `strings.Contains(out,
  "logged in")` against `ppz status` text, but the *unauthenticated* state
  literally prints `"not logged in"` — a substring match. Daemon-up-but-
  logged-out was misreported as ready, so a fresh spawn would get wrapped in
  `ppz terminal share` and fail unauthenticated instead of showing the
  guided connect screen. Fixed to match the exact `"daemon: logged in"`
  line.
- Found but not fixed (logged for later, no user-visible urgency): `muster
  ls`'s unread badge is actually a lifetime message count, not real
  unread — mstrctl's ppz session never does a cursor-advancing `read`
  (everything goes through `reread`/`ls`), so the cursor never moves and the
  count never drops. `ppzReadInbox` (the one function that *would* do a
  cursor-advancing read) is dead code, unused since the muster-relay design
  was dropped. Candidate fix: run a cursor-advancing read when a room/agent
  is actually opened, Slack-style.

**Room chat is now a real group chat**, not just a transcript + the T-key
standup broadcast: `muster room <proj> --watch` (room.go) has a compose line
always focused at the bottom — type, hit enter, it fans out to every room
member's ppz handle (no server-side broadcast pipe exists, per
docs/WIRE.md, so this is client-side fan-out like `muster broadcast`, just
project-scoped). Since typing is now live, `q` no longer closes the room —
only esc (clears the draft, or closes if already empty) / ctrl-c do.
Send errors show on their own status line below the input, not appended
inline — an earlier version appended the error to the input line and it
silently corrupted the whole frame (header scrolled off-screen) whenever
the input+error text was long enough to soft-wrap past the pane width,
which desyncs bubbletea's alt-screen row bookkeeping. Verified via headless
tmux: failed send (nonexistent handle) renders a clean one-line error with
header intact; real send to the live `alice` handle landed and rendered as
`you → alice` in the transcript within ~2s. docs/KEYMAP.md's room-chat
section updated. go vet/test/gofmt clean.

## Prior sessions

## Session 4: rooms, right-click everywhere, quality-of-life

User issue list: read receipts; pipes as "rooms"; right-click only worked on
the sidebar and closed on button-release; crowded detail panel; agents should
default to --dangerously-skip-permissions (configurable); clicking a space
should show a slack-like chat of its agents; keyboard map; repo picker missed
nested projects (repos/PixelPioneers/*); quick terminal for dev servers/
migrations; fast viewing of files agents mention (md first-class).

Shipped (built, tested, installed):

- **Rooms**: a project IS a room. Left-click a project row → the right pane
  becomes `muster room <proj> --watch` (bubbletea viewport, 2s refresh,
  wheel/q): every mesh message to any member agent (+ member→you traffic)
  in the last 24h as one chat — day dividers, `you → dummy`, and **✓✓ read
  receipts** from ppz ack:read envelopes. The `+` at the row's end still
  opens the spawn form; esc or selecting an agent leaves. Room ownership of
  the pane is a `room:` lastTarget + `roomView` model field so the 2s tick
  doesn't clobber it (that WAS a bug — retarget reconciles from selection).
  CLI: `muster room <proj>` prints the transcript.
- **Right-click fixed + right pane**: menus now open on mouse RELEASE — a
  tmux menu opened while the button is down dies the moment you let go
  (that was the "closes on release" bug). For the agent pane: bootstrap now
  binds root-table `MouseUp3Pane` (unbound in stock tmux) guarded by
  `session==muster && pane_current_command!=muster`; outside muster it
  replicates the unbound default (forward when mouse_any_flag). In-muster it
  runs `muster rmenu <pane> <mouse_x> <mouse_y>`, which maps pane_tty →
  nested client → mstr-* session → agent, then shows a self-contained menu:
  send (tmux command-prompt), open-a-file, terminal-here, zoom, inbox
  (display-popup 80%×70% — also dodges the 38-col clip), schedule, kill
  (confirm-before). No sidebar roundtrip. Claude panes grab the mouse
  (verified mouse_any_flag=1) so the pass-through press is harmless.
- **skip-permissions default ON**: `injected()` adds
  `--dangerously-skip-permissions` at compose time (NEVER into Argv — the
  faithful-resume guarantee; deduped if the user's argv has it). New
  `<state>/config.json` ({skip_permissions, repo_roots, repo_depth}), env
  MUSTER_SKIP_PERMISSIONS=0 overrides; `muster init` writes defaults.
- **Recursive repo discovery**: picker now WalkDirs each root to repo_depth
  (default 3), pruning dot-dirs/node_modules/vendor/etc and not descending
  into found repos — so repos/PixelPioneers/<proj> shows up. Roots: env >
  config > defaults.
- **Quick terminal**: `t` (and right-click "terminal here" on agents AND
  projects) opens a 12-line shell strip under the agent pane, cwd = agent/
  project dir — dev servers, migrations, git.
- **File viewer**: `v` (or right-click "open a file…") scrapes path-looking
  tokens from the agent's screen (+300 lines scrollback), stats them against
  the agent's cwd, and menus the hits (last-mentioned first, max 12). Pick →
  pager in a split beside the agent (glow for .md if installed, else bat,
  else less; images/PDFs → macOS `open`). q closes the split; prefix+z
  fullscreens. Chat + reading side by side, as requested.
- **Detail panel decluttered**: one fact per line — glyph+name+state+age /
  model+ctx+unread (or the blocked reason, promoted, in red) / dir+branch /
  ★role-or-$cmd.
- **Keymap**: docs/KEYMAP.md — design rules (tmux-native, vim motion,
  uppercase = wider blast radius, every menu item names its key) + full
  tables + deliberate future keys. `?` help updated (user's prefix is C-a,
  so help says "prefix" not C-b).

### Session 4 E2E evidence (headless, scratch server + stub claude)

Composed spawn showed `--dangerously-skip-permissions` while the stored spec
argv stayed `['claude']`. Picker listed repos/PixelPioneers/{game-one,two}
(depth 2) and pruned node_modules. Project click → right pane ran
`muster room` (title #proj, member list, empty-state), survived refresh
ticks, and clicking the agent restored the real nested client. `t` opened a
zsh split in the agent's dir. Right-pane tty resolved to mstr-dummy (rmenu
chain). LIVE mesh: `muster send dummy` + a real inbox read produced
`you → dummy ✓✓` in the room transcript. go vet/test/gofmt clean.

## Session 3: the team release

User direction: folder-you-open = space (herdr-style); no path typing;
herdr's right-click splits + worktree menus; model/context/5h per agent;
pipes must go from "confusing CLI" to first-party legibility; and the END
GOAL stated explicitly: named agents (Alice, Dave, Peter…) with roles that
message each other BY NAME, run standups, hand off reviews. Standup is an
example of what must be possible, not a dedicated panel.

Shipped (all committed; binary installed to ~/.local/bin/muster):

- **Spaces**: `muster` run inside a repo auto-registers it as a project
  (subdir → repo root), records it as the current space (`space` file in
  state dir, re-pointed every launch), sidebar floats it first with a ●.
  Spawn forms preselect it.
- **Repo picker**: `[+ project]`/`P` = filter-as-you-type list of git repos
  discovered under `MUSTER_REPO_ROOTS` (default ~/Repos ~/code ~/src …),
  click or enter to add; literal `~/`|`/` input still accepted.
- **Right-click** (tmux display-menu at pointer — native rendering/keys):
  agent → type-into / split right·down·left (pins an EXTRA live nested
  client so several agents are visible at once) / zoom / send / schedule /
  inbox / kill (resume+kill when dead). project → new agent, new worktree
  agent (branch prefilled `wt-<hex4>`), remove from sidebar. Menu→TUI
  roundtrip via hidden F6/F7/F8 keys + menuProj state. `z` = zoom key.
- **Usage badges**: injected settings now include a `statusLine` command
  (`muster hook status-line`) — Claude pipes model/context%/rate-limits
  JSON per update; muster persists `status/<uuid>.usage.json` and renders
  the agent's in-pane status line. Sidebar rows show ctx% (colored ≥60/85),
  detail shows model (`Haiku 4.5 · ctx 18%`), header shows account-wide
  `5h N%→HH:MM`. Settings are REGENERATED at every launch (upgrades reach
  agents without re-running init). herdr does NOT have this (verified —
  its detection is screen-scrape state only): differentiator.
- **Roles + team briefing**: `spawn --role` / form field; mesh briefing now
  includes own role + teammate roster (name+role, recomputed each launch)
  + the STANDUP protocol. `muster standup` = broadcast with a reporting
  prompt; replies are ordinary mstrctl inbox traffic (T in the TUI).
- **Pipes first-party**: header shows `pipes ok/off` live; `M` (or click
  the title) = mesh view: raw `ppz status`, team table (liveness·state +
  roles, foreign handles marked ·ext), recent messages to mstrctl,
  schedules, caps. When pipes is OFF it's a guided connect screen:
  [1] start daemon, [2] `ppz login pipescloud.io` run INTERACTIVELY in the
  agent pane (device flow, browser). ppzReady now cached 10s;
  ppzCmd sets NO_COLOR + PPZ_UPDATE_CHECK=0.
- **Polish**: `o` toggles attention-sort (blocked→working→idle→dead, flat)
  vs project grouping; macOS notification on transition INTO blocked
  (MUSTER_NOTIFY=0 disables).

### Live E2E evidence (2026-07-09, real local mesh + real haiku agents)

Spawned alice (role: manages the ibex admin backoffice, repoA) via the
FORM and dave (game dev for pixel studios, gamesrv) via CLI. Dave's
composed argv contained the roster ("Teammates: alice (manages the ibex
admin backoffice)"). Then:
`muster send dave "ask alice what she's working on, report as RELAY:"` →
70s later mstrctl inbox held `dave  RELAY: alice is working on the ibex
user-permissions page redesign` (dave messaged alice BY NAME, she
answered, he relayed; ack:read receipts throughout). `muster standup` →
both replied within 10s in Task/Progress/Blockers/Next format. Mesh view
showed status/team/messages live; usage files showed Haiku 4.5, ctx
18-19%, 5h 4% with reset time rendered in the header. Splits: alice in
main pane + dave pinned below simultaneously (2 nested clients). Picker,
space ●, F7 branch-prefill, priority sort, headless right-click all
verified by capture. Unit tests + vet green.

## What this is

tmux-first agent-team manager on the ppz mesh. Single Go binary, deps =
charmbracelet only, no daemon. Workspace mode: sidebar TUI pane + the
selected agent's REAL terminal (nested tmux client) — see DESIGN.md.

## Environment on this machine

- Repo `Repos/pipe-terminal/muster`; build `go build -o muster .`
- Local ppz mesh RUNNING (server :8080, NATS :4222, Postgres `ppz`).
  `.dev/ppz-local/start.sh` after reboot; NEVER regenerate
  `.dev/ppz-local/nats.env`. ppz CLI symlink: `~/.local/bin/ppz`.
- User's live fleet: `pixel` + `tester` (~/.local/share/muster/). Scratch
  testing: MUSTER_STATE_DIR + `-L mstrtest` + **MUSTER_NOTIFY=0** (else
  test agents pop real desktop notifications — learned the loud way).
- Mesh handle pollution from tests: alice/dave/worker*/runner… exist on
  the local mesh's who list (offline). Harmless; `ppz source destroy
  <handle>` cleans up (there is no `source rm`).

## Known issues / next session TODO

- **Human dogfood still pending** (all E2E is headless): real-mouse feel,
  login-in-pane flow, notification UX, room chat with a real multi-agent
  conversation.
- ~~rmenu placement~~ FIXED (session 5): `#{mouse_x}/#{mouse_y}` are
  PANE-relative but `display-menu -x/-y` are client-absolute, so the agent
  menu opened a sidebar-width left of the pointer. rmenu now adds
  `#{pane_left}/#{pane_top}` (fetched in the existing display-message call);
  numeric `-y` anchors the menu's BOTTOM edge. Also added herdr-style
  split right/down/up/left (l/j/u/h) to the agent-pane menu — plain shell
  in the agent's dir, focused (herdr semantics; the sidebar menu's splits
  still pin extra views of the agent). Headless-verified: menu at pointer
  in the right pane, split right spawns zsh at pane_left=120.
- cmd+click on file paths is terminal-emulator territory (iTerm semantic
  history), not reachable from tmux — `v` / right-click is the muster way.
  glow isn't installed on this machine; .md falls back to bat (fine).
- rooms are backed by a shared uncollared pipe since session 6; the view
  still unions member-inbox DMs, so an agent messaging someone OUTSIDE the
  room shows in the recipient's room, not the sender's. Acceptable.
- unread badge counts lifetime messages, not unread (see session 5 notes;
  `ppzReadInbox` is dead code awaiting the Slack-style cursor fix).
- command-prompt inputs with double quotes would break rmenu's send/cron
  shell templates (user typing into their own shell — not a boundary).
- ~~Sidebar `i` inbox clipped to 38 cols~~ fixed session 7 (popup); the
  `C` schedules list still renders in the sidebar (rarely long — fine).
- ~~Pinned split panes get default titles~~ fixed session 7 (wpin).
- Stall threshold (10m) is a guess — one long tool call (big build) can
  false-positive. Tune after dogfood; MUSTER_STALL_MIN=0 disables.
- `review` picks the FIRST live role~review agent; no round-robin.
- 2026-07-10 incident (9-agent fleet): laptop froze under load; NATS
  reconnect churn after the stall broke the ppz READ path
  (E_SERVER_UNREACHABLE) while who/status stayed up. Contributors: every
  agent's PTY repaints stream to JetStream (~2MB/10min per busy agent —
  a ppz-side throttle is FOLLOWUP material), and muster's 2s tick piled
  up hung ppz subprocesses. muster side fixed same day: every ppz call
  now has a hard timeout (ppzRun, default 10s, MUSTER_PPZ_TIMEOUT_MS)
  and the TUI refresh is single-flight (no stacking while one hangs).
  Recovery: `ppz daemon restart`, then `muster resume --all` for any
  agents whose terminal-share wrappers dropped.
- 5h % arrives only after an agent's first API response (Pro/Max only);
  rate_limits needs Claude Code ≥2.1.191.
- tmux-resurrect stale mstr-* interplay unchanged (session-1 note).

## Candidate next steps (in value order)

All five session-4 candidates are now either shipped or blocked on the
user's machine — the machine-bound remainder lives in **docs/FOLLOWUP.md**
(live-mesh E2E, real-mouse menu feel, pipescloud.io login, rebuild).

1. ~~Human dogfood~~ headless half done (session 7); live-mesh half →
   FOLLOWUP §1, real-mouse half → FOLLOWUP §2.
2. ~~`muster done` + review handoff~~ shipped session 7.
3. ~~Keymap future keys (`/`, `u`, 1–9)~~ shipped session 7.
4. ~~Menu position + pinned-pane titles~~ code half shipped session 7
   (pane_left/pane_top coords, wpin titles); feel check → FOLLOWUP §2.
5. ~~pipescloud.io~~ operational only → FOLLOWUP §3.

Fresh candidates after that: unread-badge cursor fix (FOLLOWUP §5),
comparative review of N attempts (PLAN backlog), stalled notifications.

## Round-2 sidebar audit (2026-07-16)

otto. Round-1's #2-16 batch claimed fixes that didn't hold up in real
usage; round 2 was: reproduce each one live, fix for real, close what's
genuinely verified. Full per-issue evidence lives in the closed github
issues' comments, not repeated here. Real bugs found and fixed along the
way (all live-verified, not just code-reviewed):

- **#8** unread badge: superseded mid-session by Michael/chud's own
  investigation (`docs/investigations/muster-status-display-2026-07-11.md`)
  — the badge is now honest cursor-independent inbox depth, not
  per-agent unread; open question (not closed) whether a real per-agent
  "what's new" signal is still wanted.
- **#2** state icons: `Notification` hook's idle_prompt nag vs real
  permission-block split (hookEventState), plus a heartbeat-based
  sticky-blocked correction (Michael/chud).
- **#4/#5** offline/killed agents resurrecting as phantom rows: ppz's
  `who` heartbeat log outlives `source destroy`; `remoteRows` now checks
  source-still-exists.
- **#9** broadcast/send: round 1's multiline fix landed on room.go's
  chat compose, not the sidebar's actual `b`/`s` keys. Real fix: a
  `multiline` promptSpec mode (textarea, enter-sends/ctrl-j-newline).
- **#12** conventions_prompt: was folded inside the ppz-gated mesh
  briefing, so `--no-ppz` silently dropped an opted-in project's
  conventions. Decoupled.
- **Incident**: a live-mesh verification sub-agent's cleanup destroyed
  the real production `mstrctl` ppz identity (real message-history loss,
  self-healed identity). Prevention: `MUSTER_CTL_SESSION`/
  `MUSTER_CTL_HANDLE` env overrides (ppz.go) — any future sandboxed
  testing of send/broadcast/room/schedule/spawn-with-ppz MUST set both to
  a throwaway value.
- **Worktree bucketing**: `-C` into an existing worktree never set
  `spec.Repo`, so it silently landed in no project bucket (muster's own
  `<repo>__wt/<branch>` sibling convention can't prefix-match otherwise).
  `worktreeRepoRoot` derives it via plain string manipulation of that
  convention — deliberately NOT via `git rev-parse` (which returns a
  symlink-canonicalized path that would mismatch a project registered via
  the literal `filepath.Abs` path every other codepath here uses).
- **"tmux new-session: command too long"** at ~19 teammates: two causes.
  `injected()` re-embedded the full team roster on every RESUME too (pure
  waste — the transcript already has it; now gated on a `firstLaunch`
  flag). Root cause is tmux new-session's own shell-command argument
  ceiling, well below the OS's exec() argv limit (proved directly: the
  same big string reaches the real claude process's actual argv fine
  once tmux is out of the path) — `launch()` now writes the composed
  command to a file (`dataDir()/launch/<session>.sh`) and runs `sh
  <path>` instead of embedding it inline, fixing spawn too, not just
  resume.
- **Sidebar polish** (Michael, after checking the code himself — these
  were the actual reason otto was brought back, not the two bugs above,
  which were real but crowded them out): blocked and stalled now have
  genuinely distinct colors (`cBlocked`/`cStalled`) instead of sharing
  one, and blocked is bold — Michael wants it "very visible". The
  selected row's glyph now keeps its state color instead of losing it to
  a flat `sSelected` style (the one row you're actually looking at was
  the one row with no state info). `extended-keys`/`xterm-keys` turned
  on at both tmux layers (workspace session + each agent's own session)
  so modified keys like shift+tab survive the nested attach. **Decision
  override**: the working state now has a real per-frame spinner
  (`animatedGlyph`/`workingSpinner`, ~150ms tick) — this directly
  contradicts session 7's "spinners lie" call above; Michael's explicit
  override for the working glyph specifically, his call stands regardless
  of that stance. The `stalled` derived-state honesty check underneath
  is unchanged and still the real "is it actually stuck" signal — the
  spinner is a liveliness cue layered on top, not a replacement for it.
