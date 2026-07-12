# Architecture: cross-machine muster — native local capability vs remote mesh view

Agent: chud (opus), tech lead for the cross-machine question. Consolidates a
decision reached and validated with greg + Michael over 2026-07-10/07-11 (full
evidence trail in `docs/STATUS.md` sessions 9–10 and the chud handoff), now that
most of the mesh-view path has shipped and merged. This doc is the single
source: the fork, the decision, what shipped, what is deliberately NOT built.

**Status:** recommendation, ratified in substance (greg + Michael, live), most of
it shipped. Awaiting Michael only on whether he wants anything *beyond* this doc
(e.g. a prototype). Not yet folded into `DESIGN.md` — see "Doc follow-up".

---

## The fork

muster's fleet is now genuinely multi-machine: this Mac (most agents, one shared
ppz daemon), a mikle-linux desktop, and an always-on Linux **server**
(192.168.1.32) that hosts the ppz broker 24/7. The question:

- **Native local capability** — muster is a *per-machine* tool. Each machine's
  muster natively spawns and manages *that machine's* agents: real tmux, Claude
  hooks, git worktrees, and — critically — a real browser, docker, dev-servers,
  and filesystem physically on that box. Cross-machine is *federation*, not
  central control.
- **Remote mesh view** — one muster instance is the control surface; agents on
  other machines are reached *through the ppz mesh*: surfaced in the sidebar,
  watched read-only, and driven live via a NATS-relayed pty. The maximal version
  of this pole is a **native cross-machine control plane** (muster spawns and
  manages agents on remote hosts from one central point).

`DESIGN.md` (Non-goals) already picked mesh-view when the fleet was single-box:
*"No remote-server mode of our own (ppz hosted mesh already covers cross-host
messaging; ssh + tmux attach covers remote attach)."* The fleet went
multi-machine, so the question was reopened. This doc confirms that call and
draws the line precisely.

---

## What the user actually needs (validated with Michael, priority order)

1. **PARAMOUNT — hands-on capability where work happens.** An agent doing visual
   / integration work needs a real browser (Chrome to validate a design),
   dev-servers, docker, real testing — on the machine it runs on.
2. **THE EMERGED PRIORITY — continuous *unattended* work.** Fire off work, close
   the laptop, keep going, review results later (PRs / screenshots / reports).
   This is the need that actually reshapes the architecture: it demands an
   *always-on host*, because the whole team runs on the Mac today and does **not**
   survive laptop-close.
3. **Cross-machine interaction is mostly async.** Reports, PRs, screenshots,
   ppz messages. ivy/jack already post chrome-devtools-MCP screenshots on PRs.
4. **NICE-TO-HAVE, explicitly sacrificable.** Live control of a remote agent from
   wherever you sit; continuing the *same live conversation* after switching
   machines (Michael switches machines ~weekly and accepts starting fresh).

---

## The physical constraint that decides it

The paramount need cannot be met by projecting one machine's session onto
another. **Compute — and everything hands-on hangs off it — is physically local.**
A Mac agent's Chrome window opens on the Mac. Its dev-server binds the Mac's
localhost; its docker is the Mac's docker daemon; its files are the Mac's disk.
No mesh feature (watch, attach, a future live-TUI) can move a browser window or a
bound port across machines — those relay a *terminal*, not a display or a socket.

So an agent must **run on the machine where its hands-on work is real.** That is
not a preference; it is a fact about where pixels and ports live. It settles the
paramount need in favor of *native local capability* and rules out ever solving
hands-on work by remote-projection.

---

## The decision (layered — not either/or)

**1. Compute is local, always. muster stays per-machine-native for the agent
lifecycle.** Spawn / resume / kill / worktrees / hooks / self-testing all run on
the box where the agent lives, at full local fidelity. This is the non-negotiable
core; it directly serves need #1.

**2. The mesh is the cross-machine VIEW + live-control surface — not a control
plane.** From wherever you sit you can *see* and *drive* any agent on any machine
without muster owning a remote-spawn daemon:
   - surface remote agents in the main sidebar (not just the `M` mesh view),
     bucketed under their real project;
   - watch read-only;
   - **full-fidelity live attach** over the mesh — raw pty relay with real
     keystrokes, Ctrl-C passthrough, resize, and detach — via
     `ppz terminal attach`; or `ssh + tmux attach` where the host is reachable.
   This serves need #4 without reintroducing a daemon.

**3. Continuous-unattended work → put the always-on team on the always-on host.**
The team that must survive laptop-close should run *natively on the Linux server*
— still "native local capability," just on the box that never sleeps. The server
already has docker + Playwright headless chromium + node + healthy Claude auth
(inventory verified). Interaction is the async model that already exists (PRs /
screenshots / ppz), plus mesh watch/attach from the laptop for a live look. This
is the answer to need #2.

**4. Deliberately DO NOT build a native cross-machine control plane.** No muster
daemon that spawns/manages agents on remote hosts from a central point. See next
section for why and the trigger that would change it.

---

## What has shipped (evidence — this is not aspirational)

**muster (repo `mikle7/muster`, on `master`):**
- `340ad22` surface remote ppz-mesh agents in the main sidebar
- `bef30d7` bucket remote agents under their real project
- `ace8b63` embed real `ppz terminal attach` for remote rows (not watch-only)
- `52f2c5d` debounced auto-attach on select (no enter required)
- `fe900f7` persistent mesh-proxy sessions `mstr-mesh-<name>` for remote rows —
  glance-away-and-back is a cheap `switch-client`, not a fresh connect + replay
  (STATUS.md session 10 addendum 2)

**ppz (repo `mikle7/ppz`, merged to `main`):**
- `ac3dc06` `terminal attach` — bidirectional live control over the mesh
- `4ff227a` self-attach guard (stdout→stdin feedback-loop footgun)
- `bb0b404` `--embedded` mode for persistent mesh-proxy hosts
- `feae932` re-assert input modes to viewers on attach (fixes mouse/scroll, #17)

So the entire "remote mesh view + live control" pole is **built, tested, and
merged** on both sides. The decision is mostly a matter of *not* adding the one
thing left (a control plane) and of *executing* the server relocation.

---

## What is deliberately NOT built — and when to revisit

**A native cross-machine control plane** (central `muster spawn <agent> --host
<remote>`, remote lifecycle management, fleet orchestration across hosts).

Why not, today:
- **ssh + tmux and mesh-attach already cover remote control** at equal or higher
  fidelity and zero new infrastructure. Spawning on a remote host is *rare*
  compared to interacting with what's already there; `ssh <host> && muster spawn`
  covers the rare case.
- **It reintroduces the daemon muster's design rejects.** DESIGN principle 4:
  *"No daemon."* State today = tmux server + ppz mesh + flat JSON. A remote
  control plane needs a privileged always-on remote agent, host discovery, a
  trust model, and cross-host state reconciliation — a large surface for a need
  no one has yet articulated concretely.
- **YAGNI.** Nothing in the validated needs (#1–#4) requires central remote
  spawn. Need #2 is solved by *relocating* agents to the always-on host once,
  not by spawning across the network continuously.

Revisit **only** when a concrete trigger appears, e.g.: routinely spawning agents
across hosts that ssh **cannot** reach (NAT / no sshd / mobile / different
network), or fleet size where hand-placing agents per host stops scaling. Until
then this is speculative and stays unbuilt. `ppz terminal attach` already proved
the mesh transport exists if a control plane is ever justified — it would ride
the same channels, so deferring costs nothing.

---

## Tradeoffs accepted (stated honestly)

- **No central remote spawn.** To put an agent on the server you ssh in (or run
  muster there) once. Accepted — spawning is rare vs. interacting.
- **Live-conversation continuity is sacrificed across machines.** Mesh attach
  relays the *live screen*, not the remote agent's full scrollback/history — a
  fundamental limit of screen-relay (root of the #16/#17 work), not a bug to fix.
  Michael demoted this to nice-to-have. The intended workflow for "what happened
  while I was away" is async: `ppz terminal read` snapshot, PRs, recap, reports.
- **Two reach paths, two failure modes.** Mesh attach depends on the ppz broker;
  ssh depends on host reachability + sshd. Known gap: ssh is currently Mac→Linux
  only (this Mac has no sshd / Remote Login off), so Linux→Mac live control needs
  either Remote Login enabled or the mesh-attach path. The mesh path sidesteps it.

---

## Open items

1. **Server relocation NOT executed.** Per `ppz who`, the muster/pixel-studios
   team is still Mac-hosted; only `booth` runs on the server. The
   continuous-unattended need (#2) is unmet until the always-on team is moved
   there. Gate previously flagged (Claude 401 on the *desktop*) does **not** apply
   to the *server* — server Claude auth verified healthy. Blocker: none known;
   it's setup work (spawn the team natively on the server), not architecture.
2. **Upstream ppz PR** (pipescloud/ppz for `terminal attach`) parked pending
   Michael's private roadmap to align verb/semantics. Low conflict risk.
3. **`DESIGN.md` follow-up.** Its Non-goals still read as a flat "no remote-server
   mode." Once this is ratified, update that bullet to point here and state the
   *reasoning* (mesh-view chosen; control plane deferred with a named trigger),
   so the decision isn't just an absence.

---

## Recommendation in one paragraph

Keep muster per-machine-native for the agent lifecycle — compute, and every
hands-on capability that hangs off it, is physically local and cannot be
projected. Use the ppz mesh as the cross-machine *view and live-control surface*
(surface + watch + `terminal attach`), which is already shipped and merged, not
as a control plane. Solve continuous-unattended work by *relocating* the
always-on team to the always-on server (native there), with async relay +
mesh-attach from the laptop — the one remaining piece of execution. Do not build
a central remote control plane; ssh + mesh-attach cover remote control at higher
fidelity and zero new infra, and the daemon it would require is exactly what
muster's design rejects. Revisit only on a concrete ssh-can't-reach or
fleet-scale trigger.
