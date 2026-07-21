# PROPOSAL — task-first dispatch, fresh-context seeding, and the fast lane

> Written 2026-07-16 from Michael's dogfood feedback (below), a full code
> review of the spawn/refresh/briefing paths, a capability check against
> current Claude Code docs, and a sweep of ppz's extension seams.
> **STATUS: BUILT same day** (Michael: "build all of it") — see STATUS.md
> "Session 12" for what shipped and the deviations: the repurpose verb is
> named `retask` (not `fresh`); questions rank the ask lane above every
> implicit route; warm spares claim without a /clear (they're already
> fresh); fork-seeding (P6 phase-3) stays unbuilt pending a live
> `--fork-session` probe; cross-machine dispatch is plumbing-ready (ppz
> PR #4) but deliberately not wired into routing yet.
> Companion evidence: docs/RESEARCH.md, docs/COMPETITORS.md.

## 0. The feedback, distilled

Three concrete failures keep pulling Michael back to herdr:

1. **Discovery / new-task starts want fresh context + a stronger model.**
   In herdr: `/clear`, write out the context, go — better results from a
   clean window. In muster: the only agent in a project may be at 40–50%
   context, a new agent takes too long to instantiate, model is frozen in
   the agent's spec, and writing out "the basic bits about how the project
   is connected" every time is a tax.
2. **The master-agent dispatcher is a bottleneck.** The workaround for #1
   became "explain everything to one master agent and have it clear +
   brief another agent." That agent is now the busiest inbox on the mesh;
   a new task waits behind 4–5 unrelated messages before setup even
   starts.
3. **Live-ops questions need a 5-minute fast lane.** "User xxx can't get
   into the game — what's happening?" fired at the master agent → it
   pinged two agents, spawned subagents, got bogged down in replies →
   40 minutes, many tokens, no answer. The same question in herdr (fresh
   context + the triage skill + sonnet for speed) reliably takes ~5 min.

Stated priorities: **speed of getting stuff out of my head and beginning
work is top priority**; fresh context matters but must be *seeded* (basics
+ accumulated caveats + relevant skills); the team should have domain
specialists (backend / frontend / infra…) that scale with demand.

**The common thread: muster's unit of interaction is the *agent*; the
user's unit of thought is the *task*.** Every flow today starts with
"which agent?" (select a row, fill a spawn form, message the master).
herdr wins these three scenarios not because it's better engineered —
its architecture is worse on our terms (RESEARCH.md §2) — but because
`/clear` + type is a *task-first* gesture with zero routing decisions.
The fix is to make muster task-first at the entry point, and to move the
routing job from an LLM (the master agent) into muster itself, where it's
deterministic and instant.

## 1. Diagnosis — what the code actually does today

Findings from the code review (file:line as of d9d4918):

- **Model is not a first-class concept.** `AgentSpec` (spec.go:19) has no
  Model field; the model lives only inside the verbatim `Argv` (e.g.
  `["claude","--model","opus"]`). Live model is read back for *display*
  via the statusLine hook (status.go:116) and ppz heartbeats (ppz.go:234)
  but never drives anything. Changing model = hand-edit the spec or
  kill + respawn. This is exactly the "instantiated as a certain model in
  their info files" complaint.
- **Spawn latency has three components.** (a) claude's own cold boot in
  the detached pane — the dominant, inherent part; (b) muster's ~4–5
  *serialized* ppz subprocess calls in `launch()` (cmds.go:190-219:
  ensureCtlHandle, pipe create, subs add, source ls — each a fresh
  process with a 10s timeout); (c) the TUI only surfaces the new row on
  the next 2s refresh tick (tui.go:690, pendingSelect). Worktree spawns
  add serial git subprocesses (worktree.go:20).
- **Context seeding is per-agent, not per-project.** The only seeding
  machinery: the mesh briefing (spec.go:180 — identity/roster/room
  etiquette, not project knowledge), the opt-in per-project
  `conventions_prompt` (spec.go:222 — one prose string), `.muster/setup`
  (env bootstrap, not knowledge), and the *per-agent* handoff file
  re-injected on /clear (status.go:387, 9k tail). There is no shared
  "how this project is wired" primer and no accumulator for caveats
  agents learn — every fresh context re-learns or gets it typed at them.
- **Role is prose, not behavior.** `Role` (spec.go:21) feeds one sentence
  of the briefing and the roster line. No templates, no skills, no model
  binding, no way to say "give me another backend."
- **Nothing resembles a task lane.** send/broadcast/standup push text at
  *named* agents; cron schedules prompts for *named* agents; rooms are
  cooperative (CLAIMING is etiquette in the briefing, spec.go:204). The
  master-agent pattern exists precisely because muster offers no verb
  whose subject is a task rather than an agent.

Capability facts that unlock the design (verified against
code.claude.com/docs 2026-07-16):

- `claude --resume <id> --fork-session` copies a conversation into a NEW
  session id, original untouched, repeatable — a seeded session can be
  forked per task. (sessions.md#branch-a-session)
- `--model` on `--resume` overrides the transcript's model; aliases
  (opus/sonnet/haiku) accepted. (model-config.md)
- Skills work non-interactively: `claude -p "/triage-logs <question>"`
  expands the skill before running. (headless.md)
- `-p --output-format json` returns result + session_id + cost —
  machine-readable completion. `--bare` skips hook/skill/MCP discovery
  for fast one-shots (but also skips skills — use selectively).
- SessionStart hook fires with source startup|resume|clear|compact and
  injects up to 10k chars of additionalContext — our existing handoff
  re-injection (status.go:387) already rides this; it can carry more.

ppz seams (no server changes needed for phases 1–2):

- Heartbeats are explicitly additive (WIRE.md §11); `HeartbeatPayload`
  (internal/cli/heartbeat.go:23) already carries `project` from
  `PPZ_AGENT_PROJECT` "e.g. muster's registered project name". Adding
  `specialty` / `free` mirrors that pattern; `ppz who --json` carries new
  fields automatically.
- A true work-queue/claim primitive would copy the scheduler's own
  Postgres `FOR UPDATE SKIP LOCKED` + lease pattern
  (internal/db/schedules.go:200, server/scheduler.go). Phase-3 material.
- The subs-wait wake registry (daemon/watch_registry.go) is the
  event-driven wake path a pool would block on.

## 2. Proposals

Ordered so each stands alone; later ones compose with earlier ones.

### P1 — Context packs: `primer` + `lessons` (kills the retyping tax)

Two per-project markdown files, both first-class:

- **`.muster/primer.md`** (in-repo, committed): the durable orientation —
  how the project is wired, services/ports, where things live, the
  "basic bits" Michael retypes today. Human-authored, agent-maintainable.
- **`<state>/lessons/<proj>.md`** (muster state, cross-repo): accumulated
  caveats. The briefing gains one instruction: *"when you discover a
  non-obvious constraint or gotcha, append one line to `muster lesson
  add`"* — a tiny CLI (`muster lesson add|ls|edit <proj>`) so entries are
  timestamped/attributed and the file can't be clobbered by concurrent
  agents (append via O_APPEND, single line per entry).

Injection points (all existing machinery, extended):

- **On spawn**: appended after the mesh briefing in `injected()`
  (spec.go:259) — primer + last N lessons.
- **On /clear**: `emitHandoffContext` (status.go:387) today injects only
  the per-agent handoff tail. It grows two sections: primer + lessons,
  budgeted within the 10k additionalContext cap (suggested split: 4k
  primer head / 2k lessons tail / 3k handoff tail — handoff keeps
  priority for *refresh*, primer keeps priority for *fresh*, see P3).
- Role-scoped sections: `## backend` / `## frontend` headers in
  primer.md; an agent with a matching role gets its section promoted,
  others summarized. (Cheap string filter, no new format.)

This single change makes *every* fresh context — manual /clear, `f`
refresh, P3 fresh, P4 ask, P5 dispatch — start already knowing the
project. It's the highest leverage-per-line item in this proposal.

### P2 — Model becomes a launch-time parameter (spec.Model)

Add `Model string` to AgentSpec. At spawn, `--model X` in the *user's*
argv is adopted into the field and stripped from Argv (mirroring
`adoptSessionID`, spec.go:308); compose re-injects `--model <spec.Model>`
at launch, exactly like `--session-id`/`--settings`. Faithful resume is
preserved — arguably *more* faithful, since `muster model <name> opus`
followed by resume now does what the user meant. New surface:

- `muster model <name> <alias>` — live agent: types `/model <alias>` into
  the pane (persists in-session and across resume via the transcript);
  dead agent: edits the spec, next resume composes `--resume --model`
  (verified supported).
- Spawn form + templates (P5) get a model field. Detail panel already
  shows live model from usage.json — now also flags spec-vs-live drift.
- Guard tests: extend spec_test.go — adopt strips exactly one `--model`
  pair, resume composes the override, verbatim rerun for non-claude
  harnesses unchanged.

### P3 — `muster fresh <name> ["task…"] [--model X] [--skill s]`

The missing verb between `refresh` (continuity: flush handoff → /clear →
resume same work) and kill+spawn (slow). **fresh = repurpose**: /clear →
inject primer+lessons (NOT the handoff — this is a new task, the old
task's state is noise) → optional model switch → deliver the new task
text. Reuses cmdRefresh's proven skeleton (refresh.go:103: idle-wait →
/clear → rotation-wait → kick) minus the flush step, plus a
`fresh-marker` so the SessionStart hook knows to inject primer-only.

Sidebar key: `F` (f stays refresh). This directly answers "the only
available agent may be at 40–50% context": select, `F`, type the task,
enter — a seeded fresh agent in ~10 seconds with zero new processes.

### P4 — `muster ask` — the ephemeral fast lane (the live-ops fix)

A one-shot, fresh-context, skill-armed, auto-reaping question:

```
muster ask [--skill live-triage] [--model sonnet] [-C dir|--proj p] "user xxx can't get into the game — what's happening?"
```

- Spawns a normal tmux pane (visible, tailable, killable — muster
  principles hold) running `claude -p --output-format json` with:
  primer+lessons via `--append-system-prompt`, the question as the
  prompt, `/skill-name` prefixed when `--skill` is given (verified: -p
  expands user-invoked skills). Model defaults to a config
  `ask_model` (suggest sonnet — Michael's own call for speed).
- **No mesh identity.** No handle, no room subscribe, no roster in the
  briefing → structurally immune to the "got pulled into 4–5
  conversations" failure. It cannot be messaged; it exists to answer.
- Completion: the JSON result is captured (the pane runs
  `claude -p … | muster hook ask-done <id>`-style plumbing), stored
  under `<state>/asks/<id>.json` (question, answer, model, elapsed,
  cost), desktop-notified, badged in the TUI. Pane auto-reaps after the
  answer is stored (config `ask_keep_pane` to disable while trusting it).
- Sidebar: asks render in a small ASKS section with elapsed time; enter
  opens the answer popup; `muster answers` lists recent ones.

The 40-minute incident becomes: one keystroke (P7 palette), type the
question, tag `/live-triage`, enter. First tokens flow in seconds; no
inter-agent chatter is *possible*. This is herdr's 5-minute flow, minus
herdr (and minus even herdr's manual /clear + context-typing, thanks to
P1).

Open question (flagged, not blocking): skills discovery in `-p` honors
project `.claude/skills/` — the live-triage skills already live with the
game repos, so cwd selection (`--proj`) is the only routing decision.

### P5 — Roles become templates; `muster dispatch` replaces the master agent

**Templates** (`<state>/templates.json` + optional in-repo
`.muster/templates.json`, merged): named bundles —
`{name, role_prompt, model, argv_extra, skills[], primer_sections[],
default_project}`. `muster spawn --as backend` auto-names (`backend-2`,
`backend-3`…), fills role/model/briefing from the template. The spawn
form's first field becomes a template selector (`(blank)` = today's
form). This is "one really good at backend, one frontend, one infra —
add more as demand grows" as a first-class object.

**Dispatch** — the deterministic router that does the master agent's job
without an LLM and without a queue behind someone's inbox:

```
muster dispatch [--as backend] [--fresh] [--model X] "task…"
```

Resolution order (all local reads, instant):
1. An **idle** agent whose template/role matches → deliver via
   send (or `--fresh` → P3 fresh-cycle first, recommended default for
   new tasks).
2. Else, if the matching pool is below its cap → spawn-from-template
   and queue the brief for delivery on first idle (the existing ppz
   pump delivers it — no new machinery).
3. Else → refuse loudly with the pool status ("backend ×3 all working —
   b1 idle in ~?, or --force-spawn").

The master agent isn't banned — it's just no longer *load-bearing*. What
it did (clear + brief + hand over) is now `dispatch --fresh`, which is
O(seconds) and burns zero routing tokens. If LLM-judgment routing is
ever genuinely wanted, it's a `muster ask` away (`--skill route` over
the roster), not a standing bottleneck.

### P6 — Instantiation speed (attack all three latency components)

- **Parallelize/defer the ppz block** (cmds.go:190-219): ensureRoomPipe +
  subscribeRoom + ppzSourceExists are independent — run concurrently
  (errgroup) and don't gate tmux new-session on them; the pane command
  only needs the handle string. Saves the serialized worst case.
- **Optimistic sidebar row**: submitForm/dispatch inserts a `pending`
  row immediately (spinner glyph, no status file yet) instead of waiting
  for the 2s tick. Perceived latency matters most at exactly this moment.
- **Warm spare** (config `warm_spare: true` per project/template): after
  the workspace boots (and after each claim), muster keeps one pre-booted
  idle claude per configured template, already primed via P1. Dispatch
  claims it (rename spec + deliver task) and background-spawns the next
  spare. Cost: one idle process + one primed context per template —
  tokens ≈ primer size, cheap on Max. Feels instant, which is the stated
  top priority.
- **Fork-seeding (phase 3, behind a flag)**: keep a per-project *seed
  session* — a claude session that has read primer + repo layout once
  (regenerated by cron when primer changes). New agents launch as
  `claude --resume <seed> --fork-session --model X`: orientation is
  already *in context* (not just in the prompt), repeatable, original
  untouched. Trade-off: forks inherit the seed's context% (keep seeds
  lean, ~10–15%) and `--fork-session` with `-p` is docs-ambiguous —
  needs a live probe before we depend on it. Prompt-cache-friendly.

### P7 — ppz-side (phase 3, when pools span machines)

Nothing in P1–P6 needs ppz changes. When dispatch should pick targets
across the 3-machine mesh:

- **Heartbeat fields** (additive, no server change):
  `PPZ_AGENT_SPECIALTY` → `specialty`, plus muster-maintained
  `current_task`. `ppz who --free --specialty backend` becomes the
  cross-host candidate query. Mirrors the existing `PPZ_AGENT_PROJECT`
  seam (heartbeat.go:38,119).
- **Interrupt pipe convention**: a per-handle `<h>.alert` pipe that the
  pump treats with a shorter idle-gate than inbox — so a dispatch brief
  or live-ops question doesn't queue behind room chatter. Convention +
  pump tweak only; no wire change.
- **Real task queue with claims**: only if pools get big enough that
  "dispatch picks one name" stops scaling — copy the scheduler's
  SKIP-LOCKED lease pattern into a `tasks` table + claim verb. Explicitly
  deferred; rooms' CLAIMING etiquette covers the cooperative case today.

## 3. UI/UX

### The dispatch palette — the "out of my head" surface

One unprefixed key (proposal: **`;`**, unassigned; KEYMAP rule 1 lets the
sidebar own it) opens a full-width tmux display-popup — same mechanism as
recap/inbox, so zero new rendering tech — containing ONE focused input.
**You type the task first.** Routing is expressed inline, Slack-style,
all optional:

```
; user 4821 can't log in since the 14:00 deploy @live-triage !sonnet /triage-logs
```

- `@name-or-template` → target (agent, template, or `@ask` for P4's
  ephemeral lane; default: config per project, suggest `@ask` for
  questions ending in `?` — half-joking, default is the space's
  template).
- `!model` → model override; `/skill` → skill; `#proj` → project when
  not the current space.
- A live preview line under the input shows the *resolved* plan before
  enter commits: `→ fresh backend-2 (idle 40%→0%) · sonnet · primer+12
  lessons` or `→ new ask · sonnet · /triage-logs · ~5s to first tokens`.
  Enter dispatches; esc cancels; the popup closes and an optimistic row
  appears selected.

This is deliberately the inverse of the spawn form: the form is
agent-first (five fields, then work exists); the palette is task-first
(work exists, then routing tokens if the defaults are wrong). The spawn
form stays for deliberate fleet-building.

### Keymap deltas

| key | action |
|-----|--------|
| `;` | dispatch palette (task-first entry) |
| `F` | fresh — repurpose selected agent: /clear + primer + new task (prompted) |
| `A` | answers — recent `ask` results popup |
| `m` | set model for selected agent (menu: opus/sonnet/haiku/custom) |

(`f` refresh, `a`/`S` spawn form, and all existing keys unchanged.
Right-click menus gain the same verbs per KEYMAP rule 3.)

### Sidebar

- Group pool members under their template when grouping by project:
  `backend ×3` header, members indented — the "team of specialists" is
  visible structure, not naming convention.
- ASKS section (only when asks are live/recent): `? live-triage 2m ⚙` →
  `? live-triage 4m ✓` with the answer one enter away.
- Pending/optimistic rows render immediately with a distinct glyph.
- Detail panel: template name, spec model (+ live-model drift), primer
  age ("primer 12d · 37 lessons") — staleness of the seed becomes
  glanceable.

### TUI stack (the "look into TUI libraries" ask)

Reviewed. Recommendation: **change nothing structural.** The
tmux-substrate + bubbletea sidebar architecture is the moat
(RESEARCH.md §2 — herdr's 180K LOC is the cost of the other road), and
every surface this proposal needs already exists in the stack:
display-popup (palette, answers), textinput/textarea (palette input —
`bubbles` already vendored), display-menu (model picker). Two optional
charmbracelet additions stay within the "charmbracelet-only" dependency
rule if wanted later: `huh` (nicer multi-field forms — only worth it if
the spawn form grows template/model fields awkwardly) and `bubbles/table`
for the answers list. Neither is required for phase 1. Explicitly
rejected: any richer TUI framework that would own panes/PTYs — that's
re-deriving herdr.

## 4. Phasing

- **Phase 1 (one session, highest leverage):** P1 primer/lessons +
  injection; P2 spec.Model + `muster model`; P3 `muster fresh` + `F`;
  P4 `muster ask` + ASKS section + notifications. Ships every piece of
  the live-ops fast lane and the discovery flow.
- **Phase 2:** P5 templates + dispatch + `;` palette; P6 ppz-block
  parallelization + optimistic rows + warm spare. Retires the
  master-agent pattern.
- **Phase 3 (needs live probes / ppz work):** P6 fork-seeding (probe
  `--fork-session` with `-p` first); P7 heartbeat specialty/free +
  who filters + alert pipe; queue-with-claims only on demonstrated need.

## 5. Invariants (unchanged, load-bearing)

- **Faithful resume**: Model adoption strips-and-composes exactly like
  session-id adoption; Argv stays verbatim for everything else; all
  spec_test.go guards extended, none weakened.
- **Never own a PTY / never scrape**: asks are plain panes + hook
  events; the palette is a display-popup; answers come from `-p` JSON
  output, not screen reads.
- **TUI actions self-exec the CLI**: palette/fresh/ask/dispatch/model
  are all CLI verbs first (`runSelf` pattern), so scripts and cron can
  drive them too — e.g. a ppz schedule that runs a nightly
  `muster ask --skill health-sweep`.
- **ppz optional**: P1–P4 and P6 work fully off-mesh; dispatch degrades
  to local-only candidates without ppz.
