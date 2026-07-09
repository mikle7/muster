package main

import (
	"reflect"
	"strings"
	"testing"
)

// pinState isolates dataDir so the developer's real config.json can't leak
// into compose behavior (skip_permissions defaults to true with no config).
func pinState(t *testing.T) {
	t.Setenv("MUSTER_STATE_DIR", t.TempDir())
	t.Setenv("MUSTER_SKIP_PERMISSIONS", "")
}

// The faithful-resume guarantee: every flag the user launched with is
// present on resume, plus --resume <uuid>, and nothing else we didn't add
// deliberately. This is the test herdr would have failed (#965).
func TestComposeResumePreservesAllFlags(t *testing.T) {
	pinState(t)
	s := &AgentSpec{
		Harness:     "claude",
		SessionUUID: "abc-123",
		Argv: []string{"claude", "--dangerously-skip-permissions", "--model", "opus",
			"--mcp-config", "x.json", "--append-system-prompt", "be terse"},
	}
	got := composeResume(s, "/hooks.json")
	want := []string{"claude", "--dangerously-skip-permissions", "--model", "opus",
		"--mcp-config", "x.json", "--append-system-prompt", "be terse",
		"--resume", "abc-123", "--settings", "/hooks.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestComposeAddsMeshBriefingOnlyOnMesh(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Harness: "claude", SessionUUID: "u", Argv: []string{"claude"}, PpzHandle: "w1", Name: "w1"}
	got := composeSpawn(s, "")
	if got[len(got)-2] != "--append-system-prompt" || !strings.Contains(got[len(got)-1], "'w1'") {
		t.Fatalf("mesh briefing missing: %v", got)
	}
	s.PpzHandle = ""
	got = composeSpawn(s, "")
	for _, a := range got {
		if a == "--append-system-prompt" {
			t.Fatalf("briefing injected off-mesh: %v", got)
		}
	}
}

func TestComposeSpawnInjectsSessionID(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Harness: "claude", SessionUUID: "u1", Argv: []string{"claude", "-n", "worker"}}
	got := composeSpawn(s, "")
	want := []string{"claude", "-n", "worker", "--session-id", "u1", "--dangerously-skip-permissions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// skip-permissions is injected at compose time (never stored in Argv),
// deduped when the user already passed it, and off when configured off.
func TestSkipPermissionsInjection(t *testing.T) {
	pinState(t)
	s := &AgentSpec{Harness: "claude", SessionUUID: "u1", Argv: []string{"claude", "--dangerously-skip-permissions"}}
	got := composeSpawn(s, "")
	n := 0
	for _, a := range got {
		if a == "--dangerously-skip-permissions" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("flag should appear exactly once, got %v", got)
	}
	t.Setenv("MUSTER_SKIP_PERMISSIONS", "0")
	got = composeSpawn(&AgentSpec{Harness: "claude", SessionUUID: "u1", Argv: []string{"claude"}}, "")
	if argvHas(got, "--dangerously-skip-permissions") {
		t.Fatalf("flag injected despite MUSTER_SKIP_PERMISSIONS=0: %v", got)
	}
}

func TestNonClaudeVerbatim(t *testing.T) {
	s := &AgentSpec{Harness: "", Argv: []string{"aider", "--yes-always"}}
	if got := composeSpawn(s, "/h.json"); !reflect.DeepEqual(got, s.Argv) {
		t.Fatalf("spawn mutated non-claude argv: %v", got)
	}
	if got := composeResume(s, "/h.json"); !reflect.DeepEqual(got, s.Argv) {
		t.Fatalf("resume mutated non-claude argv: %v", got)
	}
}

func TestAdoptSessionID(t *testing.T) {
	id, rest := adoptSessionID([]string{"claude", "--session-id", "my-id", "--model", "opus"})
	if id != "my-id" || !reflect.DeepEqual(rest, []string{"claude", "--model", "opus"}) {
		t.Fatalf("got id=%q rest=%v", id, rest)
	}
	id, rest = adoptSessionID([]string{"claude", "--session-id=eq-id"})
	if id != "eq-id" || !reflect.DeepEqual(rest, []string{"claude"}) {
		t.Fatalf("got id=%q rest=%v", id, rest)
	}
	id, rest = adoptSessionID([]string{"claude", "-n", "x"})
	if id != "" || len(rest) != 3 {
		t.Fatalf("no-op case broke: id=%q rest=%v", id, rest)
	}
}

func TestShQuote(t *testing.T) {
	cases := map[string]string{
		"plain":        "plain",
		"has space":    "'has space'",
		"don't":        `'don'\''t'`,
		"":             "''",
		"a;b|c&d":      "'a;b|c&d'",
		"$HOME `x` !y": "'$HOME `x` !y'",
	}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestHookEventState(t *testing.T) {
	for ev, want := range map[string]string{
		"PreToolUse": "working", "PostToolUse": "working", "SessionStart": "working",
		"PermissionRequest": "blocked", "Notification": "blocked",
		"Stop": "idle", "SessionEnd": "ended", "SomeFutureEvent": "",
	} {
		if got, _ := hookEventState(ev, ""); got != want {
			t.Errorf("hookEventState(%s) = %q, want %q", ev, got, want)
		}
	}
}

func TestDetectHarness(t *testing.T) {
	if detectHarness([]string{"/usr/local/bin/claude", "-n", "x"}) != "claude" {
		t.Error("path-qualified claude not detected")
	}
	if detectHarness([]string{"codex"}) != "" {
		t.Error("codex misdetected as claude")
	}
}
