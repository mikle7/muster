package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Workspace mode: `muster` always runs its UI inside a dedicated tmux
// session ("muster") — left pane is the sidebar TUI, right pane is a REAL
// tmux client attached to the selected agent's session (nested attach with
// TMUX unset, same server). Typing in the right pane is typing into the
// agent — no PTY ownership, no emulation, no capture-pane preview. Selection
// changes just retarget that inner client with switch-client.

const wsSession = "muster"

func isEmbedded() bool { return os.Getenv("MUSTER_EMBEDDED") == "1" }

// bootstrapWorkspace ensures the muster session exists (pane 0 = the
// embedded TUI) and moves the user's terminal into it.
func bootstrapWorkspace() int {
	// the folder you open muster in is your space — even when re-joining
	// an already-running workspace from a different repo
	if cwd, err := os.Getwd(); err == nil {
		setSpace(cwd)
	}
	if tmuxHasSession(wsSession) && (!workspaceAlive() || workspaceBinaryChanged()) {
		_ = tmuxKillSession(wsSession) // continuum-restored shell, crashed TUI, or rebuilt binary
	}
	if !tmuxHasSession(wsSession) {
		args := []string{"new-session", "-d", "-s", wsSession, "-e", "MUSTER_EMBEDDED=1"}
		for _, k := range []string{"MUSTER_STATE_DIR", "MUSTER_TMUX_ARGS", "MUSTER_TMUX", "MUSTER_PPZ",
			"MUSTER_DEFAULT_CMD", "MUSTER_SKIP_PERMISSIONS", "MUSTER_REPO_ROOTS", "MUSTER_NOTIFY",
			"MUSTER_STALL_MIN", "MUSTER_REFRESH_PCT", "MUSTER_PPZ_TIMEOUT_MS", "MUSTER_REFRESH_WAIT_S"} {
			if v := os.Getenv(k); v != "" {
				args = append(args, "-e", k+"="+v)
			}
		}
		args = append(args, shQuote(selfExe())+" ui")
		if out, err := tmuxRun(args...); err != nil {
			return fail(errf("tmux new-session: %v: %s", err, out))
		}
		// workspace-scoped looks; the user's tmux config is untouched
		// (targets need =name: — bare =name is rejected by set-option)
		_, _ = tmuxRun("set-option", "-t", "="+wsSession+":", "mouse", "on")
		_, _ = tmuxRun("set-option", "-t", "="+wsSession+":", "status", "off")
		_, _ = tmuxRun("set-option", "-w", "-t", "="+wsSession+":", "pane-border-status", "top")
		_, _ = tmuxRun("set-option", "-w", "-t", "="+wsSession+":", "pane-border-format", " #{pane_title} ")
		// outer half of the nested-attach key relay — see launch()'s
		// matching set on each agent's own tmux session for why both
		// layers need this (modified keys like shift+tab otherwise get
		// eaten/mangled at whichever layer doesn't opt in).
		_, _ = tmuxRun("set-option", "-w", "-t", "="+wsSession+":", "xterm-keys", "on")
		_, _ = tmuxRun("set-option", "-w", "-t", "="+wsSession+":", "extended-keys", "on")
	}
	bindRightClick()
	if os.Getenv("TMUX") != "" {
		if out, err := tmuxRun("switch-client", "-t", "="+wsSession); err != nil {
			return fail(errf("switch-client: %s", out))
		}
		return 0
	}
	argv := []string{tmuxBin()}
	if extra := os.Getenv("MUSTER_TMUX_ARGS"); extra != "" {
		argv = append(argv, strings.Fields(extra)...)
	}
	argv = append(argv, "attach", "-t", "="+wsSession)
	path, err := lookPath(tmuxBin())
	if err != nil {
		return fail(err)
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		return fail(errf("exec tmux: %v", err))
	}
	return 0
}

// bindRightClick makes right-click work on the agent (right) panes, not just
// the sidebar. MouseUp3Pane is unbound in stock tmux, and the binding is
// self-scoping: inside the muster session (except the sidebar pane, which
// handles its own mouse) it opens the agent menu; everywhere else it exactly
// replicates the unbound default (forward to mouse-aware panes). Release-
// triggered on purpose — menus opened while the button is down close the
// moment you let go. Idempotent; inert outside muster.
func bindRightClick() {
	cond := "#{&&:#{==:#{session_name}," + wsSession + "},#{!=:#{pane_current_command}," + filepath.Base(selfExe()) + "}}"
	ours := `run-shell -b "` + selfExe() + ` rmenu '#{pane_id}' '#{mouse_x}' '#{mouse_y}'"`
	passthru := "if-shell -F -t= '#{mouse_any_flag}' 'send-keys -M -t='"
	_, _ = tmuxRun("bind-key", "-n", "MouseUp3Pane", "if-shell", "-F", cond, ours, passthru)
}

// workspaceAlive reports whether some pane in the muster session still runs
// the muster binary (the TUI). pane_current_command is the process basename.
func workspaceAlive() bool {
	out, err := tmuxRun("list-panes", "-s", "-t", "="+wsSession, "-F", "#{pane_current_command}")
	if err != nil {
		return false
	}
	self := filepath.Base(selfExe())
	for _, c := range strings.Split(out, "\n") {
		if strings.TrimSpace(c) == self {
			return true
		}
	}
	return false
}

// workspaceBinaryChanged reports whether the on-disk muster binary is newer
// than the running workspace session was created — meaning a rebuild happened
// since launch, and we should restart fresh to pick up the new binary (#15).
func workspaceBinaryChanged() bool {
	out, err := tmuxRun("display-message", "-t", "="+wsSession+":", "-p", "#{session_created}")
	if err != nil {
		return false
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return false
	}
	info, err := os.Stat(selfExe())
	if err != nil {
		return false
	}
	return info.ModTime().After(time.Unix(secs, 0))
}

// ---- right pane (the live agent view), driven by the embedded TUI ----------

type workspacePanes struct {
	left       string // the TUI's own pane id ($TMUX_PANE)
	right      string // live-agent pane id, "" until created
	lastTarget string // dedupes retarget calls ("live:x" | "dead:x" | "none")
}

func paneExists(id string) bool {
	if id == "" {
		return false
	}
	_, err := tmuxRun("display-message", "-p", "-t", id, "#{pane_id}")
	return err == nil
}

// ensureRightPane creates (or adopts) the agent pane and pins the sidebar
// width. Called on every WindowSizeMsg — cheap no-op when all is well.
func (wp *workspacePanes) ensure(tuiWidth int) {
	if wp.left == "" {
		wp.left = os.Getenv("TMUX_PANE")
	}
	if wp.left == "" {
		return // not in tmux (shouldn't happen in embedded mode)
	}
	if !paneExists(wp.right) {
		wp.right = ""
		// adopt a survivor pane if one exists (e.g. TUI restarted)
		out, err := tmuxRun("list-panes", "-t", wp.left, "-F", "#{pane_id}")
		if err == nil {
			for _, id := range strings.Split(out, "\n") {
				if id = strings.TrimSpace(id); id != "" && id != wp.left {
					wp.right = id
					break
				}
			}
		}
	}
	if wp.right == "" {
		if tuiWidth <= sidebarW+2 {
			return // window too narrow for a split; sidebar-only
		}
		out, err := tmuxRun("split-window", "-h", "-d", "-t", wp.left, "-P", "-F", "#{pane_id}", placeholderCmd(welcomeText))
		if err != nil {
			return
		}
		wp.right = strings.TrimSpace(out)
		// keep dead attach clients visible until the next retarget respawns
		_, _ = tmuxRun("set-option", "-p", "-t", wp.right, "remain-on-exit", "on")
		_, _ = tmuxRun("select-pane", "-t", wp.left, "-T", "muster")
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", "agent")
		wp.lastTarget = "none"
	}
	if tuiWidth != sidebarW && paneExists(wp.right) {
		_, _ = tmuxRun("resize-pane", "-t", wp.left, "-x", strconv.Itoa(sidebarW))
	}
}

// retarget points the right pane at the selected agent. sess is either a
// real local agent's tmux session OR — for a remote row already being
// followed via a persistent mesh proxy (see meshProxySession) — the
// proxy's session name; both are ordinary tmux sessions from here, so they
// share the exact same cheap switch-client path. A remote row with no
// proxy YET (nobody has settled on it long enough — see tuiModel.retarget
// and followMeshProxy) gets a lightweight placeholder instead; creating
// the proxy is deliberately not this function's job, so mere selection
// (scrolling past rows) never spawns/kills anything.
func (wp *workspacePanes) retarget(name, sess, state string) {
	if wp.right == "" {
		return
	}
	target := "none"
	switch {
	case name == "":
	case state == "dead":
		target = "dead:" + name
	case sess == "":
		target = "remote:" + name // not yet followed — no proxy exists
	default:
		target = "live:" + sess
	}
	if target == wp.lastTarget {
		return
	}
	switch {
	case target == "none":
		_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, placeholderCmd(welcomeText))
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", "agent")
	case strings.HasPrefix(target, "remote:"):
		wp.showRemotePlaceholder(name, "agent '"+name+"' lives on the ppz mesh only.\n\nsettling in — you'll be following it live in a moment.\n(enter/l skips the wait)")
	case state == "dead":
		msg := "agent '" + name + "' is dead.\n\nenter or r in the sidebar resumes it with its EXACT original command\n(conversation and permissions included)."
		if sess == "" || strings.HasPrefix(sess, "mstr-mesh-") { // remote row: no local spec, resume doesn't apply
			msg = "agent '" + name + "' looks offline on the mesh.\n\nnothing to resume from here — it isn't a local agent."
		}
		_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, placeholderCmd(msg))
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", name+" (dead)")
	default:
		title := name
		if strings.HasPrefix(sess, "mstr-mesh-") {
			title += " (mesh)"
		} else {
			// agents spawned by older musters still have a status bar —
			// silence it so the embedded view stays clean (idempotent);
			// a mesh proxy never has one to silence, harmless no-op either way
			_, _ = tmuxRun("set-option", "-t", "="+sess+":", "status", "off")
		}
		// same server, nested client: switch it when alive, respawn otherwise
		cur, _ := tmuxRun("display-message", "-p", "-t", wp.right, "#{pane_current_command}")
		if cur == "tmux" {
			tty, err := tmuxRun("display-message", "-p", "-t", wp.right, "#{pane_tty}")
			if err == nil {
				if _, err := tmuxRun("switch-client", "-c", tty, "-t", "="+sess); err == nil {
					_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", title)
					wp.lastTarget = target
					return
				}
			}
		}
		_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, attachCmd(sess))
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", title)
	}
	wp.lastTarget = target
}

// showRemotePlaceholder renders msg in the right pane for a mesh-only
// agent not yet (or no longer) followed via a proxy.
func (wp *workspacePanes) showRemotePlaceholder(name, msg string) {
	_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, placeholderCmd(msg))
	_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", name+" (mesh)")
}

// showPending: a spawn was just decided; its pane doesn't exist yet.
func (wp *workspacePanes) showPending(name string) {
	if wp.right == "" {
		return
	}
	target := "pending:" + name
	if wp.lastTarget == target {
		return
	}
	_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right,
		placeholderCmd("hiring '"+name+"' — booting now.\n\nits terminal appears here the moment it's up."))
	_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", name+" (booting)")
	wp.lastTarget = target
}

// ---- mesh proxies: persistent background `ppz terminal attach` sessions --

// meshProxySession names the local tmux session that keeps a remote
// agent's attach connection alive in the background, so glancing away and
// back is a cheap switch-client (like any local agent) instead of a fresh
// connect + JetStream scrollback replay. Prefixed distinctly from
// mstr-<name> (real local agent sessions) so the two namespaces can never
// collide.
func meshProxySession(name string) string { return "mstr-mesh-" + name }

// meshProxyAttachCmd is the proxy session's own command: ppz terminal
// attach --embedded, so Ctrl-\ can never break the persistent connection —
// swallowed inside the pane, and never forwarded to the remote as SIGQUIT
// either. Only a genuine crash ever kills it, which reapMeshProxies'
// per-tick check self-heals.
func meshProxyAttachCmd(handle string) string {
	return "exec " + shQuote(ppzBin()) + " terminal attach " + shQuote(handle) + " --embedded"
}

// ensureMeshProxy creates the proxy session if it doesn't already exist.
// PPZ_SESSION is explicitly cleared rather than left to ambient
// inheritance — ppz terminal attach refuses to attach to its own caller's
// handle (the self-attach guard), and this proxy is muster's own session,
// never the target agent's, so it must never look like one.
func ensureMeshProxy(name string) error {
	sess := meshProxySession(name)
	if tmuxHasSession(sess) {
		return nil
	}
	if _, err := tmuxRun("new-session", "-d", "-s", sess, "-e", "PPZ_SESSION=", meshProxyAttachCmd(name)); err != nil {
		return err
	}
	// without this, a crashed attach process closes its pane — the
	// session's only one — destroying the whole session instead of
	// leaving a frozen pane for meshProxyPaneDead/respawnMeshProxy to
	// find and self-heal.
	_, err := tmuxRun("set-option", "-t", "="+sess+":", "remain-on-exit", "on")
	return err
}

// reapMeshProxy kills a proxy session outright — the agent went offline or
// this proxy aged out of the LRU cap.
func reapMeshProxy(name string) error {
	return tmuxKillSession(meshProxySession(name))
}

// meshProxyPaneDead reports whether a proxy's attach process has exited
// (a real crash — Ctrl-\ can't cause this with --embedded).
func meshProxyPaneDead(name string) bool {
	dead, _ := tmuxRun("display-message", "-p", "-t", "="+meshProxySession(name)+":", "#{pane_dead}")
	return dead == "1"
}

// respawnMeshProxy self-heals a crashed proxy in place — same session,
// fresh attach process (one acceptable scrollback replay).
func respawnMeshProxy(name string) {
	_, _ = tmuxRun("respawn-pane", "-k", "-t", "="+meshProxySession(name)+":", meshProxyAttachCmd(name))
}

// focus moves the user's cursor into the agent pane — from there they type
// straight into the agent, exactly as if attached.
func (wp *workspacePanes) focus() {
	if wp.right != "" {
		_, _ = tmuxRun("select-pane", "-t", wp.right)
	}
}

// pin opens an extra live pane for sess next to the main agent pane
// (dir: "right"|"down"|"left"). Pinned panes are plain tmux panes — the
// user closes them like any pane; they vanish when the agent dies.
func (wp *workspacePanes) pin(sess, dir string) {
	if wp.right == "" {
		return
	}
	args := []string{"split-window"}
	switch dir {
	case "right":
		args = append(args, "-h")
	case "down":
		args = append(args, "-v")
	case "left":
		args = append(args, "-h", "-b")
	default:
		return
	}
	args = append(args, "-d", "-t", wp.right, attachCmd(sess))
	_, _ = tmuxRun(args...)
}

// terminal opens a small shell strip under the agent pane, cwd = dir — for
// dev servers, migrations, quick git, without leaving the workspace.
func (wp *workspacePanes) terminal(dir string) {
	t := wp.right
	if t == "" {
		t = wp.left
	}
	_, _ = tmuxRun("split-window", "-v", "-l", "12", "-t", t, "-c", dir)
}

// showRoom fills the agent pane with the project's live room chat. The
// "room:" lastTarget keeps retarget() from clobbering it on the next tick;
// selecting an agent replaces it.
func (wp *workspacePanes) showRoom(proj string) {
	if wp.right == "" {
		return
	}
	target := "room:" + proj
	if wp.lastTarget == target {
		return
	}
	_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, shQuote(selfExe())+" room "+shQuote(proj)+" --watch")
	_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", "#"+proj)
	wp.lastTarget = target
}

// runSetup runs an interactive command (e.g. ppz login) in the agent pane,
// then hands the pane back to the selected agent. lastTarget is left alone
// so the 2s tick doesn't clobber the flow; changing selection still will.
func (wp *workspacePanes) runSetup(cmd, thenAttach string) {
	if wp.right == "" {
		return
	}
	full := cmd + `; echo; echo '  done.'; sleep 2`
	if thenAttach != "" {
		full += "; " + attachCmd(thenAttach)
	}
	_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, "sh -c "+shQuote(full))
	_, _ = tmuxRun("select-pane", "-t", wp.right)
}

// zoom toggles the agent pane fullscreen (C-b z restores).
func (wp *workspacePanes) zoom() {
	if wp.right != "" {
		_, _ = tmuxRun("resize-pane", "-Z", "-t", wp.right)
		_, _ = tmuxRun("select-pane", "-t", wp.right)
	}
}

// attachCmd is the right pane's command: a real tmux client, nested on the
// same server (TMUX unset so tmux allows it).
func attachCmd(sess string) string {
	cmd := "TMUX= exec " + shQuote(tmuxBin())
	if extra := os.Getenv("MUSTER_TMUX_ARGS"); extra != "" {
		cmd += " " + extra
	}
	return cmd + " attach -t " + shQuote("="+sess)
}

func placeholderCmd(msg string) string {
	var b strings.Builder
	b.WriteString("clear; echo")
	for _, line := range strings.Split(msg, "\n") {
		b.WriteString("; echo " + shQuote("  "+line))
	}
	b.WriteString("; exec tail -f /dev/null")
	return "sh -c " + shQuote(b.String())
}

const welcomeText = "no agent selected.\n\nthe sidebar spawns, resumes, and messages agents;\nclick this pane (or press enter) to type into one directly."

// quitWorkspace tears the whole session down; tmux moves each client to its
// previous session (or detaches it) automatically.
func quitWorkspace() {
	_ = tmuxKillSession(wsSession)
}

// leaveWorkspace sends every client home without killing the fleet view:
// back to their previous session when they have one, detached otherwise.
func leaveWorkspace() {
	out, err := tmuxRun("list-clients", "-t", "="+wsSession, "-F", "#{client_tty}")
	if err != nil {
		return
	}
	for _, tty := range strings.Split(out, "\n") {
		if tty = strings.TrimSpace(tty); tty == "" {
			continue
		}
		if _, err := tmuxRun("switch-client", "-l", "-c", tty); err != nil {
			_, _ = tmuxRun("detach-client", "-t", tty)
		}
	}
}
