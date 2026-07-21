package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// capHead/capTail cap to a BYTE budget but must cut on a rune boundary — a
// split multibyte char garbles the injected system prompt.
func TestCapRuneSafe(t *testing.T) {
	s := strings.Repeat("é", 10) // "é" = 2 bytes (0xC3 0xA9); 20 bytes total

	// every byte budget across a mid-rune range must stay valid UTF-8
	for n := 0; n <= 22; n++ {
		if h := capHead(s, n); !utf8.ValidString(h) {
			t.Fatalf("capHead(n=%d) invalid UTF-8: %q", n, h)
		}
		if tl := capTail(s, n); !utf8.ValidString(tl) {
			t.Fatalf("capTail(n=%d) invalid UTF-8: %q", n, tl)
		}
	}

	// concrete mid-rune cut: budget 5 lands inside the 3rd char → back off to 4 bytes
	if got := capHead(s, 5); got != "éé…" {
		t.Fatalf("capHead mid-rune = %q, want éé…", got)
	}
	if got := capTail(s, 5); got != "…éé" {
		t.Fatalf("capTail mid-rune = %q, want …éé", got)
	}

	// under budget → unchanged, no ellipsis
	if got := capHead("hello", 100); got != "hello" {
		t.Fatalf("capHead under budget = %q", got)
	}
	if got := capTail("hello", 100); got != "hello" {
		t.Fatalf("capTail under budget = %q", got)
	}
	// ASCII over budget → exact byte cut
	if got := capHead("abcdef", 3); got != "abc…" {
		t.Fatalf("capHead ascii = %q", got)
	}
	if got := capTail("abcdef", 3); got != "…def" {
		t.Fatalf("capTail ascii = %q", got)
	}
}
