package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// AgentEvent is the generic structured event any external client (voice
// service, mobile app, Home Assistant, Slack, ...) can subscribe to over the
// shared "muster-events" ppz pipe. Shape matches the Event Model in
// mikle7/muster-voice's DESIGN.md: agent.notification (priority+message),
// agent.question (question), agent.progress (status). One struct covers all
// three — the unused fields per type are omitempty.
//
// This is Phase 1's entire "Muster event API" bullet: publish structured
// events on top of the existing ppz mesh transport, nothing more. No
// hardware/voice/TTS/STT/provider code belongs here — that's muster-voice's
// job as an external client of this stream.
type AgentEvent struct {
	Type     string    `json:"type"` // agent.notification | agent.question | agent.progress
	Agent    string    `json:"agent"`
	Priority string    `json:"priority,omitempty"` // notification only: low|normal|high|critical
	Message  string    `json:"message,omitempty"`  // notification
	Question string    `json:"question,omitempty"` // question
	Status   string    `json:"status,omitempty"`   // progress
	TS       time.Time `json:"ts"`
}

// eventsPipe is the shared uncollared pipe every external client subscribes
// to (`ppz subs add muster-events`) or replays (`ppz reread muster-events
// --json --since 1h`) — same idiom as roomPipe, just global instead of
// per-project: there's one event stream for the whole fleet.
const eventsPipe = "muster-events"

// ensureEventsPipe creates the pipe (idempotent, tolerate already-exists —
// same idiom as ensureRoomPipe).
func ensureEventsPipe() error {
	if err := ensureCtlHandle(); err != nil {
		return err
	}
	if out, err := ppzOut(ctlSession, "pipe", "create", eventsPipe); err != nil {
		if !strings.Contains(string(out), "E_PIPE_TAKEN") && !strings.Contains(string(out), "already exists") {
			return errf("ppz pipe create %s: %s", eventsPipe, out)
		}
	}
	return nil
}

// publishEvent emits ev on the shared event-stream pipe.
func publishEvent(ev AgentEvent) error {
	if err := ensureEventsPipe(); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return ppzSend(eventsPipe, string(b))
}

// resolveAgentName maps a claude session uuid to its muster agent name:
// MUSTER_AGENT (set in every muster pane's env) first, falling back to a
// spec scan for callers running outside that pane (e.g. a relay).
func resolveAgentName(sessionUUID string) string {
	if name := os.Getenv("MUSTER_AGENT"); name != "" {
		return name
	}
	if specs, _ := listSpecs(); specs != nil {
		for _, s := range specs {
			if s.SessionUUID == sessionUUID {
				return s.Name
			}
		}
	}
	return ""
}

// eventForTransition maps a hook-derived state CHANGE to the generic
// event-stream shape. Fires only on genuine transitions (prev.State !=
// st.State) — PreToolUse/PostToolUse etc. re-report "working" on every tool
// call, and publishing each one would flood external clients (a voice
// service reading this stream doesn't want a spoken update per tool call).
// Returns nil for a same-state re-report or a state this stream doesn't
// cover ("stalled"/"dead"/"error" are read-time-only derivations in
// liveState, never written here — see status.go).
func eventForTransition(agent string, prev *AgentStatus, st AgentStatus) *AgentEvent {
	if prev != nil && prev.State == st.State {
		return nil
	}
	ev := AgentEvent{Agent: agent, TS: st.TS}
	switch st.State {
	case "working":
		ev.Type, ev.Status = "agent.progress", "working"
	case "blocked":
		ev.Type, ev.Question = "agent.question", st.Reason
		if ev.Question == "" || ev.Question == "permission" {
			ev.Question = "needs your input"
		}
	case "idle":
		ev.Type, ev.Message, ev.Priority = "agent.notification", "task complete", "normal"
	case "ended":
		ev.Type, ev.Message, ev.Priority = "agent.notification", "session ended", "low"
	default:
		return nil
	}
	return &ev
}
