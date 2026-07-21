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
	// Model is first-class launch state, NOT part of the verbatim Argv: a
	// user --model in the spawn command is adopted here (adoptModel) exactly
	// like --session-id, and composed back in at every launch/resume. That
	// makes `muster model <name> opus` a one-line spec edit instead of a
	// kill-and-retype — the spec is the truth, the flag is derived.
	Model string `json:"model,omitempty"`
	// Template names the role template this agent was spawned from
	// (template.go) — the pool key for dispatch ("give this to any idle
	// backend"). Spare marks a pre-warmed pool member nobody has claimed
	// yet: booted, briefed, primed, waiting for its first task.
	Template string `json:"template,omitempty"`
	Spare    bool   `json:"spare,omitempty"`
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

// handoffPath: the agent's rolling handoff file — the user's clear-not-compact
// workflow made first-class. The briefing tells the agent to keep it current;
// `muster refresh` flushes it, /clear wipes the context, and the SessionStart
// hook re-injects it. Keyed by NAME (not session uuid): it must survive the
// very rotation it exists for.
func handoffPath(name string) string {
	return filepath.Join(dataDir(), "handoff", name+".md")
}

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
		"5 lines with your current task, progress, blockers, and what's next." +
		" CONTEXT HANDOFF: maintain " + handoffPath(s.Name) + " as a rolling markdown handoff — current " +
		"task, state, key decisions, file paths, exact next steps. Update it after every significant step, " +
		"not just when asked: when your context is cleared (muster does this at high context usage), that " +
		"file is automatically re-injected and is ALL your future self gets. Write it to resume cold."
	ps := loadProjects()
	if proj := projectFor(ps, lsRow{Dir: s.Dir, Repo: s.Repo}); proj != "" {
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
	if cp := conventionsPrompt(s); cp != "" {
		b += " " + cp
	}
	return b
}

// conventionsPrompt returns the opted-in project's WORKFLOW CONVENTIONS
// line, or "" if the project has none set (#12's opt-in, default-off
// injection) — pulled out of meshBriefing so it reaches an agent's system
// prompt even with --no-ppz, where meshBriefing itself never runs (that's
// entirely mesh-specific; conventions aren't).
func conventionsPrompt(s *AgentSpec) string {
	ps := loadProjects()
	proj := projectFor(ps, lsRow{Dir: s.Dir, Repo: s.Repo})
	if proj == "" {
		return ""
	}
	for _, p := range ps {
		if p.Name == proj && p.ConventionsPrompt != "" {
			return "WORKFLOW CONVENTIONS: " + p.ConventionsPrompt
		}
	}
	return ""
}

// teamRoster lists the other agents (name + one-line specialty) for the
// briefing. It uses shortRole, NOT the full o.Role: a role is a whole task
// brief (often paragraphs), and embedding every teammate's entire brief here
// bloated the roster to ~20KB+ — it grows with the fleet, is re-paid on every
// spawn/clear/retask/spare-boot, and once overflowed argv into the
// "tmux new-session: command too long" crash (see injected() in this file).
// A teammate only needs "who does what" to decide who to ping; the detail is
// a `ppz send`/`ppz who` away on demand.
func teamRoster(self string) string {
	specs, _ := listSpecs()
	var parts []string
	for _, o := range specs {
		if o.Name == self || o.PpzHandle == "" {
			continue
		}
		p := o.Name
		if sr := shortRole(o.Role); sr != "" {
			p += " (" + sr + ")"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// shortRole condenses a free-form role brief to a one-line specialty: the
// first sentence (to the first period or line break), capped. Whitespace is
// collapsed so a multi-line brief can't smuggle newlines into the roster. The
// cap slices RUNES, not bytes — roles are free-form text (accents, curly
// quotes, em-dashes), and a byte-boundary cut would emit invalid UTF-8 into
// the --append-system-prompt argv this very fix exists to keep clean.
func shortRole(role string) string {
	role = strings.TrimSpace(role)
	// '.' and '\n' are single-byte, so this byte index is always a rune boundary
	if i := strings.IndexAny(role, ".\n"); i >= 0 {
		role = strings.TrimSpace(role[:i])
	}
	role = strings.Join(strings.Fields(role), " ")
	const max = 60
	if r := []rune(role); len(r) > max {
		role = strings.TrimSpace(string(r[:max])) + "…"
	}
	return role
}

// identityPrompt is the who-you-are half of the briefing: the mesh briefing
// (role, roster, handoff contract, room etiquette — or bare conventions for
// --no-ppz agents) plus the template specialty. Shared by the spawn flags
// and the /clear hook re-injection, so both always agree and the roster is
// recomputed fresh at the moment it's needed.
func identityPrompt(s *AgentSpec) string {
	// --no-ppz agents get no mesh briefing (nothing mesh-specific applies),
	// but an opted-in project's conventions aren't mesh-specific and must
	// still reach them (#12).
	prompt := conventionsPrompt(s)
	if s.PpzHandle != "" {
		prompt = meshBriefing(s) // already folds conventionsPrompt in
	}
	// templated agents learn their specialty + go-to skills (template.go)
	if s.Template != "" {
		if tb := templateBriefing(findTemplate(s.Template)); tb != "" {
			if prompt != "" {
				prompt += " "
			}
			prompt += tb
		}
	}
	return prompt
}

// muster-injected flags, recomputed at every launch — never stored in Argv.
// firstLaunch gates the system-prompt briefing (team roster + conventions):
// only the FIRST launch actually needs it — a resumed session's transcript
// already has it, and re-embedding it every resume is pure waste that grows
// with the team (a real one: "tmux new-session: ...: command too long" once
// the roster got big enough, 2026-07-16).
func injected(s *AgentSpec, hooksSettings string, firstLaunch bool) []string {
	var extra []string
	if skipPermissions() && !argvHas(s.Argv, "--dangerously-skip-permissions") {
		extra = append(extra, "--dangerously-skip-permissions")
	}
	if hooksSettings != "" {
		extra = append(extra, "--settings", hooksSettings)
	}
	if !firstLaunch {
		return extra
	}
	prompt := identityPrompt(s)
	// the context pack (project primer + team lessons, packs.go) seeds every
	// fresh context regardless of mesh membership — it's a process flag, so
	// it survives /clear along with the rest of the briefing.
	if pack := contextPack(s); pack != "" {
		if prompt != "" {
			prompt += "\n\n"
		}
		prompt += pack
	}
	if prompt != "" {
		extra = append(extra, "--append-system-prompt", prompt)
	}
	return extra
}

// composeSpawn builds the argv actually executed at first launch.
// claude: user argv (any user --session-id/--model adopted into the spec
// first) + our --session-id/--model + injected extras.
func composeSpawn(s *AgentSpec, hooksSettings string) []string {
	if s.Harness != "claude" {
		return s.Argv
	}
	argv := append([]string{}, s.Argv...)
	argv = append(argv, "--session-id", s.SessionUUID)
	if s.Model != "" {
		argv = append(argv, "--model", s.Model)
	}
	return append(argv, injected(s, hooksSettings, true)...)
}

// composeResume builds the argv for a faithful restart: the user's exact
// argv with the harness resume flag appended. Never reconstructed. The
// spec's Model is composed back in too — --model on --resume overrides the
// transcript's model, so a `muster model <name> X` done while the agent was
// dead takes effect on the next resume.
func composeResume(s *AgentSpec, hooksSettings string) []string {
	if s.Harness != "claude" || s.SessionUUID == "" {
		return s.Argv // verbatim rerun is the honest fallback
	}
	argv := append([]string{}, s.Argv...)
	argv = append(argv, "--resume", s.SessionUUID)
	if s.Model != "" {
		argv = append(argv, "--model", s.Model)
	}
	return append(argv, injected(s, hooksSettings, false)...)
}

// adoptSessionID pulls a user-supplied --session-id out of argv into the
// spec (so resume targets it) and returns argv without the flag.
func adoptSessionID(argv []string) (string, []string) {
	return adoptValueFlag(argv, "--session-id")
}

// adoptModel pulls a user-supplied --model out of argv into the spec — the
// exact same move as adoptSessionID, for the exact same reason: the model
// is launch state muster manages (spec.Model, `muster model`), so it must
// not be frozen inside the verbatim Argv where only a respawn could change it.
func adoptModel(argv []string) (string, []string) {
	return adoptValueFlag(argv, "--model")
}

func adoptValueFlag(argv []string, flag string) (string, []string) {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1], stripFlag(argv, flag)
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"="), stripFlag(argv, flag)
		}
	}
	return "", argv
}
