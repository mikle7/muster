package main

import (
	"testing"
	"time"
)

func TestEventForTransition(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		prev     *AgentStatus
		st       AgentStatus
		wantType string
		wantNil  bool
	}{
		{"cold start into working", nil, AgentStatus{State: "working", TS: now}, "agent.progress", false},
		{"same-state re-report is suppressed", &AgentStatus{State: "working"}, AgentStatus{State: "working", TS: now}, "", true},
		{"idle -> blocked is a question", &AgentStatus{State: "idle"}, AgentStatus{State: "blocked", Reason: "needs your permission to use Bash", TS: now}, "agent.question", false},
		{"working -> idle is a notification", &AgentStatus{State: "working"}, AgentStatus{State: "idle", TS: now}, "agent.notification", false},
		{"working -> ended is a notification", &AgentStatus{State: "working"}, AgentStatus{State: "ended", TS: now}, "agent.notification", false},
		{"unmapped state is suppressed", &AgentStatus{State: "working"}, AgentStatus{State: "unknown", TS: now}, "", true},
	}
	for _, c := range cases {
		ev := eventForTransition("planner", c.prev, c.st)
		if c.wantNil {
			if ev != nil {
				t.Errorf("%s: got %+v, want nil", c.name, ev)
			}
			continue
		}
		if ev == nil {
			t.Fatalf("%s: got nil, want type %s", c.name, c.wantType)
		}
		if ev.Type != c.wantType {
			t.Errorf("%s: type = %s, want %s", c.name, ev.Type, c.wantType)
		}
		if ev.Agent != "planner" {
			t.Errorf("%s: agent = %s, want planner", c.name, ev.Agent)
		}
	}
}

func TestEventForTransitionBlockedFallsBackToGenericQuestion(t *testing.T) {
	for _, reason := range []string{"", "permission"} {
		ev := eventForTransition("reviewer", &AgentStatus{State: "idle"}, AgentStatus{State: "blocked", Reason: reason, TS: time.Now()})
		if ev == nil || ev.Question != "needs your input" {
			t.Errorf("reason %q: question = %+v, want fallback", reason, ev)
		}
	}
}

func TestEventForTransitionFiresOnToolChangeWithinAWorkingStreak(t *testing.T) {
	now := time.Now()
	prev := &AgentStatus{State: "working", Tool: "Read", Target: "auth.ts"}
	st := AgentStatus{State: "working", Tool: "Bash", Target: "npm test", TS: now}
	ev := eventForTransition("planner", prev, st)
	if ev == nil {
		t.Fatal("tool change within a working streak must still fire, got nil")
	}
	if ev.Type != "agent.progress" || ev.Tool != "Bash" || ev.Target != "npm test" {
		t.Errorf("got %+v, want progress event carrying the new tool/target", ev)
	}
}

func TestEventForTransitionFiresOnTargetChangeWithTheSameTool(t *testing.T) {
	prev := &AgentStatus{State: "working", Tool: "Read", Target: "auth.ts"}
	st := AgentStatus{State: "working", Tool: "Read", Target: "session.ts", TS: time.Now()}
	ev := eventForTransition("planner", prev, st)
	if ev == nil || ev.Target != "session.ts" {
		t.Errorf("same-tool different-target must still fire, got %+v", ev)
	}
}

func TestEventForTransitionSuppressesTheSameToolAndTarget(t *testing.T) {
	// The PreToolUse/PostToolUse pair for one tool call: same tool, same
	// target, both "working" — must collapse into a single event, not
	// double-fire.
	prev := &AgentStatus{State: "working", Tool: "Bash", Target: "npm test"}
	st := AgentStatus{State: "working", Tool: "Bash", Target: "npm test", TS: time.Now()}
	if ev := eventForTransition("planner", prev, st); ev != nil {
		t.Errorf("same tool+target re-report must be suppressed, got %+v", ev)
	}
}

func TestEventForTransitionSuppressesWorkingWithNoToolName(t *testing.T) {
	// UserPromptSubmit/SessionStart also map to "working" but carry no
	// tool_name at all — must not refire just because Tool is empty on
	// both sides (or flip-flops to/from empty).
	prev := &AgentStatus{State: "working", Tool: "Read", Target: "auth.ts"}
	st := AgentStatus{State: "working", Tool: "", Target: "", TS: time.Now()}
	if ev := eventForTransition("planner", prev, st); ev != nil {
		t.Errorf("a working re-report with no tool_name must be suppressed, got %+v", ev)
	}
}

func TestEventForTransitionFirstToolCallFiresEvenWithNoPriorTool(t *testing.T) {
	// SessionStart (working, no tool) -> first PreToolUse (working, Read) —
	// both "working", but this is the first real tool call and must narrate.
	prev := &AgentStatus{State: "working", Tool: "", Target: ""}
	st := AgentStatus{State: "working", Tool: "Read", Target: "auth.ts", TS: time.Now()}
	ev := eventForTransition("planner", prev, st)
	if ev == nil || ev.Tool != "Read" || ev.Target != "auth.ts" {
		t.Errorf("first tool call in a working streak must fire, got %+v", ev)
	}
}
