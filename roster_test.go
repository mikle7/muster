package main

// P1: the team roster embeds a one-line specialty per teammate, not the full
// role brief — the full brief bloated argv to 20KB+ and overflowed into the
// "tmux new-session: command too long" crash. shortRole is the condenser.

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortRole(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"reviewer", "reviewer"},
		{"Platform/backend feature dev, pixel-studios. Continuing work on X.", "Platform/backend feature dev, pixel-studios"},
		{"Line one\nLine two\nLine three", "Line one"},
		{strings.Repeat("x", 200), strings.Repeat("x", 60) + "…"},
		// multibyte: cap at 60 RUNES, and the first-sentence cut on a period
		// after a multibyte char must stay clean
		{"café touched everything. rest dropped", "café touched everything"},
		{strings.Repeat("é", 200), strings.Repeat("é", 60) + "…"},
	}
	for _, c := range cases {
		if got := shortRole(c.in); got != c.want {
			t.Fatalf("shortRole(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// no newline may ever reach the roster (it's a single comma-joined line)
	if strings.ContainsAny(shortRole("a\nb"), "\n") {
		t.Fatal("shortRole leaked a newline")
	}
	// the cap must NEVER emit invalid UTF-8 — this string goes into JSON + argv.
	// A byte-slice cap of "…"*100 would split the 3-byte ellipsis rune.
	for _, in := range []string{strings.Repeat("…", 100), strings.Repeat("🎮x", 80), strings.Repeat("naïve ", 40)} {
		if got := shortRole(in); !utf8.ValidString(got) {
			t.Fatalf("shortRole(%q…) produced invalid UTF-8: %q", in[:12], got)
		}
	}
}

func TestTeamRosterUsesShortRoleNotFullBrief(t *testing.T) {
	pinState(t)
	long := "Poker dev, pixel-studios. SECRET-TASK-DETAIL that must not be in everyone else's prompt."
	for _, n := range []string{"cole", "reid"} {
		s := &AgentSpec{Name: n, PpzHandle: n, Role: long, Argv: []string{"claude"}, Harness: "claude"}
		if err := saveSpec(s); err != nil {
			t.Fatal(err)
		}
	}
	roster := teamRoster("cole") // cole's own view of the team
	if strings.Contains(roster, "cole") {
		t.Fatalf("self must be excluded from its own roster: %s", roster)
	}
	if !strings.Contains(roster, "reid (Poker dev, pixel-studios)") {
		t.Fatalf("roster should carry the one-line specialty: %s", roster)
	}
	if strings.Contains(roster, "SECRET-TASK-DETAIL") {
		t.Fatalf("roster leaked the full role brief — the whole bug: %s", roster)
	}
}
