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
	FiveHrPct   float64   `json:"five_hr_pct"` // 0 = unknown (API-key users)
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
		Model:     model,
		CtxPct:    p.ContextWindow.UsedPercentage,
		FiveHrPct: p.RateLimits.FiveHour.UsedPercentage,
		TS:        time.Now(),
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
func hookEventState(event, notifMessage string) (state, reason string) {
	switch event {
	case "PreToolUse", "PostToolUse", "UserPromptSubmit", "SessionStart":
		return "working", ""
	case "PermissionRequest":
		return "blocked", "permission"
	case "Notification":
		return "blocked", notifMessage
	case "Stop", "SubagentStop":
		return "idle", ""
	case "SessionEnd":
		return "ended", ""
	}
	return "", ""
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
		HookEventName string `json:"hook_event_name"`
		SessionID     string `json:"session_id"`
		Message       string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.SessionID == "" {
		return 0
	}
	state, reason := hookEventState(payload.HookEventName, payload.Message)
	if state == "" {
		return 0
	}
	prev := loadStatus(payload.SessionID)
	st := AgentStatus{
		State: state, Event: payload.HookEventName, Reason: reason,
		SessionID: payload.SessionID, TS: time.Now(),
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
	return 0
}

// notifyBlocked posts a macOS notification naming the blocked agent.
// MUSTER_NOTIFY=0 disables. Best-effort by design.
func notifyBlocked(sessionUUID, reason string) {
	if os.Getenv("MUSTER_NOTIFY") == "0" || runtime.GOOS != "darwin" {
		return
	}
	name := os.Getenv("MUSTER_AGENT") // set in every muster tmux session
	if name == "" {
		if specs, _ := listSpecs(); specs != nil {
			for _, s := range specs {
				if s.SessionUUID == sessionUUID {
					name = s.Name
					break
				}
			}
		}
	}
	if name == "" {
		name = "an agent"
	}
	if reason == "" {
		reason = "waiting for input"
	}
	script := fmt.Sprintf("display notification %q with title %q", reason, "muster: "+name+" needs you")
	_ = exec.Command("osascript", "-e", script).Start()
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
			return applyStall(st.State, st.Reason, st.TS, stallAfter(), time.Now())
		}
	}
	if s.PpzHandle != "" {
		if h, ok := hb[s.PpzHandle]; ok && h.State != "" {
			return h.State, "heartbeat"
		}
	}
	return "unknown", ""
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
