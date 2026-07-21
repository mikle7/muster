package main

// P2: native auto-compaction (SessionStart source="compact") is re-injected
// like a refresh (SAME task), instead of being received-and-ignored. Before
// this, a compacted agent — especially a resumed one, which never got the
// --append-system-prompt briefing — silently lost its identity/handoff.

import (
	"os"
	"strings"
	"testing"
)

// A compaction is SAME-task: re-brief identity + re-inject the handoff thread,
// do NOT park the handoff aside and do NOT seed the project primer as if new.
func TestClearHookCompactIsSameTask(t *testing.T) {
	pinState(t)
	writeFile(t, handoffPath("al"), "LIVE-TASK-NOTES")
	s := &AgentSpec{Name: "al", Harness: "claude", Dir: t.TempDir(), PpzHandle: "al", Argv: []string{"claude"}}
	if err := saveSpec(s); err != nil {
		t.Fatal(err)
	}
	ctx := string(clearHookJSON("al", "compact"))
	for _, want := range []string{
		"auto-compacted",     // accurate wording, not "cleared"
		"SAME task",          // same-task treatment
		"You are agent 'al'", // identity re-injected (the resumed-agent amnesia fix)
		"LIVE-TASK-NOTES",    // handoff thread carried, not dropped
	} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("compact injection misses %q: %s", want, ctx)
		}
	}
	// the live handoff must survive a compaction untouched — never parked .prev
	if _, err := os.Stat(handoffPath("al")); err != nil {
		t.Fatal("compaction must not park/remove the handoff — it's the live thread")
	}
	if _, err := os.Stat(handoffPath("al") + ".prev"); !os.IsNotExist(err) {
		t.Fatal("compaction must not create a .prev — that's the NEW-task (manual) path")
	}
}

// A compaction must never consume a retask/refresh marker set aside for a
// real /clear: clearModeForSource routes compact past clearMode entirely.
func TestClearModeForSourceCompactDoesNotEatMarkers(t *testing.T) {
	pinState(t)
	setRetaskMark("al", "new thing")
	if got := clearModeForSource("al", "compact"); got != "compact" {
		t.Fatalf("compact source must map to compact mode, got %q", got)
	}
	// the retask mark must still be pending for the real /clear that follows
	if got := clearModeForSource("al", "clear"); got != "retask" {
		t.Fatalf("compact must not have consumed the retask mark, got %q", got)
	}
}
