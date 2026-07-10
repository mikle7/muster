package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// AgentSpec is the durable record of how an agent was launched. Argv is the
// user's command VERBATIM — muster-injected flags (--session-id, --settings)
// are composed at launch time and never written back into Argv. That is the
// whole faithful-resume guarantee.
type AgentSpec struct {
	Name        string            `json:"name"`
	Role        string            `json:"role,omitempty"` // "reviewer", "game dev for pixel studios"
	Dir         string            `json:"dir"`            // cwd the agent runs in
	Repo        string            `json:"repo,omitempty"`
	Worktree    string            `json:"worktree,omitempty"`
	Branch      string            `json:"branch,omitempty"`
	Argv        []string          `json:"argv"`
	Env         map[string]string `json:"env,omitempty"`
	Harness     string            `json:"harness"` // "claude" | "" (unknown)
	SessionUUID string            `json:"session_uuid,omitempty"`
	PpzHandle   string            `json:"ppz_handle,omitempty"`
	TmuxSession string            `json:"tmux_session"`
	CreatedAt   time.Time         `json:"created_at"`
	ResumedAt   time.Time         `json:"resumed_at,omitempty"`
}

func dataDir() string {
	if d := os.Getenv("MUSTER_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "muster")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "muster")
}

func agentsDir() string { return filepath.Join(dataDir(), "agents") }
func statusDir() string { return filepath.Join(dataDir(), "status") }

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

func validName(n string) error {
	if !nameRe.MatchString(n) {
		return fmt.Errorf("invalid agent name %q (want [a-z0-9-], max 31 chars, ppz-handle compatible)", n)
	}
	return nil
}

func specPath(name string) string { return filepath.Join(agentsDir(), name+".json") }

func loadSpec(name string) (*AgentSpec, error) {
	b, err := os.ReadFile(specPath(name))
	if err != nil {
		return nil, err
	}
	var s AgentSpec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("corrupt spec %s: %w", specPath(name), err)
	}
	return &s, nil
}

func saveSpec(s *AgentSpec) error {
	if err := os.MkdirAll(agentsDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(specPath(s.Name), b)
}

func deleteSpec(name string) error { return os.Remove(specPath(name)) }

func listSpecs() ([]*AgentSpec, error) {
	ents, err := os.ReadDir(agentsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*AgentSpec
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := loadSpec(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "muster: skipping %s: %v\n", e.Name(), err)
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func atomicWrite(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand.Read never fails on supported platforms
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// detectHarness returns "claude" when argv launches Claude Code; anything
// else is opaque (spawned/resumed verbatim).
func detectHarness(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	if filepath.Base(argv[0]) == "claude" {
		return "claude"
	}
	return ""
}

func argvHas(argv []string, flag string) bool {
	for _, a := range argv {
		if a == flag {
			return true
		}
	}
	return false
}

// stripFlag removes `flag <value>` and `flag=value` occurrences from argv.
func stripFlag(argv []string, flag string) []string {
	var out []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == flag {
			i++ // skip value
			continue
		}
		if strings.HasPrefix(a, flag+"=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// meshBriefing is appended to claude's system prompt (only when the agent
// is on the ppz mesh) so ppz's built-in subs-alert nudge ("Please run
// 'ppz subs read' and action messages") makes sense to the agent. It also
// establishes the TEAM: who you are (role), who your teammates are, and the
// standup ritual — recomputed at every launch/resume so the roster is fresh.
func meshBriefing(s *AgentSpec) string {
	b := "You are agent '" + s.Name + "' on a muster-managed team, connected to the ppz message mesh " +
		"(handle '" + s.PpzHandle + "', PPZ_SESSION preset)."
	if s.Role != "" {
		b += " Your role: " + s.Role + "."
	}
	if roster := teamRoster(s.Name); roster != "" {
		b += " Teammates: " + roster + "."
	}
	b += " Message any teammate by name: ppz send <name> '<text>' (64KiB cap — send pointers like " +
		"branch/sha/path, not diffs); 'ppz who' shows who's online. The user (and their control handle " +
		"mstrctl) also messages you. When told to run 'ppz subs read', run it and act on every message, " +
		"replying to senders by name. A message starting with STANDUP means: reply to its sender in under " +
		"5 lines with your current task, progress, blockers, and what's next."
	if proj := projectFor(loadProjects(), lsRow{Dir: s.Dir, Repo: s.Repo}); proj != "" {
		pipe := roomPipe(proj)
		b += " TEAM ROOM: '" + pipe + "' is a shared pipe the whole #" + proj + " team (and the user) reads — " +
			"you are subscribed, its messages arrive via 'ppz subs read'. Reply to room messages IN the room " +
			"(ppz send " + pipe + " '<text>'), never to the sender's inbox. Room etiquette: (1) a room message " +
			"addressed to a specific agent is handled by that agent alone — everyone else stays silent unless " +
			"they have something material to add; (2) before doing work whose result serves the whole room " +
			"(fetching an issue list, triaging a backlog, summarizing state), post 'CLAIMING: <task>' to the " +
			"room and check it wasn't already claimed — if a teammate claimed it, wait for their summary and " +
			"build on that instead of redoing the fetch; (3) keep room messages short and conversational — " +
			"discuss and divide, don't broadcast identical reports."
	}
	return b
}

// teamRoster lists the other agents (name + role) for the briefing.
func teamRoster(self string) string {
	specs, _ := listSpecs()
	var parts []string
	for _, o := range specs {
		if o.Name == self || o.PpzHandle == "" {
			continue
		}
		p := o.Name
		if o.Role != "" {
			p += " (" + o.Role + ")"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// muster-injected flags, recomputed at every launch — never stored in Argv.
func injected(s *AgentSpec, hooksSettings string) []string {
	var extra []string
	if skipPermissions() && !argvHas(s.Argv, "--dangerously-skip-permissions") {
		extra = append(extra, "--dangerously-skip-permissions")
	}
	if hooksSettings != "" {
		extra = append(extra, "--settings", hooksSettings)
	}
	if s.PpzHandle != "" {
		extra = append(extra, "--append-system-prompt", meshBriefing(s))
	}
	return extra
}

// composeSpawn builds the argv actually executed at first launch.
// claude: user argv (any user --session-id adopted into the spec first)
// + our --session-id + injected extras.
func composeSpawn(s *AgentSpec, hooksSettings string) []string {
	if s.Harness != "claude" {
		return s.Argv
	}
	argv := append([]string{}, s.Argv...)
	argv = append(argv, "--session-id", s.SessionUUID)
	return append(argv, injected(s, hooksSettings)...)
}

// composeResume builds the argv for a faithful restart: the user's exact
// argv with the harness resume flag appended. Never reconstructed.
func composeResume(s *AgentSpec, hooksSettings string) []string {
	if s.Harness != "claude" || s.SessionUUID == "" {
		return s.Argv // verbatim rerun is the honest fallback
	}
	argv := append([]string{}, s.Argv...)
	argv = append(argv, "--resume", s.SessionUUID)
	return append(argv, injected(s, hooksSettings)...)
}

// adoptSessionID pulls a user-supplied --session-id out of argv into the
// spec (so resume targets it) and returns argv without the flag.
func adoptSessionID(argv []string) (string, []string) {
	for i, a := range argv {
		if a == "--session-id" && i+1 < len(argv) {
			return argv[i+1], stripFlag(argv, "--session-id")
		}
		if strings.HasPrefix(a, "--session-id=") {
			return strings.TrimPrefix(a, "--session-id="), stripFlag(argv, "--session-id")
		}
	}
	return "", argv
}
