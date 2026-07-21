package main

import (
	"os"
	"strings"
	"testing"
)

// The handoff splits into a live resume section (injected on /clear) and
// append-only history (pruned, never injected).

func TestHandoffResumeSplit(t *testing.T) {
	content := "# a — handoff\n## Resume state\n- task: ship X\n\n" +
		handoffLogMarker + "\nold turn 1\nold turn 2\n"
	sec, ok := handoffResume(content)
	if !ok {
		t.Fatal("marker present but not found")
	}
	if !strings.Contains(sec, "ship X") {
		t.Fatalf("resume section missing live state: %q", sec)
	}
	if strings.Contains(sec, "old turn") {
		t.Fatalf("history leaked into resume section: %q", sec)
	}
	// markerless (old handoff) → not ok, caller falls back to whole file
	if _, ok := handoffResume("just some old notes, no marker"); ok {
		t.Fatal("markerless handoff should report ok=false")
	}
}

func TestSeedHandoffTemplate(t *testing.T) {
	pinState(t)
	seedHandoffTemplate("al")
	b, err := os.ReadFile(handoffPath("al"))
	if err != nil {
		t.Fatalf("skeleton not written: %v", err)
	}
	if !strings.Contains(string(b), handoffLogMarker) {
		t.Fatal("seeded handoff missing the log marker")
	}
	// idempotent + non-destructive: existing notes are never clobbered
	_ = os.WriteFile(handoffPath("al"), []byte("MY REAL NOTES"), 0o644)
	seedHandoffTemplate("al")
	b, _ = os.ReadFile(handoffPath("al"))
	if string(b) != "MY REAL NOTES" {
		t.Fatalf("seed clobbered existing notes: %q", b)
	}
}

func TestPruneHandoff(t *testing.T) {
	pinState(t)
	resume := "# a — handoff\n## Resume state\n- task: KEEP ME\n\n" + handoffLogMarker + "\n"
	big := strings.Repeat("log line filler\n", 2000) // ~32KB of history, well over the cap
	_ = os.MkdirAll(strings.TrimSuffix(handoffPath("al"), "/al.md"), 0o755)
	if err := os.WriteFile(handoffPath("al"), []byte(resume+big), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneHandoff("al")
	b, _ := os.ReadFile(handoffPath("al"))
	out := string(b)
	if !strings.Contains(out, "KEEP ME") || !strings.Contains(out, handoffLogMarker) {
		t.Fatal("prune dropped the resume section or marker")
	}
	// history below the marker must be bounded now
	hist := out[strings.Index(out, handoffLogMarker)+len(handoffLogMarker):]
	if len(hist) > handoffHistoryCap+64 { // +slack for the "…"/newlines
		t.Fatalf("history not pruned: %d bytes", len(hist))
	}
	// pruning again is a no-op (already small)
	before, _ := os.ReadFile(handoffPath("al"))
	pruneHandoff("al")
	after, _ := os.ReadFile(handoffPath("al"))
	if string(before) != string(after) {
		t.Fatal("second prune should be a no-op")
	}
}
