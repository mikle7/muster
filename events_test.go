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
