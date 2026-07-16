package main

// Session 9: surface remote ppz-mesh agents (no local tmux session, spawned
// on a different machine) in the main sidebar, not just the M mesh view.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakePpz writes a script standing in for the ppz binary and points
// MUSTER_PPZ at it, so ppzReady()/ppzWho() never touch the real daemon.
// Mirrors TestPpzRunTimesOut's pattern (session5_test.go). Also clears the
// 10s package-level readyCache — otherwise an earlier test's real (or
// stale) result would leak in here.
func fakePpz(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ppz")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUSTER_PPZ", p)
	readyCache.at = time.Time{}
}

func TestBuildRowsAddsRemoteAgent(t *testing.T) {
	hb := map[string]ppzHeartbeat{
		"ivy": {Handle: "ivy", Status: "online", State: "working", Harness: "claude", Model: "opus", Host: "linux-desktop"},
	}
	rows := buildRows(nil, hb, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if !r.Remote {
		t.Errorf("Remote = false, want true")
	}
	if r.Name != "ivy" || r.Ppz != "ivy" {
		t.Errorf("Name/Ppz = %q/%q, want ivy/ivy", r.Name, r.Ppz)
	}
	if r.State != "working" || r.Harness != "claude" || r.Model != "opus" || r.Host != "linux-desktop" {
		t.Errorf("got state=%q harness=%q model=%q host=%q", r.State, r.Harness, r.Model, r.Host)
	}
	if r.Tmux != "" || r.Dir != "" || r.Branch != "" || r.Wt {
		t.Errorf("remote row should leave Tmux/Dir/Branch/Wt zero: %+v", r)
	}
}

func TestBuildRowsSkipsLocalHandle(t *testing.T) {
	specs := []*AgentSpec{{Name: "ivy", PpzHandle: "ivy", TmuxSession: "mstr-ivy"}}
	hb := map[string]ppzHeartbeat{
		"ivy": {Handle: "ivy", Status: "online", State: "working", Harness: "claude"},
	}
	rows := buildRows(specs, hb, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (no duplicate for a locally-spawned handle): %+v", len(rows), rows)
	}
	if rows[0].Remote {
		t.Errorf("the local spec's row should not be marked Remote")
	}
}

func TestBuildRowsSkipsNonAgentsAndSelf(t *testing.T) {
	hb := map[string]ppzHeartbeat{
		"human":       {Handle: "human", Status: "online"},   // interactive ppz login, no harness — not an agent
		ctlHandle:     {Handle: ctlHandle, Status: "online"}, // muster's own control handle
		"remoteAgent": {Handle: "remoteAgent", Status: "online", Harness: "codex", State: "idle"},
	}
	rows := buildRows(nil, hb, nil, nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (only the harness-bearing handle): %+v", len(rows), rows)
	}
	if rows[0].Name != "remoteAgent" {
		t.Errorf("got %q, want remoteAgent", rows[0].Name)
	}
}

func TestBuildRowsOfflineRemoteIsDead(t *testing.T) {
	hb := map[string]ppzHeartbeat{
		"jack": {Handle: "jack", Status: "offline", State: "working", Harness: "claude"},
	}
	rows := buildRows(nil, hb, nil, map[string]bool{"jack": true})
	if len(rows) != 1 || rows[0].State != "dead" {
		t.Fatalf("got %+v, want a single dead row (offline liveness beats stale agent_state)", rows)
	}
}

// TestBuildRowsDestroyedSourceDropped covers #4/#5: ppz's heartbeat history
// outlives `source destroy` (it's a log, not live state), so without the
// sourceExists check a cleared/killed-and-removed agent reappears as a
// phantom dead row forever.
func TestBuildRowsDestroyedSourceDropped(t *testing.T) {
	hb := map[string]ppzHeartbeat{
		"ghost": {Handle: "ghost", Status: "offline", State: "working", Harness: "claude"},
	}
	rows := buildRows(nil, hb, nil, map[string]bool{"ghost": false})
	if len(rows) != 0 {
		t.Fatalf("got %+v, want no rows (source destroyed, heartbeat stale)", rows)
	}
}

func TestBuildRowsRemoteInboxDepth(t *testing.T) {
	hb := map[string]ppzHeartbeat{"quinn": {Handle: "quinn", Status: "online", State: "idle", Harness: "claude"}}
	inbox := map[string]int{"quinn": 3}
	rows := buildRows(nil, hb, inbox, nil)
	if len(rows) != 1 || rows[0].Inbox != 3 {
		t.Fatalf("got %+v, want inbox=3", rows)
	}
}

// ---- remote rows bucket by project name (follow-up to the mesh-sidebar
// work: paths differ per machine, so a remote row carries its spawning
// machine's registered project NAME via the heartbeat instead) ----------

func TestBuildRowsCarriesProject(t *testing.T) {
	hb := map[string]ppzHeartbeat{
		"ivy": {Handle: "ivy", Status: "online", State: "working", Harness: "claude", Project: "pixel-studios"},
	}
	rows := buildRows(nil, hb, nil, nil)
	if len(rows) != 1 || rows[0].Project != "pixel-studios" {
		t.Fatalf("got %+v, want Project=pixel-studios", rows)
	}
}

func TestProjectForLocalPathUnaffected(t *testing.T) {
	ps := []Project{{Name: "pixel-studios", Path: "/repo/pixel-studios"}}
	r := lsRow{Dir: "/repo/pixel-studios/apps/api"}
	if got := projectFor(ps, r); got != "pixel-studios" {
		t.Fatalf("got %q, want pixel-studios (path-matching must stay unchanged)", got)
	}
}

func TestProjectForRemoteNameFallback(t *testing.T) {
	ps := []Project{{Name: "pixel-studios", Path: "/repo/pixel-studios"}}
	r := lsRow{Remote: true, Project: "pixel-studios"} // no Dir/Repo — different machine, different path
	if got := projectFor(ps, r); got != "pixel-studios" {
		t.Fatalf("got %q, want pixel-studios via name fallback", got)
	}
}

func TestProjectForRemoteUnknownProjectIsUnassigned(t *testing.T) {
	ps := []Project{{Name: "pixel-studios", Path: "/repo/pixel-studios"}}
	r := lsRow{Remote: true, Project: "some-other-repo-not-registered-here"}
	if got := projectFor(ps, r); got != "" {
		t.Fatalf("got %q, want \"\" (unassigned) for a project this machine hasn't registered", got)
	}
}

// agentHandle must resolve a mesh-only handle directly (no local spec) so
// `muster send`/`inbox`/`cron add` work against a remote sidebar row — the
// same distinction buildRows draws to decide what's a synthetic remote row.
// Without this fallback, those commands 404 on "no such agent" for any
// agent this machine didn't spawn itself (found while verifying greg's plan
// — his assumption was `s` needed no change; it did).
func TestAgentHandleFallsBackToMeshHandle(t *testing.T) {
	pinState(t)
	fakePpz(t, `case "$1" in
status) echo "daemon: logged in" ;;
who) cat <<'EOF'
[{"handle":"remote1","status":"online","heartbeat":{"agent_state":"working","harness":"claude","model":"","hostname":"h"}}]
EOF
;;
esac`)
	h, err := agentHandle("remote1")
	if err != nil || h != "remote1" {
		t.Fatalf("agentHandle(remote1) = %q, %v — want remote1, nil", h, err)
	}
	if _, err := agentHandle("nobody-on-the-mesh-or-locally"); err == nil {
		t.Fatalf("expected an error for a name that's neither a local spec nor a mesh handle")
	}
}
