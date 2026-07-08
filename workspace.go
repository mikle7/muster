package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	if tmuxHasSession(wsSession) && !workspaceAlive() {
		_ = tmuxKillSession(wsSession) // continuum-restored shell or crashed TUI
	}
	if !tmuxHasSession(wsSession) {
		args := []string{"new-session", "-d", "-s", wsSession, "-e", "MUSTER_EMBEDDED=1"}
		for _, k := range []string{"MUSTER_STATE_DIR", "MUSTER_TMUX_ARGS", "MUSTER_TMUX", "MUSTER_PPZ", "MUSTER_DEFAULT_CMD"} {
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
	}
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

// retarget points the right pane at the selected agent: live agents get the
// nested attach client switched (or respawned), dead ones a resume hint.
func (wp *workspacePanes) retarget(name, tmuxSess, state string) {
	if wp.right == "" {
		return
	}
	target := "none"
	switch {
	case name == "":
	case state == "dead":
		target = "dead:" + name
	default:
		target = "live:" + tmuxSess
	}
	if target == wp.lastTarget {
		return
	}
	switch {
	case target == "none":
		_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, placeholderCmd(welcomeText))
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", "agent")
	case state == "dead":
		msg := "agent '" + name + "' is dead.\n\nenter or r in the sidebar resumes it with its EXACT original command\n(conversation and permissions included)."
		_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, placeholderCmd(msg))
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", name+" (dead)")
	default:
		// agents spawned by older musters still have a status bar — silence
		// it so the embedded view stays clean (idempotent)
		_, _ = tmuxRun("set-option", "-t", "="+tmuxSess+":", "status", "off")
		// same server, nested client: switch it when alive, respawn otherwise
		cur, _ := tmuxRun("display-message", "-p", "-t", wp.right, "#{pane_current_command}")
		if cur == "tmux" {
			tty, err := tmuxRun("display-message", "-p", "-t", wp.right, "#{pane_tty}")
			if err == nil {
				if _, err := tmuxRun("switch-client", "-c", tty, "-t", "="+tmuxSess); err == nil {
					_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", name)
					break
				}
			}
		}
		_, _ = tmuxRun("respawn-pane", "-k", "-t", wp.right, attachCmd(tmuxSess))
		_, _ = tmuxRun("select-pane", "-t", wp.right, "-T", name)
	}
	wp.lastTarget = target
}

// focus moves the user's cursor into the agent pane — from there they type
// straight into the agent, exactly as if attached.
func (wp *workspacePanes) focus() {
	if wp.right != "" {
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
