package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ctlSession is the PPZ_SESSION muster's own commands run under, so the
// control handle survives across muster invocations (fresh subprocesses).
const ctlSession = "muster-ctl"
const ctlHandle = "mstrctl"

func ppzBin() string {
	if p := os.Getenv("MUSTER_PPZ"); p != "" {
		return p
	}
	if p, err := exec.LookPath("ppz"); err == nil {
		return p
	}
	return ""
}

func ppzCmd(session string, args ...string) *exec.Cmd {
	c := exec.Command(ppzBin(), args...)
	// NO_COLOR: we scrape output. PPZ_UPDATE_CHECK=0: status/login otherwise
	// make a network update check per call.
	c.Env = append(os.Environ(), "PPZ_SESSION="+session, "NO_COLOR=1", "PPZ_UPDATE_CHECK=0")
	return c
}

// Every ppz call muster makes goes through ppzOut (stdout+stderr mixed) or
// ppzJSON (stdout only — stderr must not pollute parsed JSON), both with a
// hard timeout. A wedged daemon once turned the TUI's 2s tick into an
// unbounded pileup of hung subprocesses on an already-struggling laptop
// (2026-07-10 incident) — muster must degrade to "pipes off", not amplify.

func ppzTimeout() time.Duration {
	if v := os.Getenv("MUSTER_PPZ_TIMEOUT_MS"); v != "" {
		if n, err := time.ParseDuration(v + "ms"); err == nil && n > 0 {
			return n
		}
	}
	return 10 * time.Second
}

func ppzRun(session string, combined bool, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ppzTimeout())
	defer cancel()
	c := exec.CommandContext(ctx, ppzBin(), args...)
	c.Env = append(os.Environ(), "PPZ_SESSION="+session, "NO_COLOR=1", "PPZ_UPDATE_CHECK=0")
	c.WaitDelay = 2 * time.Second // SIGKILL stragglers that ignore the ctx kill
	var out []byte
	var err error
	if combined {
		out, err = c.CombinedOutput()
	} else {
		out, err = c.Output()
	}
	if ctx.Err() == context.DeadlineExceeded {
		return out, errf("ppz %s timed out after %s (daemon wedged? ppz daemon restart)", args[0], ppzTimeout())
	}
	return out, err
}

func ppzOut(session string, args ...string) ([]byte, error) {
	return ppzRun(session, true, args...)
}

func ppzJSON(session string, args ...string) ([]byte, error) {
	return ppzRun(session, false, args...)
}

// ppzStatusText is the raw `ppz status` output — already the best short
// human summary of the mesh (daemon, server, account, nats).
func ppzStatusText() string {
	if ppzBin() == "" {
		return "ppz CLI not found (set MUSTER_PPZ or install ppz)"
	}
	out, err := ppzOut(ctlSession, "status")
	if err != nil && len(out) == 0 {
		return "ppz status: " + err.Error()
	}
	return strings.TrimSpace(string(out))
}

var readyCache struct {
	sync.Mutex
	ok bool
	at time.Time
}

// ppzReady reports whether the ppz daemon is up and logged in. Cached ~10s:
// the TUI refreshes every 2s and several helpers call this per refresh.
func ppzReady() bool {
	if ppzBin() == "" {
		return false
	}
	readyCache.Lock()
	defer readyCache.Unlock()
	if time.Since(readyCache.at) < 10*time.Second {
		return readyCache.ok
	}
	out, err := ppzOut(ctlSession, "status")
	// exact "daemon: logged in" — "daemon: not logged in" / "authentication
	// error" both contain the bare substring "logged in" and were false
	// positives here (ppz status has no --json form to check structurally).
	readyCache.ok = err == nil && strings.Contains(string(out), "daemon: logged in")
	readyCache.at = time.Now()
	return readyCache.ok
}

// ensureCtlHandle makes sure the mstrctl control handle exists and is
// current for the muster-ctl session. Idempotent.
func ensureCtlHandle() error {
	out, err := ppzJSON(ctlSession, "get", "handle")
	if err == nil && strings.TrimSpace(string(out)) == ctlHandle {
		return nil
	}
	// create (tolerate taken), then set current
	if out, err := ppzOut(ctlSession, "source", "create", ctlHandle); err != nil {
		if !strings.Contains(string(out), "E_SOURCE_TAKEN") && !strings.Contains(string(out), "E_NAME_TAKEN") {
			return errf("ppz source create %s: %s (%v)", ctlHandle, out, err)
		}
		if out, err := ppzOut(ctlSession, "set", "handle", ctlHandle); err != nil {
			return errf("ppz set handle: %s (%v)", out, err)
		}
	}
	return nil
}

func ppzSend(target, payload string, extra ...string) error {
	if err := ensureCtlHandle(); err != nil {
		return err
	}
	args := append([]string{"send", target, payload}, extra...)
	if out, err := ppzOut(ctlSession, args...); err != nil {
		return errf("ppz send: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// roomPipe is the shared uncollared pipe backing a project's room chat:
// "room-" + the project name squeezed into ppz's segment regex
// (^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$, max 32).
func roomPipe(proj string) string {
	var b []rune
	for _, r := range strings.ToLower(proj) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b = append(b, r)
		case len(b) > 0 && b[len(b)-1] != '-':
			b = append(b, '-')
		}
	}
	s := "room-" + strings.Trim(string(b), "-")
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.TrimRight(s, "-")
}

// ensureRoomPipe creates proj's room pipe (uncollared, at root — the ctl
// session never sets a namespace). Idempotent: an existing pipe is success.
// E_NAME_TAKEN is NOT tolerated — it means the name clashes with a source
// handle, which needs a human.
func ensureRoomPipe(proj string) (string, error) {
	if err := ensureCtlHandle(); err != nil {
		return "", err
	}
	pipe := roomPipe(proj)
	if out, err := ppzOut(ctlSession, "pipe", "create", pipe); err != nil {
		if !strings.Contains(string(out), "E_PIPE_TAKEN") && !strings.Contains(string(out), "already exists") {
			return "", errf("ppz pipe create %s: %s", pipe, out)
		}
	}
	return pipe, nil
}

// subscribeRoom adds pipe to an agent session's subscriptions so room
// traffic reaches it via `ppz subs read` / the idle nudge. Idempotent;
// best-effort (the agent still works without the room).
func subscribeRoom(session, pipe string) {
	_, _ = ppzOut(session, "subs", "add", pipe)
}

type ppzHeartbeat struct {
	Handle  string
	Status  string // online|stale|offline
	State   string // ""|idle|working|blocked
	Harness string
	Model   string
	Host    string
	Project string // muster's registered project name, if the spawning machine set one
}

// ppzWho returns heartbeat info keyed by handle. Empty map when ppz is
// unavailable — callers degrade gracefully.
func ppzWho() map[string]ppzHeartbeat {
	res := map[string]ppzHeartbeat{}
	if !ppzReady() {
		return res
	}
	out, err := ppzJSON(ctlSession, "who", "--json")
	if err != nil {
		return res
	}
	var rows []struct {
		Handle    string `json:"handle"`
		Status    string `json:"status"`
		Heartbeat struct {
			AgentState string `json:"agent_state"`
			Harness    string `json:"harness"`
			Model      string `json:"model"`
			Hostname   string `json:"hostname"`
			Project    string `json:"project"`
		} `json:"heartbeat"`
	}
	if json.Unmarshal(out, &rows) != nil {
		return res
	}
	for _, r := range rows {
		res[r.Handle] = ppzHeartbeat{
			Handle: r.Handle, Status: r.Status, State: r.Heartbeat.AgentState,
			Harness: r.Heartbeat.Harness, Model: r.Heartbeat.Model, Host: r.Heartbeat.Hostname,
			Project: r.Heartbeat.Project,
		}
	}
	return res
}

type ppzEnvelope struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	Subject   string `json:"subject"`
	Payload   string `json:"payload"`
	CreatedAt string `json:"created_at"`
	InReplyTo string `json:"in_reply_to"`
}

// ppzReadInbox reads (cursor-advancing) new messages on handle's inbox,
// from the relay's own session so each agent inbox has one relay cursor.
func ppzReadInbox(session, handle string) ([]ppzEnvelope, error) {
	out, err := ppzJSON(session, "read", handle+".inbox", "--json")
	if err != nil {
		// "no new messages" exits non-zero on some paths; treat empty as ok
		if len(strings.TrimSpace(string(out))) == 0 {
			return nil, nil
		}
		return nil, err
	}
	var msgs []ppzEnvelope
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var e ppzEnvelope
		if json.Unmarshal([]byte(line), &e) == nil {
			msgs = append(msgs, e)
		}
	}
	return msgs, nil
}

// ppzReread replays retained history on target without moving any cursor
// (envelopes newest-last). since is a ppz duration like "6h".
func ppzReread(target, since string) []ppzEnvelope {
	out, err := ppzJSON(ctlSession, "reread", target, "--json", "--since", since)
	if err != nil && len(strings.TrimSpace(string(out))) == 0 {
		return nil
	}
	var msgs []ppzEnvelope
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var e ppzEnvelope
		if json.Unmarshal([]byte(line), &e) == nil {
			msgs = append(msgs, e)
		}
	}
	return msgs
}

// ppzScheduleText is `ppz schedule ls` verbatim (already a tidy table).
func ppzScheduleText() string {
	out, _ := ppzOut(ctlSession, "schedule", "ls")
	return strings.TrimSpace(string(out))
}

type ppzScheduleEntry struct {
	ID       string `json:"id"`
	Handle   string `json:"handle"`
	Pipe     string `json:"pipe"`
	Schedule string `json:"schedule"` // "every" | "cron" | "at"
	Spec     string `json:"spec"`     // the interval / cron expr / timestamp
	NextAt   string `json:"next_at"`
	LastAt   string `json:"last_at"`
	Payload  string `json:"payload"`
	Creator  string `json:"creator"`
}

// ppzScheduleList returns all live schedules in next-fire order.
func ppzScheduleList() []ppzScheduleEntry {
	if !ppzReady() {
		return nil
	}
	out, err := ppzJSON(ctlSession, "schedule", "ls", "--json")
	if err != nil && len(strings.TrimSpace(string(out))) == 0 {
		return nil
	}
	var entries []ppzScheduleEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var e ppzScheduleEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries
}

// ppzScheduleRm removes a schedule by ID. Returns nil on success.
func ppzScheduleRm(id string) error {
	out, err := ppzOut(ctlSession, "schedule", "rm", id)
	if err != nil {
		return errf("ppz schedule rm %s: %s", id, strings.TrimSpace(string(out)))
	}
	return nil
}

type ppzPipeRow struct {
	Handle string `json:"handle"`
	Pipe   string `json:"pipe"`
	Unread int    `json:"unread"`
	Total  int    `json:"total"`
}

// ppzLs runs `ppz ls --json [pattern]` (NDJSON: one row per line).
func ppzLs(pattern string) []ppzPipeRow {
	args := []string{"ls", "--json"}
	if pattern != "" {
		args = append(args, pattern)
	}
	out, err := ppzJSON(ctlSession, args...)
	if err != nil {
		return nil
	}
	var rows []ppzPipeRow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var r ppzPipeRow
		if json.Unmarshal([]byte(line), &r) == nil {
			rows = append(rows, r)
		}
	}
	return rows
}

// ppzSourceExists reports whether any pipe exists under the handle —
// true after a previous `terminal share <handle>` run, even post-exit.
func ppzSourceExists(handle string) bool {
	return len(ppzLs(handle+".*")) > 0
}

// ppzUnreadCounts returns unread message counts per "<handle>.inbox"
// according to the ctl session's cursors (cheap glance for `muster ls`).
func ppzUnreadCounts() map[string]int {
	res := map[string]int{}
	if !ppzReady() {
		return res
	}
	for _, r := range ppzLs("") {
		if r.Pipe == "inbox" {
			res[r.Handle] = r.Unread
		}
	}
	return res
}

// ppzMarkRead advances mstrctl's read cursor on handle's inbox so the
// unread badge clears after the user opens an agent. Fire-and-forget;
// ignore errors (best-effort, mesh may be unavailable).
//
// `ppz read` flood-caps at 10 messages per call by default — a backlog
// bigger than that (any agent with a normal chatty session) left the
// badge stuck nonzero after just one call (#8, round 1 regression).
// -l 0 uncaps it so one mark-read call fully drains the cursor.
func ppzMarkRead(handle string) {
	if !ppzReady() || handle == "" {
		return
	}
	ppzOut(ctlSession, "read", handle+".inbox", "-l", "0", "--json")
}

// ppzSourceDestroy removes handle and all its pipes from the mesh.
// Used to clear offline remote agents from the sidebar permanently.
func ppzSourceDestroy(handle string) error {
	out, err := ppzOut(ctlSession, "source", "destroy", handle)
	if err != nil {
		return errf("ppz source destroy %s: %s", handle, strings.TrimSpace(string(out)))
	}
	return nil
}
