package main

import (
	"os"
	"runtime/debug"
)

// musterVersion is a short build marker so a restart is verifiable at a glance
// — Michael's ask: "it's hard to tell if the TUI actually updated". `go build`
// stamps the git revision into the binary automatically (Go 1.18+ VCS info),
// so there is NO build plumbing to add. When that stamp is absent (built with
// -buildvcs=false, or from a non-git tree) it falls back to the installed
// binary's mtime — the same freshness signal workspaceBinaryChanged already
// keys off (workspace.go).
func musterVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev string
		var dirty bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" {
			if len(rev) > 7 {
				rev = rev[:7]
			}
			if dirty {
				rev += "*" // uncommitted changes were in the tree at build
			}
			return rev
		}
	}
	// no VCS stamp: the install/build time IS the "did it update?" signal
	if fi, err := os.Stat(selfExe()); err == nil {
		return "built " + fi.ModTime().Format("Jan2 15:04")
	}
	return "dev"
}

// musterVersionFull is the verbose form for `muster --version`: the marker plus
// the installed binary's mtime and the Go toolchain, so a reinstall can be
// CONFIRMED from the CLI (real functional output, not a checksum) — the
// discipline the macOS Gatekeeper binary-swap taught us.
func musterVersionFull() string {
	s := "muster " + musterVersion()
	if fi, err := os.Stat(selfExe()); err == nil {
		s += "  (installed " + fi.ModTime().Format("2006-01-02 15:04:05") + ")"
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.GoVersion != "" {
		s += "  " + info.GoVersion
	}
	return s
}
