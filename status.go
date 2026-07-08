package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	st := AgentStatus{
		State: state, Event: payload.HookEventName, Reason: reason,
		SessionID: payload.SessionID, TS: time.Now(),
	}
	if err := os.MkdirAll(statusDir(), 0o755); err != nil {
		return 0
	}
	b, _ := json.Marshal(st)
	_ = atomicWrite(statusPath(payload.SessionID), b)
	return 0
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
			return st.State, st.Reason
		}
	}
	if s.PpzHandle != "" {
		if h, ok := hb[s.PpzHandle]; ok && h.State != "" {
			return h.State, "heartbeat"
		}
	}
	return "unknown", ""
}

func stateGlyph(state string) string {
	switch state {
	case "working":
		return "⚙"
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
