package main

// Handoff structure: the rolling handoff (handoffPath) is split by a sentinel
// into a LIVE RESUME section (above) and APPEND-ONLY HISTORY (below).
//
//	# <name> — handoff
//	## Resume state …           <- re-injected verbatim on /clear
//	<!-- muster:log … -->        <- the marker
//	older notes, append-only …   <- auto-pruned, never injected
//
// Two problems this fixes: (1) on /clear the WHOLE handoff was re-injected
// (tail-capped), dragging stale history into every fresh context; now only the
// resume section is injected. (2) nothing bounded the file, so it re-bloated
// across a long-lived agent's turns; now the history below the marker is
// auto-pruned. The marker is SEEDED at spawn so the structure exists by
// default — the briefing only reinforces it, never has to create it.

import (
	"os"
	"path/filepath"
	"strings"
)

// handoffLogMarker separates the live resume section (above) from append-only
// history (below). A distinctive HTML comment, not a `---` rule — thematic
// breaks and frontmatter fences are far too common in markdown handoffs and
// would misfire as a split point.
const handoffLogMarker = "<!-- muster:log — append-only history below; keep the live resume state ABOVE this line -->"

// handoffHistoryCap bounds the append-only history kept below the marker
// (bytes), so the file can't grow without limit as an agent logs turn after
// turn. The resume section above the marker is never touched by pruning.
const handoffHistoryCap = 8000

// seedHandoffTemplate writes a skeleton handoff (resume header + marker) when
// none exists — called at first launch so the structure is present by default.
// Idempotent and non-destructive: an existing handoff (the agent's real notes,
// or one carried across a resume) is left untouched.
func seedHandoffTemplate(name string) {
	hp := handoffPath(name)
	if _, err := os.Stat(hp); err == nil {
		return
	}
	if os.MkdirAll(filepath.Dir(hp), 0o755) != nil {
		return
	}
	skel := "# " + name + " — handoff\n\n" +
		"## Resume state\n" +
		"_Keep this section tight — it is ALL that's re-injected when your context clears._\n" +
		"- Current task:\n- State / progress:\n- Key decisions:\n- Exact next steps:\n\n" +
		handoffLogMarker + "\n"
	_ = atomicWrite(hp, []byte(skel))
}

// handoffResume returns the live resume section — everything above the log
// marker, trimmed. ok is false when the content has no marker (an old handoff,
// or one an agent rewrote without it) so the caller can fall back to its own
// whole-file cap rather than injecting nothing.
func handoffResume(content string) (section string, ok bool) {
	i := strings.Index(content, handoffLogMarker)
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(content[:i]), true
}

// pruneHandoff trims the append-only history below the marker to its most
// recent handoffHistoryCap bytes (rune-safe via capTail), leaving the resume
// section intact. No-op without a marker or when history is already small.
// Best-effort — called at each /clear, when re-bloat would otherwise compound.
func pruneHandoff(name string) {
	hp := handoffPath(name)
	b, err := os.ReadFile(hp)
	if err != nil {
		return
	}
	content := string(b)
	i := strings.Index(content, handoffLogMarker)
	if i < 0 {
		return
	}
	cut := i + len(handoffLogMarker)
	history := content[cut:]
	if len(history) <= handoffHistoryCap {
		return
	}
	head := content[:cut]
	trimmed := capTail(strings.TrimSpace(history), handoffHistoryCap)
	_ = atomicWrite(hp, []byte(head+"\n"+trimmed+"\n"))
}
