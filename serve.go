// serve.go — `muster serve`: the HQ gateway. A small HTTP surface over
// the exact same CLI calls any local client (the HQ desktop app, a
// script, a human) already makes — so remote clients that cannot spawn
// processes (a phone on the tailnet) stay pure clients of the mesh.
//
// Design rules, matching the rest of muster:
//   - Every outside fact is produced by the CLIs: muster verbs self-exec
//     this binary (the runSelf philosophy — HTTP and CLI cannot
//     disagree), ppz verbs go through ppzRun with its hard timeout.
//   - Responses are the raw CLI bytes, passed through untouched. The
//     client parses them with the same parsers it uses for local spawn
//     output; the gateway never re-shapes data.
//   - No TLS, no accounts: this is designed to listen on a tailnet (or
//     localhost) where the network layer is the auth boundary. An
//     optional bearer token (--token / MUSTER_HQ_TOKEN) adds a second
//     factor for the cautious.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// serveRunner is the subprocess seam: tests swap it for a fake. kind is
// "self" (this muster binary) or "ppz" (the ppz CLI under ctlSession).
type serveRunner func(kind string, args ...string) ([]byte, error)

func liveRunner(kind string, args ...string) ([]byte, error) {
	switch kind {
	case "self":
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		c := exec.Command(self, args...)
		c.Env = os.Environ()
		out, err := c.CombinedOutput()
		return out, err
	case "ppz":
		return ppzOut(ctlSession, args...)
	}
	return nil, errf("unknown runner kind %q", kind)
}

// ppz's own segment rules are stricter; this just refuses anything that
// could smuggle flags or paths into an argv.
var serveNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

const serveMaxBody = 8 << 10 // ppz caps chat payloads well below this

type serveHandler struct {
	run   serveRunner
	token string
}

func (h *serveHandler) authorized(r *http.Request) bool {
	if h.token == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

func serveText(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// cliResult maps a CLI outcome onto HTTP: success passes the bytes
// through; failure returns 502 with the combined output (the CLI's own
// error text is the most useful diagnostic a client can show).
func cliResult(w http.ResponseWriter, out []byte, err error) {
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		serveText(w, http.StatusBadGateway, []byte(msg+"\n"))
		return
	}
	serveText(w, http.StatusOK, out)
}

func serveLimit(r *http.Request, def, max int) string {
	n := def
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= max {
			n = parsed
		}
	}
	return strconv.Itoa(n)
}

// rereadTarget allows pipe names and inbox leaves ("name.inbox") — the
// only two shapes reread is ever asked for.
func rereadTargetOK(target string) bool {
	leaf := strings.TrimSuffix(target, ".inbox")
	return serveNameRe.MatchString(leaf)
}

func (h *serveHandler) readBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, serveMaxBody))
	if err != nil {
		serveText(w, http.StatusRequestEntityTooLarge, []byte("body too large\n"))
		return "", false
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		serveText(w, http.StatusBadRequest, []byte("empty body\n"))
		return "", false
	}
	return text, true
}

type spawnRequest struct {
	Name   string `json:"name"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Role   string `json:"role"`
	Task   string `json:"task"`
}

func (h *serveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		serveText(w, http.StatusUnauthorized, []byte("bad or missing bearer token\n"))
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	get := func() bool {
		if r.Method != http.MethodGet {
			serveText(w, http.StatusMethodNotAllowed, []byte("GET only\n"))
			return false
		}
		return true
	}
	post := func() bool {
		if r.Method != http.MethodPost {
			serveText(w, http.StatusMethodNotAllowed, []byte("POST only\n"))
			return false
		}
		return true
	}

	switch {
	case path == "/v1/ping":
		if !get() {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"service":"muster-hq-gateway"}` + "\n"))

	case path == "/v1/fleet":
		if !get() {
			return
		}
		out, err := h.run("self", "ls", "--json")
		cliResult(w, out, err)

	case path == "/v1/pipes":
		if !get() {
			return
		}
		out, err := h.run("ppz", "ls", "--json")
		cliResult(w, out, err)

	case path == "/v1/mesh":
		if !get() {
			return
		}
		out, err := h.run("ppz", "status")
		cliResult(w, out, err)

	case strings.HasPrefix(path, "/v1/reread/"):
		if !get() {
			return
		}
		target := strings.TrimPrefix(path, "/v1/reread/")
		if !rereadTargetOK(target) {
			serveText(w, http.StatusBadRequest, []byte("bad target\n"))
			return
		}
		out, err := h.run("ppz", "reread", target, "--json", "-l", serveLimit(r, 120, 500))
		cliResult(w, out, err)

	case strings.HasPrefix(path, "/v1/handoff/"):
		if !get() {
			return
		}
		name := strings.TrimPrefix(path, "/v1/handoff/")
		if !serveNameRe.MatchString(name) {
			serveText(w, http.StatusBadRequest, []byte("bad name\n"))
			return
		}
		body, err := os.ReadFile(handoffPath(name))
		if err != nil {
			serveText(w, http.StatusNotFound, []byte("no handoff note\n"))
			return
		}
		serveText(w, http.StatusOK, body)

	case strings.HasPrefix(path, "/v1/recap/"):
		if !get() {
			return
		}
		name := strings.TrimPrefix(path, "/v1/recap/")
		if !serveNameRe.MatchString(name) {
			serveText(w, http.StatusBadRequest, []byte("bad name\n"))
			return
		}
		out, err := h.run("self", "recap", name)
		cliResult(w, out, err)

	case path == "/v1/diffs":
		if !get() {
			return
		}
		serveText(w, http.StatusOK, h.diffLines())

	case strings.HasPrefix(path, "/v1/send/agent/"):
		if !post() {
			return
		}
		name := strings.TrimPrefix(path, "/v1/send/agent/")
		if !serveNameRe.MatchString(name) {
			serveText(w, http.StatusBadRequest, []byte("bad name\n"))
			return
		}
		text, ok := h.readBody(w, r)
		if !ok {
			return
		}
		out, err := h.run("self", "send", name, text)
		cliResult(w, out, err)

	case strings.HasPrefix(path, "/v1/send/pipe/"):
		if !post() {
			return
		}
		pipe := strings.TrimPrefix(path, "/v1/send/pipe/")
		if !serveNameRe.MatchString(pipe) {
			serveText(w, http.StatusBadRequest, []byte("bad pipe\n"))
			return
		}
		text, ok := h.readBody(w, r)
		if !ok {
			return
		}
		out, err := h.run("ppz", "send", pipe, text)
		cliResult(w, out, err)

	case strings.HasPrefix(path, "/v1/pipe/"):
		if !post() {
			return
		}
		pipe := strings.TrimPrefix(path, "/v1/pipe/")
		if !serveNameRe.MatchString(pipe) {
			serveText(w, http.StatusBadRequest, []byte("bad pipe\n"))
			return
		}
		out, err := h.run("ppz", "pipe", "create", pipe)
		cliResult(w, out, err)

	case strings.HasPrefix(path, "/v1/act/"):
		if !post() {
			return
		}
		rest := strings.TrimPrefix(path, "/v1/act/")
		verb, name, found := strings.Cut(rest, "/")
		if !found || !serveNameRe.MatchString(name) {
			serveText(w, http.StatusBadRequest, []byte("bad act path\n"))
			return
		}
		switch verb {
		case "review", "refresh", "done", "kill":
		default:
			serveText(w, http.StatusBadRequest, []byte("unknown verb\n"))
			return
		}
		// Long-running verbs (refresh waits for idle, done merges) run
		// detached; the client's poll shows the outcome, same as it
		// would for a colleague running the CLI by hand.
		go func() { _, _ = h.run("self", verb, name) }()
		serveText(w, http.StatusAccepted, []byte("started: muster "+verb+" "+name+"\n"))

	case path == "/v1/spawn":
		if !post() {
			return
		}
		var req spawnRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, serveMaxBody))
		if err := dec.Decode(&req); err != nil {
			serveText(w, http.StatusBadRequest, []byte("bad json body\n"))
			return
		}
		if !serveNameRe.MatchString(req.Name) || strings.TrimSpace(req.Repo) == "" {
			serveText(w, http.StatusBadRequest, []byte("need name and repo\n"))
			return
		}
		args := []string{"spawn", req.Name}
		if strings.TrimSpace(req.Branch) != "" {
			args = append(args, "--repo", req.Repo, "-b", req.Branch)
		} else {
			args = append(args, "-C", req.Repo)
		}
		if strings.TrimSpace(req.Role) != "" {
			args = append(args, "-role", req.Role)
		}
		args = append(args, "--", "claude")
		task := strings.TrimSpace(req.Task)
		name := req.Name
		go func() {
			if _, err := h.run("self", args...); err != nil {
				return
			}
			if task != "" {
				_, _ = h.run("self", "send", name, task)
			}
		}()
		serveText(w, http.StatusAccepted, []byte("started: muster spawn "+name+"\n"))

	default:
		serveText(w, http.StatusNotFound, []byte("no such endpoint\n"))
	}
}

// diffLines mirrors the HQ desktop app's worktree diff sweep, server
// side: one "<dir>\t<git shortstat>" line per live worktree agent — the
// same bytes the app's parser already reads.
func (h *serveHandler) diffLines() []byte {
	rows, err := gatherRows()
	if err != nil {
		return nil
	}
	var b strings.Builder
	count := 0
	for _, row := range rows {
		if !row.Wt || row.State == "dead" || row.Dir == "" || count >= 12 {
			continue
		}
		count++
		out, gitErr := exec.Command("git", "-C", row.Dir, "diff", "HEAD", "--shortstat").Output()
		stat := strings.TrimSpace(string(out))
		if gitErr != nil || stat == "" {
			continue
		}
		b.WriteString(row.Dir)
		b.WriteString("\t")
		b.WriteString(stat)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envDefault("MUSTER_HQ_ADDR", ":7777"), "listen address (bind your tailnet IP or leave for all interfaces)")
	token := fs.String("token", os.Getenv("MUSTER_HQ_TOKEN"), "optional bearer token clients must present")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	h := &serveHandler{run: liveRunner, token: *token}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}
	guard := "open (tailnet is the boundary)"
	if *token != "" {
		guard = "bearer token required"
	}
	fmt.Printf("muster HQ gateway listening on %s — %s\n", *addr, guard)
	if err := srv.ListenAndServe(); err != nil {
		return fail(err)
	}
	return 0
}
