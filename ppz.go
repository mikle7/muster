package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
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
	c.Env = append(os.Environ(), "PPZ_SESSION="+session)
	return c
}

// ppzReady reports whether the ppz daemon is up and logged in.
func ppzReady() bool {
	if ppzBin() == "" {
		return false
	}
	out, err := ppzCmd(ctlSession, "status").CombinedOutput()
	return err == nil && strings.Contains(string(out), "logged in")
}

// ensureCtlHandle makes sure the mstrctl control handle exists and is
// current for the muster-ctl session. Idempotent.
func ensureCtlHandle() error {
	out, err := ppzCmd(ctlSession, "get", "handle").Output()
	if err == nil && strings.TrimSpace(string(out)) == ctlHandle {
		return nil
	}
	// create (tolerate taken), then set current
	if out, err := ppzCmd(ctlSession, "source", "create", ctlHandle).CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "E_SOURCE_TAKEN") && !strings.Contains(string(out), "E_NAME_TAKEN") {
			return errf("ppz source create %s: %s", ctlHandle, out)
		}
		if out, err := ppzCmd(ctlSession, "set", "handle", ctlHandle).CombinedOutput(); err != nil {
			return errf("ppz set handle: %s", out)
		}
	}
	return nil
}

func ppzSend(target, payload string, extra ...string) error {
	if err := ensureCtlHandle(); err != nil {
		return err
	}
	args := append([]string{"send", target, payload}, extra...)
	if out, err := ppzCmd(ctlSession, args...).CombinedOutput(); err != nil {
		return errf("ppz send: %s", out)
	}
	return nil
}

type ppzHeartbeat struct {
	Handle  string
	Status  string // online|stale|offline
	State   string // ""|idle|working|blocked
	Harness string
	Model   string
	Host    string
}

// ppzWho returns heartbeat info keyed by handle. Empty map when ppz is
// unavailable — callers degrade gracefully.
func ppzWho() map[string]ppzHeartbeat {
	res := map[string]ppzHeartbeat{}
	if !ppzReady() {
		return res
	}
	out, err := ppzCmd(ctlSession, "who", "--json").Output()
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
		} `json:"heartbeat"`
	}
	if json.Unmarshal(out, &rows) != nil {
		return res
	}
	for _, r := range rows {
		res[r.Handle] = ppzHeartbeat{
			Handle: r.Handle, Status: r.Status, State: r.Heartbeat.AgentState,
			Harness: r.Heartbeat.Harness, Model: r.Heartbeat.Model, Host: r.Heartbeat.Hostname,
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
	out, err := ppzCmd(session, "read", handle+".inbox", "--json").Output()
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
	out, err := ppzCmd(ctlSession, args...).Output()
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
