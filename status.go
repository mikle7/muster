package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AgentStatus is written by `muster hook` (Claude Code hooks) keyed by the
// claude session uuid, and merged with tmux liveness + ppz heartbeats at
// read time.
type AgentStatus struct {
	State     string    `json:"state"` // working|blocked|idle|error|ended
	Event     string    `json:"event"` // hook event that produced it
	Reason    string    `json:"reason,omitempty"`
	SessionID string    `json:"session_id"`
	TS        time.Time `json:"ts"`
	Tool      string    `json:"tool,omitempty"`   // PreToolUse/PostToolUse only
	Target    string    `json:"target,omitempty"` // best-effort, see toolTarget
}

func statusPath(sessionUUID string) string {
	return filepath.Join(statusDir(), sessionUUID+".json")
}

// ---- event history ----------------------------------------------------------
// Every hook event is also appended to <uuid>.events.jsonl — the raw material
// for `muster recap` (re-orientation is the #2 cost of running a fleet: what
// was this agent doing while I looked away?). Latest-status alone can't answer
// that.

func eventsPath(sessionUUID string) string {
	return filepath.Join(statusDir(), sessionUUID+".events.jsonl")
}

const maxEventsBytes = 128 << 10 // trim threshold
const keepEvents = 200           // lines kept after a trim

func appendEvent(st AgentStatus) {
	p := eventsPath(st.SessionID)
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxEventsBytes {
		trimEvents(p)
	}
}

func trimEvents(p string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := splitLines(string(b))
	if len(lines) > keepEvents {
		lines = lines[len(lines)-keepEvents:]
	}
	_ = atomicWrite(p, []byte(joinLines(lines)))
}

// loadEvents returns the last n events, oldest first.
func loadEvents(sessionUUID string, n int) []AgentStatus {
	b, err := os.ReadFile(eventsPath(sessionUUID))
	if err != nil {
		return nil
	}
	lines := splitLines(string(b))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	var out []AgentStatus
	for _, l := range lines {
		var st AgentStatus
		if json.Unmarshal([]byte(l), &st) == nil {
			out = append(out, st)
		}
	}
	return out
}

func clearEvents(sessionUUID string) { _ = os.Remove(eventsPath(sessionUUID)) }

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// AgentUsage is written by `muster hook status-line` (Claude Code statusLine
// command) — model + context% per session, plus the account-wide 5h window.
// Separate file from AgentStatus so the two writers never race.
type AgentUsage struct {
	Model       string    `json:"model,omitempty"` // display name ("Opus")
	CtxPct      float64   `json:"ctx_pct"`
	UsedTokens  int64     `json:"used_tokens,omitempty"` // absolute context tokens in use (0 = CC didn't report)
	FiveHrPct   float64   `json:"five_hr_pct"`           // 0 = unknown (API-key users)
	FiveHrReset time.Time `json:"five_hr_reset,omitempty"`
	TS          time.Time `json:"ts"`
}

func usagePath(sessionUUID string) string {
	return filepath.Join(statusDir(), sessionUUID+".usage.json")
}

func loadUsage(sessionUUID string) *AgentUsage {
	b, err := os.ReadFile(usagePath(sessionUUID))
	if err != nil {
		return nil
	}
	var u AgentUsage
	if json.Unmarshal(b, &u) != nil {
		return nil
	}
	return &u
}

// cmdStatusLine handles the statusLine invocations: persist usage for the
// muster UI, and print the line claude displays inside the agent pane.
// Like cmdHook, it must never fail loudly.
func cmdStatusLine() int {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 0
	}
	var p struct {
		SessionID string `json:"session_id"`
		Model     struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"model"`
		ContextWindow struct {
			UsedPercentage float64 `json:"used_percentage"`
			// absolute context tokens in use (input incl. cache reads/writes —
			// the numerator behind used_percentage). Lets the refresh trigger
			// key off real size, not a % that means 150k on a 200k window but
			// 520k on a 1M one. Reads 0 (not missing) before the first API
			// response, and the % trigger carries the load then (refresh.go).
			// CAVEAT: this means CURRENT context tokens only on Claude Code
			// >= v2.1.132; BEFORE that it was CUMULATIVE session totals, so on
			// pre-2.1.132 CC the token ceiling would fire off lifetime usage
			// and over-refresh. We run current CC; the %-fallback is the
			// backstop if an old CC ever reports a cumulative number here.
			TotalInputTokens int64 `json:"total_input_tokens"`
		} `json:"context_window"`
		RateLimits struct {
			FiveHour struct {
				UsedPercentage float64 `json:"used_percentage"`
				ResetsAt       int64   `json:"resets_at"`
			} `json:"five_hour"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		return 0
	}
	model := p.Model.DisplayName
	if model == "" {
		model = p.Model.ID
	}
	u := AgentUsage{
		Model:      model,
		CtxPct:     p.ContextWindow.UsedPercentage,
		UsedTokens: p.ContextWindow.TotalInputTokens,
		FiveHrPct:  p.RateLimits.FiveHour.UsedPercentage,
		TS:         time.Now(),
	}
	if p.RateLimits.FiveHour.ResetsAt > 0 {
		u.FiveHrReset = time.Unix(p.RateLimits.FiveHour.ResetsAt, 0)
	}
	if err := os.MkdirAll(statusDir(), 0o755); err == nil {
		b, _ := json.Marshal(u)
		_ = atomicWrite(usagePath(p.SessionID), b)
	}
	// the line claude renders at the bottom of the agent pane
	line := model
	if name := os.Getenv("MUSTER_AGENT"); name != "" {
		line = "\033[35m" + name + "\033[0m · " + line
	}
	line += fmt.Sprintf(" · ctx %d%%", int(u.CtxPct))
	if u.FiveHrPct > 0 {
		line += fmt.Sprintf(" · 5h %d%%", int(u.FiveHrPct))
		if !u.FiveHrReset.IsZero() {
			line += " → " + u.FiveHrReset.Local().Format("15:04")
		}
	}
	fmt.Println(line)
	return 0
}

func loadStatus(sessionUUID string) *AgentStatus {
	b, err := os.ReadFile(statusPath(sessionUUID))
	if err != nil {
		return nil
	}
	var st AgentStatus
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}

func clearStatus(sessionUUID string) { _ = os.Remove(statusPath(sessionUUID)) }

// hookEventState maps Claude Code hook events to muster states.
// Unknown events return "" (ignored) so new hook types never break us.
func hookEventState(event, notifMessage, source string) (state, reason string) {
	switch event {
	case "SessionStart":
		// a fresh boot (source=startup) is a claude sitting at its input
		// prompt — idle, not working. Warm spares depend on this: dispatch
		// only claims IDLE spares, and a spare's only hook event until its
		// first task is this one. clear/resume keep reporting working — they
		// happen mid-flow (refresh/retask kick a prompt in right after).
		if source == "startup" {
			return "idle", ""
		}
		return "working", ""
	case "PreToolUse", "PostToolUse", "UserPromptSubmit":
		return "working", ""
	case "PermissionRequest":
		return "blocked", "permission"
	case "Notification":
		// idle_prompt fires ~60s after Stop just because the user hasn't
		// replied yet — not stuck. Only permission_prompt/agent_needs_input
		// (any other Notification text) are a genuine block (#2).
		if strings.Contains(notifMessage, "waiting for your input") {
			return "idle", ""
		}
		return "blocked", notifMessage
	case "Stop", "SubagentStop":
		return "idle", ""
	case "SessionEnd":
		return "ended", ""
	}
	return "", ""
}

// toolTarget extracts a best-effort "what is this tool acting on" string
// from a PreToolUse/PostToolUse hook's tool_input — one known field per
// tool, matching Claude Code's actual tool_input shapes. Returns "" for an
// unrecognized tool or unparseable input rather than guessing wrong; a
// narration consumer can fall back to just the tool name in that case.
func toolTarget(toolName string, toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return ""
	}
	var in struct {
		FilePath    string `json:"file_path"`
		Command     string `json:"command"`
		Pattern     string `json:"pattern"`
		URL         string `json:"url"`
		Query       string `json:"query"`
		Description string `json:"description"`
	}
	if json.Unmarshal(toolInput, &in) != nil {
		return ""
	}
	switch toolName {
	case "Read", "Edit", "Write", "NotebookEdit":
		return in.FilePath
	case "Bash":
		return in.Command
	case "Grep", "Glob":
		return in.Pattern
	case "WebFetch":
		return in.URL
	case "WebSearch":
		return in.Query
	case "Task":
		return in.Description
	default:
		return ""
	}
}

// cmdHook is the hook sink: reads the hook JSON from stdin, writes the
// status file. Registered for every event in the muster hooks settings.
// Must never fail loudly — a broken status write must not break the agent.
func cmdHook(args []string) int {
	if len(args) > 0 && args[0] == "status-line" {
		return cmdStatusLine()
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 0
	}
	var payload struct {
		HookEventName string          `json:"hook_event_name"`
		SessionID     string          `json:"session_id"`
		Source        string          `json:"source"` // SessionStart: startup|resume|clear|compact
		Message       string          `json:"message"`
		ToolName      string          `json:"tool_name"`  // PreToolUse/PostToolUse only
		ToolInput     json.RawMessage `json:"tool_input"` // shape varies per tool, see toolTarget
	}
	if json.Unmarshal(raw, &payload) != nil || payload.SessionID == "" {
		return 0
	}
	// /clear ROTATES the session id (SessionStart announces the new one).
	// Without adoption the agent keeps running but muster reads the old uuid
	// forever: status/ctx freeze into a false "stalled", and resume targets
	// the pre-clear snapshot. MUSTER_AGENT is in every muster pane's env, so
	// the sink can re-point the spec before any state is written.
	if payload.HookEventName == "SessionStart" {
		adoptRotatedSession(os.Getenv("MUSTER_AGENT"), payload.SessionID)
	}
	state, reason := hookEventState(payload.HookEventName, payload.Message, payload.Source)
	if state == "" {
		return 0
	}
	prev := loadStatus(payload.SessionID)
	st := AgentStatus{
		State: state, Event: payload.HookEventName, Reason: reason,
		SessionID: payload.SessionID, TS: time.Now(),
		Tool: payload.ToolName, Target: toolTarget(payload.ToolName, payload.ToolInput),
	}
	if err := os.MkdirAll(statusDir(), 0o755); err != nil {
		return 0
	}
	b, _ := json.Marshal(st)
	_ = atomicWrite(statusPath(payload.SessionID), b)
	appendEvent(st)
	// desktop nudge on the transition INTO blocked — you're often not
	// looking at the workspace when an agent stalls on a permission
	if state == "blocked" && (prev == nil || prev.State != "blocked") {
		notifyBlocked(payload.SessionID, reason)
	}
	// generic event-stream publish (muster-events pipe) — any external
	// client (voice service, mobile, Home Assistant...) subscribed there
	// hears about this transition. Best-effort: a mesh hiccup must never
	// break the agent it's reporting on.
	if ev := eventForTransition(resolveAgentName(payload.SessionID), prev, st); ev != nil {
		_ = publishEvent(*ev)
	}
	// a fresh post-/clear context gets seeded — stdout from a SessionStart
	// hook is injected as context (docs: hookSpecificOutput.
	// additionalContext). Refresh = same task: handoff notes. Retask = NEW
	// task: primer, and pointedly NOT the old handoff (rotated aside by
	// cmdRetask). Manual (a human typed /clear, no marker) = NEW task:
	// primer, old handoff parked as .prev. Every mode re-briefs identity.
	// source=="compact" is Claude Code's own auto-compaction (context filled)
	// — same SessionStart injection channel, treated as a refresh (SAME task):
	// the transcript was just summarized, so re-inject the durable muster
	// context that summary may have dropped. Without this, a compacted agent —
	// especially a RESUMED one, which never got the --append-system-prompt
	// briefing (firstLaunch gate, spec.go) — silently loses its identity/
	// handoff. muster already received this event and ignored it.
	if payload.HookEventName == "SessionStart" && (payload.Source == "clear" || payload.Source == "compact") {
		emitClearContext(os.Getenv("MUSTER_AGENT"), payload.Source)
	}
	return 0
}

// adoptRotatedSession re-points agent's spec at a new session uuid, migrating
// the event history (recap must survive a clear) and dropping stale status/
// usage files. No-op when the id is unchanged or the agent is unknown. Argv
// is untouched — faithful resume stays sacred; only the resume TARGET moves,
// which is exactly what makes resume faithful after a rotation.
func adoptRotatedSession(agent, newID string) {
	if agent == "" || newID == "" {
		return
	}
	s, err := loadSpec(agent)
	if err != nil || s.SessionUUID == newID {
		return
	}
	old := s.SessionUUID
	s.SessionUUID = newID
	if saveSpec(s) != nil {
		return
	}
	if old != "" {
		migrateEvents(old, newID)
		clearStatus(old)
		_ = os.Remove(usagePath(old)) // ctx% restarts at unknown, honest
	}
}

// migrateEvents moves old's event history under the new uuid (prepended —
// the new session's own events may already exist).
func migrateEvents(old, newID string) {
	ob, err := os.ReadFile(eventsPath(old))
	if err != nil || len(ob) == 0 {
		_ = os.Remove(eventsPath(old))
		return
	}
	nb, _ := os.ReadFile(eventsPath(newID))
	if atomicWrite(eventsPath(newID), append(ob, nb...)) == nil {
		_ = os.Remove(eventsPath(old))
	}
}

// emitClearContext prints SessionStart hook JSON injecting seed context
// into the just-cleared window. Docs cap additionalContext at 10k chars.
// source is the SessionStart source: "clear" (a /clear — classify by marker)
// or "compact" (Claude's own auto-compaction — always SAME task, never
// consume a retask/refresh marker meant for a real /clear).
func emitClearContext(agent, source string) {
	if b := clearHookJSON(agent, clearModeForSource(agent, source)); b != nil {
		fmt.Println(string(b))
	}
}

// clearModeForSource picks the injection mode for a SessionStart source.
// "compact" is Claude's own auto-compaction — always SAME task, and it must
// NOT run clearMode (that consumes a one-shot retask marker set aside for a
// real /clear; a compaction firing first would eat it and the next actual
// retask would wake up thinking it's just a refresh).
func clearModeForSource(agent, source string) string {
	if source == "compact" {
		return "compact"
	}
	return clearMode(agent)
}

// clearMode classifies a /clear by who initiated it: a retask mark
// (consumed — one-shot) means muster retask (NEW task), a live refresh mark
// means muster context refresh (SAME task), and neither means a human typed
// /clear themselves — treated as a fresh start for a new task (Michael's
// call 2026-07-17: muster-guided clears keep their meaning; a manual clear
// means "I'm starting something new", matching plain claude-code muscle
// memory).
func clearMode(agent string) string {
	switch {
	case consumeRetaskMark(agent):
		return "retask"
	case refreshInFlight(agent):
		return "refresh"
	}
	return "manual"
}

// clearHookJSON builds the post-/clear injection for agent. Every mode
// re-briefs identity first: the spawn-time --append-system-prompt flag only
// exists on FIRST-launch processes (injected() skips it on resume — see the
// firstLaunch gate in spec.go), so on any resumed agent a /clear would
// otherwise be amnesia. Recomputing here also means the roster is current,
// not the spawn-day snapshot. Per-part caps are budgeted to fit the 10k
// additionalContext limit: identity 2500 + primer 4000/handoff 4500 +
// lessons 1500 + prose.
func clearHookJSON(agent, mode string) []byte {
	if agent == "" {
		return nil
	}
	var s *AgentSpec
	if sp, err := loadSpec(agent); err == nil {
		s = sp
	}
	proj, identity, primer := "", "", ""
	if s != nil {
		proj = projectFor(loadProjects(), lsRow{Dir: s.Dir, Repo: s.Repo})
		identity = capHead(identityPrompt(s), 2500)
		primer = capHead(readPrimer(s), 4000)
	}
	lessons := readLessons(proj, 1500)

	var ctx string
	switch mode {
	case "retask":
		// NEW task: the old handoff is rotated aside by cmdRetask and must
		// not leak in.
		ctx = "Your context was cleared for a NEW task (muster retask). You are the same agent — " +
			"but the previous task is over; do not resume it. Your next message is the new task."
	case "refresh":
		ctx = "Your context was just cleared (muster context refresh). You are the same agent on the " +
			"SAME task — your handoff notes below are the thread; continue from them."
	case "compact":
		// Claude auto-compacted the transcript (context filled). SAME task: its
		// summary is live; re-inject the durable context that summary may have
		// dropped. Handoff notes injected below, same as refresh.
		ctx = "Your conversation was just auto-compacted by Claude Code (context filled up). You are the " +
			"same agent on the SAME task — Claude's summary is in play; your durable muster context is " +
			"re-injected below so nothing important was silently dropped. Continue the task."
	default:
		// manual: a human typed /clear — fresh start, old handoff parked
		// (kept, never destroyed) so it can't drag the new task backwards.
		ctx = "Your context was cleared (a manual /clear) — muster treats this as a fresh start " +
			"for a NEW task. You are the same agent."
		hp := handoffPath(agent)
		if _, err := os.Stat(hp); err == nil && os.Rename(hp, hp+".prev") == nil {
			ctx += " Your previous handoff notes are parked at " + hp + ".prev — read them only if " +
				"you need continuity."
		}
		if s != nil && s.PpzHandle != "" {
			ctx += " Earlier messages are retained in your inbox — 'ppz subs read' to catch up."
		}
	}
	if identity != "" {
		ctx += "\n\nYOUR BRIEFING (re-injected — the roster is current):\n" + identity
	}
	// refresh + compact are SAME-task: the handoff notes are the thread.
	// retask + manual are NEW-task: seed the project primer instead.
	sameTask := mode == "refresh" || mode == "compact"
	if sameTask {
		const capChars = 4500
		b, _ := os.ReadFile(handoffPath(agent))
		notes := strings.TrimSpace(string(b))
		if notes == "" {
			notes = "(handoff file is empty — reconstruct from the repo: git log, git diff, docs, your inbox.)"
		}
		if len(notes) > capChars {
			notes = "…" + notes[len(notes)-capChars:]
		}
		ctx += "\n\nYOUR HANDOFF NOTES (your durable thread):\n" + notes
	} else if primer != "" {
		// retask + manual seed the project, not the old task
		ctx += "\n\nPROJECT PRIMER:\n" + primer
	}
	if lessons != "" {
		ctx += "\n\nLESSONS LEARNED here by the team:\n" + lessons
	}
	if sameTask {
		ctx += "\n\nContinue from your notes and keep the handoff file updated as you work."
	} else {
		ctx += "\n\nStart a fresh handoff file as you work."
	}
	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "SessionStart",
			"additionalContext": ctx,
		},
	})
	if err != nil {
		return nil
	}
	return out
}

// notify posts a desktop notification (macOS; MUSTER_NOTIFY=0 disables) —
// you're rarely staring at the workspace when something needs you.
func notify(title, body string) {
	if os.Getenv("MUSTER_NOTIFY") == "0" || runtime.GOOS != "darwin" {
		return
	}
	script := fmt.Sprintf("display notification %q with title %q", body, title)
	_ = exec.Command("osascript", "-e", script).Start()
}

// notifyBlocked posts a macOS notification naming the blocked agent.
// MUSTER_NOTIFY=0 disables. Best-effort by design.
func notifyBlocked(sessionUUID, reason string) {
	name := resolveAgentName(sessionUUID)
	if name == "" {
		name = "an agent"
	}
	if reason == "" {
		reason = "waiting for input"
	}
	notify("muster: "+name+" needs you", reason)
}

// hooksSettingsPath is the settings file passed to claude via --settings.
// Missing file (init not run) = no hooks = status shows "-"; still functional.
func hooksSettingsPath() string {
	return filepath.Join(dataDir(), "hooks-settings.json")
}

func hooksSettingsIfPresent() string {
	p := hooksSettingsPath()
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func writeHooksSettings() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	hook := []map[string]any{{
		"hooks": []map[string]any{{"type": "command", "command": shQuote(exe) + " hook", "timeout": 5}},
	}}
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart":      hook,
			"UserPromptSubmit":  hook,
			"PreToolUse":        hook,
			"PostToolUse":       hook,
			"PermissionRequest": hook,
			"Notification":      hook,
			"Stop":              hook,
			"SessionEnd":        hook,
		},
		// feeds model/context%/5h-window to the muster UI and renders the
		// agent's own status line
		"statusLine": map[string]any{
			"type": "command", "command": shQuote(exe) + " hook status-line", "padding": 0,
		},
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return "", err
	}
	p := hooksSettingsPath()
	if err := atomicWrite(p, b); err != nil {
		return "", err
	}
	return p, nil
}

// liveState merges all signals for one agent. Precedence:
// tmux dead > hook status (post-launch only) > ppz heartbeat > unknown.
func liveState(s *AgentSpec, hb map[string]ppzHeartbeat) (state, reason string) {
	if !tmuxHasSession(s.TmuxSession) {
		return "dead", ""
	}
	launched := s.CreatedAt
	if s.ResumedAt.After(launched) {
		launched = s.ResumedAt
	}
	if s.SessionUUID != "" {
		if st := loadStatus(s.SessionUUID); st != nil && st.TS.After(launched) {
			state, reason := applyStall(st.State, st.Reason, st.TS, stallAfter(), time.Now())
			h, ok := hb[s.PpzHandle]
			return correctStickyBlock(state, reason, st.TS, h, ok && s.PpzHandle != "")
		}
	}
	if s.PpzHandle != "" {
		if h, ok := hb[s.PpzHandle]; ok && h.State != "" {
			return h.State, "heartbeat"
		}
	}
	return "unknown", ""
}

// correctStickyBlock lets a fresh "working" heartbeat override a stale
// hook-derived "blocked". The Notification hook that sets "blocked" has no
// reliable unblock counterpart, so it can outlive the block; a heartbeat
// reporting "working" NEWER than that hook event is positive proof the agent
// resumed (working is definitionally incompatible with waiting-for-input).
// Deliberately only "working", not "idle": idle is ambiguous (an agent
// sitting at a blocked prompt reads idle too), so it must not clear a real
// block. Pure for testability.
func correctStickyBlock(state, reason string, hookTS time.Time, hb ppzHeartbeat, hbOK bool) (string, string) {
	if state == "blocked" && hbOK && hb.State == "working" && hb.TS.After(hookTS) {
		return "working", "heartbeat"
	}
	return state, reason
}

// applyStall derives "stalled" from a working status that hasn't produced a
// hook event in `after` (0 disables). Spinners lie — the absence of events is
// the honest "is anything still happening?" signal. Pure for testability.
func applyStall(state, reason string, ts time.Time, after time.Duration, now time.Time) (string, string) {
	if state == "working" && after > 0 && now.Sub(ts) > after {
		return "stalled", "no events for " + fmtAge(ts)
	}
	return state, reason
}

func stateGlyph(state string) string {
	switch state {
	case "working":
		return "⚙"
	case "stalled":
		return "⌛"
	case "blocked":
		return "✋"
	case "idle":
		return "✔"
	case "error":
		return "✖"
	case "pending":
		return "⋯"
	case "dead", "ended":
		return "☠"
	}
	return "?"
}

func fmtAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
