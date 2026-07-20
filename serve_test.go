package main

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRun records calls and returns canned output — the serve handler's
// subprocess seam, no real muster/ppz involved.
type fakeRun struct {
	mu    sync.Mutex
	calls [][]string
	out   []byte
	err   error
}

func (f *fakeRun) run(kind string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{kind}, args...))
	return f.out, f.err
}

func (f *fakeRun) waitCalls(n int) [][]string {
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		calls := make([][]string, len(f.calls))
		copy(calls, f.calls)
		f.mu.Unlock()
		if len(calls) >= n || time.Now().After(deadline) {
			return calls
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func serveReq(t *testing.T, h *serveHandler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *httptest.ResponseRecorder = httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range header {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(r, req)
	return r
}

func TestServeFleetPassthrough(t *testing.T) {
	f := &fakeRun{out: []byte(`[{"name":"alice"}]`)}
	h := &serveHandler{run: f.run}
	r := serveReq(t, h, "GET", "/v1/fleet", "", nil)
	if r.Code != 200 || r.Body.String() != `[{"name":"alice"}]` {
		t.Fatalf("fleet: code=%d body=%q", r.Code, r.Body.String())
	}
	calls := f.waitCalls(1)
	if len(calls) != 1 || calls[0][0] != "self" || calls[0][1] != "ls" || calls[0][2] != "--json" {
		t.Fatalf("fleet call: %v", calls)
	}
}

func TestServeRereadValidatesTarget(t *testing.T) {
	f := &fakeRun{out: []byte("{}")}
	h := &serveHandler{run: f.run}
	if r := serveReq(t, h, "GET", "/v1/reread/room-pixel?limit=50", "", nil); r.Code != 200 {
		t.Fatalf("good pipe refused: %d", r.Code)
	}
	if r := serveReq(t, h, "GET", "/v1/reread/alice.inbox", "", nil); r.Code != 200 {
		t.Fatalf("inbox leaf refused: %d", r.Code)
	}
	for _, bad := range []string{"/v1/reread/..%2Fetc", "/v1/reread/--flag", "/v1/reread/a%20b"} {
		if r := serveReq(t, h, "GET", bad, "", nil); r.Code != 400 {
			t.Fatalf("bad target %q allowed: %d", bad, r.Code)
		}
	}
	calls := f.waitCalls(2)
	if got := strings.Join(calls[0], " "); got != "ppz reread room-pixel --json -l 50" {
		t.Fatalf("reread argv: %q", got)
	}
	if got := strings.Join(calls[1], " "); got != "ppz reread alice.inbox --json -l 120" {
		t.Fatalf("default limit argv: %q", got)
	}
}

func TestServeLimitClamped(t *testing.T) {
	f := &fakeRun{out: []byte("{}")}
	h := &serveHandler{run: f.run}
	serveReq(t, h, "GET", "/v1/reread/x?limit=99999", "", nil)
	calls := f.waitCalls(1)
	if got := strings.Join(calls[0], " "); got != "ppz reread x --json -l 120" {
		t.Fatalf("overlarge limit not clamped to default: %q", got)
	}
}

func TestServeSendAgent(t *testing.T) {
	f := &fakeRun{out: []byte("sent")}
	h := &serveHandler{run: f.run}
	r := serveReq(t, h, "POST", "/v1/send/agent/alice", "  fix the login bug  ", nil)
	if r.Code != 200 {
		t.Fatalf("send: %d %s", r.Code, r.Body.String())
	}
	calls := f.waitCalls(1)
	if got := strings.Join(calls[0], " "); got != "self send alice fix the login bug" {
		t.Fatalf("send argv: %q", got)
	}
	if r := serveReq(t, h, "POST", "/v1/send/agent/alice", "   ", nil); r.Code != 400 {
		t.Fatalf("empty body allowed: %d", r.Code)
	}
	if r := serveReq(t, h, "GET", "/v1/send/agent/alice", "hi", nil); r.Code != 405 {
		t.Fatalf("GET send allowed: %d", r.Code)
	}
}

func TestServeActVerbs(t *testing.T) {
	f := &fakeRun{out: []byte("ok")}
	h := &serveHandler{run: f.run}
	if r := serveReq(t, h, "POST", "/v1/act/review/alice", "", nil); r.Code != 202 {
		t.Fatalf("act review: %d", r.Code)
	}
	if r := serveReq(t, h, "POST", "/v1/act/format/alice", "", nil); r.Code != 400 {
		t.Fatalf("unknown verb allowed: %d", r.Code)
	}
	calls := f.waitCalls(1) // async — wait for the goroutine
	if len(calls) != 1 || strings.Join(calls[0], " ") != "self review alice" {
		t.Fatalf("act calls: %v", calls)
	}
}

func TestServeSpawnComposesArgv(t *testing.T) {
	f := &fakeRun{out: []byte("ok")}
	h := &serveHandler{run: f.run}
	body := `{"name":"nova","repo":"~/Repos/x","branch":"feat-1","role":"api dev","task":"read the docs"}`
	if r := serveReq(t, h, "POST", "/v1/spawn", body, nil); r.Code != 202 {
		t.Fatalf("spawn: %d", r.Code)
	}
	calls := f.waitCalls(2) // spawn then the first-task send
	if got := strings.Join(calls[0], " "); got != "self spawn nova --repo ~/Repos/x -b feat-1 -role api dev -- claude" {
		t.Fatalf("spawn argv: %q", got)
	}
	if got := strings.Join(calls[1], " "); got != "self send nova read the docs" {
		t.Fatalf("task argv: %q", got)
	}
	if r := serveReq(t, h, "POST", "/v1/spawn", `{"repo":"x"}`, nil); r.Code != 400 {
		t.Fatalf("nameless spawn allowed: %d", r.Code)
	}
}

func TestServeBearerToken(t *testing.T) {
	f := &fakeRun{out: []byte("{}")}
	h := &serveHandler{run: f.run, token: "s3cret"}
	if r := serveReq(t, h, "GET", "/v1/fleet", "", nil); r.Code != 401 {
		t.Fatalf("tokenless allowed: %d", r.Code)
	}
	if r := serveReq(t, h, "GET", "/v1/fleet", "", map[string]string{"Authorization": "Bearer wrong"}); r.Code != 401 {
		t.Fatalf("wrong token allowed: %d", r.Code)
	}
	if r := serveReq(t, h, "GET", "/v1/fleet", "", map[string]string{"Authorization": "Bearer s3cret"}); r.Code != 200 {
		t.Fatalf("right token refused: %d", r.Code)
	}
}

func TestServeCliFailureIs502(t *testing.T) {
	f := &fakeRun{out: []byte("no such agent \"zed\"\n"), err: errf("exit 1")}
	h := &serveHandler{run: f.run}
	r := serveReq(t, h, "GET", "/v1/recap/zed", "", nil)
	if r.Code != 502 || !strings.Contains(r.Body.String(), "no such agent") {
		t.Fatalf("cli failure: code=%d body=%q", r.Code, r.Body.String())
	}
}
