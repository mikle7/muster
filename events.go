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
	Tool     string    `json:"tool,omitempty"`     // progress only, best-effort (see toolTarget)
	Target   string    `json:"target,omitempty"`   // progress only, best-effort (see toolTarget)
	TS       time.Time `json:"ts"`
}

// eventsPipe is the shared uncollared pipe every external client subscribes
// to (`ppz subs add muster-events`) or replays (`ppz reread muster-events
// --json --since 1h`) — same idiom as roomPipe, just global instead of
// per-project: there's one event stream for the whole fleet.
const eventsPipe = "muster-events"

// ensureCtlHandleFast/ensureEventsPipe/publishEvent deliberately do NOT
// reuse ensureCtlHandle/ppzSend (ppz.go) — those are shared by room chat,
// standup, and review-request sends, which should keep the general 10s
// ppzTimeout(). This path runs inside cmdHook, synchronously, on every
// qualifying tool call fleet-wide (see eventPublishTimeout's docstring in
// ppz.go) — it needs its own short timeout without changing anyone else's.

// ensureCtlHandleFast is ensureCtlHandle (ppz.go), bounded to
// eventPublishTimeout instead of the general ppzTimeout().
func ensureCtlHandleFast() error {
	out, err := ppzRunTimeout(ctlSession, false, eventPublishTimeout, "get", "handle")
	if err == nil && strings.TrimSpace(string(out)) == ctlHandle {
		return nil
	}
	if out, err := ppzOutFast(ctlSession, "source", "create", ctlHandle); err != nil {
		if !strings.Contains(string(out), "E_SOURCE_TAKEN") && !strings.Contains(string(out), "E_NAME_TAKEN") {
			return errf("ppz source create %s: %s (%v)", ctlHandle, out, err)
		}
		if out, err := ppzOutFast(ctlSession, "set", "handle", ctlHandle); err != nil {
			return errf("ppz set handle: %s (%v)", out, err)
		}
	}
	return nil
}

// ensureEventsPipe creates the pipe (idempotent, tolerate already-exists —
// same idiom as ensureRoomPipe).
func ensureEventsPipe() error {
	if err := ensureCtlHandleFast(); err != nil {
		return err
	}
	if out, err := ppzOutFast(ctlSession, "pipe", "create", eventsPipe); err != nil {
		if !strings.Contains(string(out), "E_PIPE_TAKEN") && !strings.Contains(string(out), "already exists") {
			return errf("ppz pipe create %s: %s", eventsPipe, out)
		}
	}
	return nil
}

// publishEvent emits ev on the shared event-stream pipe. Best-effort by
// design (see eventPublishTimeout) — a mesh hiccup drops this one event,
// never stalls the tool call cmdHook is reporting on.
func publishEvent(ev AgentEvent) error {
	if err := ensureEventsPipe(); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if out, err := ppzOutFast(ctlSession, "send", eventsPipe, string(b)); err != nil {
		return errf("ppz send: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
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
// event-stream shape. Fires on a genuine state transition (prev.State !=
// st.State), OR — while staying "working" — on a tool/target change, so a
// DEEP-turn narration consumer hears "reading auth.ts... running tests..."
// instead of going stale after the first tool call. PreToolUse/PostToolUse
// for the SAME call still collapse into one event (tool+target unchanged
// between them). Publishing on every hook event regardless would flood
// external clients (a voice service reading this stream doesn't want a
// spoken update per tool call) — this is the narrower "did the thing worth
// narrating actually change" gate instead.
// Returns nil for a same-state/same-tool re-report or a state this stream
// doesn't cover ("stalled"/"dead"/"error" are read-time-only derivations in
// liveState, never written here — see status.go).
func eventForTransition(agent string, prev *AgentStatus, st AgentStatus) *AgentEvent {
	if prev != nil && prev.State == st.State {
		toolChanged := st.State == "working" && st.Tool != "" &&
			(st.Tool != prev.Tool || st.Target != prev.Target)
		if !toolChanged {
			return nil
		}
	}
	ev := AgentEvent{Agent: agent, TS: st.TS}
	switch st.State {
	case "working":
		ev.Type, ev.Status, ev.Tool, ev.Target = "agent.progress", "working", st.Tool, st.Target
	case "blocked":
		ev.Type, ev.Question = "agent.question", st.Reason
		if ev.Question == "" || ev.Question == "permission" {
			ev.Question = "needs your input"
		}
	case "idle":
		// Routine same-fleet completions are noise for a single-user voice
		// client: the addressed agent's own reply already conveys "done", and
		// speaking every fleet agent's "task complete" spammed a live voice
		// session once the producer went fleet-wide. Don't publish routine
		// idle notifications. Proper per-client scoping (surface only the
		// addressed agent + high/critical) is the follow-up in the consumer
		// (muster-voice _speak_events, echo) — restore idle publishing then.
		return nil
	case "ended":
		// Same reasoning as idle; already low-priority but still fleet noise.
		return nil
	default:
		return nil
	}
	return &ev
}
