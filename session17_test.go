package main

import "testing"

// The control-handle prune must be throttled: it runs off the Stop hook, which
// fires at every turn-end, but the ppz unsubscribe only needs to happen
// occasionally.
func TestCtlSubPruneThrottle(t *testing.T) {
	pinState(t)
	if !ctlSubPruneDue("al") {
		t.Fatal("first prune should be due")
	}
	if ctlSubPruneDue("al") {
		t.Fatal("second prune within the window should be throttled")
	}
	// a different agent is tracked independently
	if !ctlSubPruneDue("bo") {
		t.Fatal("a different agent's first prune should be due")
	}
	if ctlSubPruneDue("bo") {
		t.Fatal("bo second prune should be throttled")
	}
}
