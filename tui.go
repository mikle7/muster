package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The TUI is the muster workspace's LEFT pane only — a sidebar of projects
// and agents. The right pane is a real tmux client attached to the selected
// agent (see workspace.go): click it (or press enter) and type into the
// agent exactly as if attached. Every action still self-execs the muster
// CLI (runSelf) so UI and CLI can never disagree. No PTY ownership, ever.

const sidebarW = 38

var (
	cWorking = lipgloss.AdaptiveColor{Light: "166", Dark: "214"}
	cBlocked = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	cIdle    = lipgloss.AdaptiveColor{Light: "28", Dark: "78"}
	cDead    = lipgloss.AdaptiveColor{Light: "244", Dark: "242"}
	cAccent  = lipgloss.AdaptiveColor{Light: "61", Dark: "141"}
	cDim     = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}

	sTitle    = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sDim      = lipgloss.NewStyle().Foreground(cDim)
	sProj     = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sProjSel  = lipgloss.NewStyle().Bold(true).Background(cAccent).Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "232"})
	sSelected = lipgloss.NewStyle().Bold(true).Background(cAccent).Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "232"})
	sHelp     = lipgloss.NewStyle().Foreground(cDim)
	sStatus   = lipgloss.NewStyle().Foreground(cIdle)
	sErr      = lipgloss.NewStyle().Foreground(cBlocked)
	sButton   = lipgloss.NewStyle().Bold(true).Foreground(cAccent).Background(lipgloss.AdaptiveColor{Light: "254", Dark: "236"})
	sField    = lipgloss.NewStyle().Foreground(cAccent)

	// projColors: 6 distinct colours cycled by project name hash so every
	// project gets a stable, visually distinct header colour.
	projColors = []lipgloss.AdaptiveColor{
		{Light: "61", Dark: "141"},  // purple (accent)
		{Light: "64", Dark: "114"},  // green
		{Light: "136", Dark: "178"}, // gold
		{Light: "31", Dark: "74"},   // cyan
		{Light: "125", Dark: "168"}, // magenta
		{Light: "167", Dark: "209"}, // orange
	}
)

// projColor returns a stable colour for proj derived from its name.
func projColor(proj string) lipgloss.AdaptiveColor {
	if proj == "" {
		return cDim
	}
	h := 0
	for _, c := range proj {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return projColors[h%len(projColors)]
}

// markReadCmd returns a background tea.Cmd that advances mstrctl's read
// cursor on handle's inbox so the unread badge clears after opening an agent.
func markReadCmd(handle string) tea.Cmd {
	h := handle
	return func() tea.Msg {
		ppzMarkRead(h)
		return nil
	}
}

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "working":
		return lipgloss.NewStyle().Foreground(cWorking)
	case "blocked", "stalled":
		return lipgloss.NewStyle().Foreground(cBlocked)
	case "idle":
		return lipgloss.NewStyle().Foreground(cIdle)
	}
	return lipgloss.NewStyle().Foreground(cDead)
}

// ---- messages ---------------------------------------------------------------

type tickMsg struct {
	rows     []lsRow
	projects []Project
	space    string
	mesh     bool
	seq      int
}
type execDoneMsg struct {
	label string
	out   string
	err   error
}
type meshMsg struct{ body string }

// attachSettleDelay: how long a remote row must stay selected before
// muster spawns `ppz terminal attach` for it — long enough to clear a
// held-j scroll (key-repeat is ~30-40ms/step), short enough that attach's
// own connect latency on top (dial + subscribe + JetStream scrollback
// replay, ~100-300ms) doesn't push "land on row → see live screen" past
// about a second.
const attachSettleDelay = 250 * time.Millisecond

// attachSettleMsg fires after a remote row has sat selected for
// attachSettleDelay. gen guards against a stale timer outliving further
// navigation — there's no way to cancel an in-flight tea.Cmd, so a
// superseded one just gets dropped on arrival instead.
type attachSettleMsg struct {
	name string
	gen  int
}

func attachSettleCmd(name string, gen int) tea.Cmd {
	return tea.Tick(attachSettleDelay, func(time.Time) tea.Msg {
		return attachSettleMsg{name: name, gen: gen}
	})
}

func refreshCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		rows, _ := gatherRows()
		return tickMsg{rows: rows, projects: loadProjects(), space: currentSpace(), mesh: ppzReady(), seq: seq}
	}
}

// meshCmd assembles the mesh view body (subprocess-heavy — runs async).
func meshCmd() tea.Cmd {
	return func() tea.Msg { return meshMsg{body: meshBody()} }
}

// popupSelf runs a muster command in a tmux display-popup — full width for
// output the 38-col sidebar would clip (inbox, recap). Enter closes.
func popupSelf(argv string) {
	popupCmd(selfExe() + " " + argv)
}

// popupCmd runs an arbitrary shell command in a tmux display-popup — same
// full-width treatment as popupSelf, for commands that aren't `muster …`
// (e.g. `ppz terminal watch` for a mesh-only agent with no local pane).
func popupCmd(cmd string) {
	sh := cmd + `; printf '\n[enter to close] '; read -r _`
	_, _ = tmuxRun("display-popup", "-E", "-w", "80%", "-h", "70%", "sh -c "+shQuote(sh))
}

func runSelf(label string, argv ...string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command(selfExe(), argv...).CombinedOutput()
		return execDoneMsg{label: label, out: strings.TrimSpace(string(out)), err: err}
	}
}

func tickEvery() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{seq: -1} })
}

// ---- sidebar items ----------------------------------------------------------

type sideItem struct {
	kind     string // "proj" | "agent" | "sub" (agent's branch line)
	projName string // proj: display name ("" = unassigned bucket)
	projPath string
	row      lsRow // agent + sub
}

// withSub appends the agent item and, when it has a branch, a dim ⎇ sub-line
// beneath it — "which checkout is in that terminal" at a glance (the herdr
// habit the user asked for). Its own item keeps mouse hit-testing at one
// line per item; clicks on it select its agent.
func withSub(items []sideItem, it sideItem) []sideItem {
	items = append(items, it)
	if it.row.Branch != "" {
		sub := it
		sub.kind = "sub"
		items = append(items, sub)
	}
	return items
}

// stateRank orders agents by how much they need you (herdr's priority sort).
// stalled sits between blocked and working: probably needs a poke, not
// certainly waiting.
func stateRank(state string) int {
	switch state {
	case "blocked":
		return 0
	case "stalled":
		return 1
	case "working":
		return 2
	case "idle":
		return 3
	case "dead", "ended":
		return 5
	}
	return 4
}

// filterRows narrows the fleet by a `/` query — name, role, state, branch or
// project dir, case-insensitive. "Search across all my agent tabs" is a
// straight wishlist item from the research (fleets outgrow one screen fast).
func filterRows(rows []lsRow, q string) []lsRow {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return rows
	}
	var out []lsRow
	for _, r := range rows {
		hay := strings.ToLower(r.Name + " " + r.Role + " " + r.State + " " + r.Branch + " " + r.Dir)
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	return out
}

// buildPriorityItems: flat list, most attention-worthy first — for triage
// when the fleet is big.
func buildPriorityItems(rows []lsRow) []sideItem {
	sorted := append([]lsRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if a, b := stateRank(sorted[i].State), stateRank(sorted[j].State); a != b {
			return a < b
		}
		return sorted[i].Unread > sorted[j].Unread
	})
	var items []sideItem
	for _, r := range sorted {
		items = withSub(items, sideItem{kind: "agent", row: r})
	}
	return items
}

func buildItems(rows []lsRow, projects []Project, space string) []sideItem {
	byProj := map[string][]lsRow{}
	for _, r := range rows {
		p := projectFor(projects, r)
		byProj[p] = append(byProj[p], r)
	}
	// the current space floats to the top
	ordered := make([]Project, 0, len(projects))
	for _, p := range projects {
		if p.Path == space {
			ordered = append(ordered, p)
		}
	}
	for _, p := range projects {
		if p.Path != space {
			ordered = append(ordered, p)
		}
	}
	projects = ordered
	var items []sideItem
	for _, p := range projects {
		items = append(items, sideItem{kind: "proj", projName: p.Name, projPath: p.Path})
		for _, r := range byProj[p.Name] {
			items = withSub(items, sideItem{kind: "agent", row: r, projName: p.Name, projPath: p.Path})
		}
	}
	if loose := byProj[""]; len(loose) > 0 {
		if len(projects) > 0 {
			items = append(items, sideItem{kind: "proj", projName: ""})
		}
		for _, r := range loose {
			items = withSub(items, sideItem{kind: "agent", row: r})
		}
	}
	return items
}

// ---- forms ------------------------------------------------------------------

type ffield struct {
	label string
	hint  string
	ti    textinput.Model
	sel   []string // non-nil = selector field (←/→ cycles), ti unused
	selIx int
}

type uiForm struct {
	kind   string // "spawn" | "project"
	title  string
	fields []ffield
	focus  int
}

func textField(label, placeholder, hint string) ffield {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 512
	ti.Width = sidebarW - 6
	return ffield{label: label, hint: hint, ti: ti}
}

func (f *uiForm) setFocus(i int) {
	for j := range f.fields {
		f.fields[j].ti.Blur()
	}
	f.focus = i
	if f.fields[i].sel == nil {
		f.fields[i].ti.Focus()
	}
}

func (f *uiForm) val(i int) string {
	if f.fields[i].sel != nil {
		return f.fields[i].sel[f.fields[i].selIx]
	}
	return strings.TrimSpace(f.fields[i].ti.Value())
}

func newSpawnForm(projects []Project, preselect, branch string) *uiForm {
	sel := []string{"(none — spawn in cwd)"}
	ix := 0
	for i, p := range projects {
		sel = append(sel, p.Name)
		if p.Name == preselect {
			ix = i + 1
		}
	}
	f := &uiForm{kind: "spawn", title: "new agent", fields: []ffield{
		textField("name", "e.g. alice", "lowercase, digits, dashes"),
		textField("role", "e.g. reviews every PR", "their charter — teammates learn it"),
		{label: "project", sel: sel, selIx: ix, hint: "←/→ to change"},
		textField("branch", "(optional)", "creates a git worktree for it"),
		textField("command", "claude (default)", "the agent's exact command"),
	}}
	if branch != "" {
		f.fields[3].ti.SetValue(branch)
	}
	f.setFocus(0)
	return f
}

// openPicker enters project-picker mode: discovered repos, filter-as-you-type.
func (m *tuiModel) openPicker() {
	m.mode = "pick"
	m.pickRepos = discoverRepos(m.projects)
	m.pickSel = 0
	m.pickInput = textinput.New()
	m.pickInput.Placeholder = "type to filter · or paste a path"
	m.pickInput.Width = sidebarW - 6
	m.pickInput.Focus()
}

func (m *tuiModel) pickFiltered() []string {
	q := strings.ToLower(strings.TrimSpace(m.pickInput.Value()))
	if q == "" {
		return m.pickRepos
	}
	var out []string
	for _, r := range m.pickRepos {
		if strings.Contains(strings.ToLower(collapseHome(r)), q) {
			out = append(out, r)
		}
	}
	return out
}

// pickChoice resolves the submission: a literal path beats the list.
func (m *tuiModel) pickChoice() string {
	if v := strings.TrimSpace(m.pickInput.Value()); strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") {
		return v
	}
	f := m.pickFiltered()
	if m.pickSel >= 0 && m.pickSel < len(f) {
		return f[m.pickSel]
	}
	return ""
}

// ---- model ------------------------------------------------------------------

type promptSpec struct {
	label       string
	placeholder string
	argv        func(text string) []string
}

type tuiModel struct {
	rows     []lsRow
	projects []Project
	space    string
	meshOK   bool
	items    []sideItem
	selName  string
	scroll   int
	w, h     int
	byPrio   bool   // o: sort by attention instead of project
	filter   string // /: narrows the fleet (name/role/state/branch/dir)

	// context-menu target (set on right-click; consumed by F6/F7/F8,
	// which tmux display-menu items send back to this pane)
	menuProj     string
	menuProjPath string

	// project picker (mode "pick")
	pickInput textinput.Model
	pickRepos []string // discovered, unregistered
	pickSel   int

	wp       workspacePanes
	roomView string // project whose room chat owns the right pane ("" = agent)

	// debounced auto-attach for remote rows (retarget's Cmd): selGen
	// invalidates a stale settle timer superseded by further navigation;
	// lastSelForAttach is the row name a settle was last scheduled for, so
	// an unchanged selection (notably the 2s refresh tick) never reschedules.
	selGen           int
	lastSelForAttach string

	mode      string // normal | prompt | form | view | cron
	prompt    promptSpec
	input     textinput.Model
	form      *uiForm
	viewTitle string
	viewBody  string

	// cron mode (C key): interactive schedule list with j/k + delete
	cronEntries []ppzScheduleEntry
	cronSel     int

	status        string
	statErr       bool
	seq           int
	refreshingAt  time.Time // non-zero while a refresh is in flight
	pendingSelect string
	quitKill      bool
}

func newTUI() tuiModel {
	ti := textinput.New()
	ti.CharLimit = 4096
	ti.Width = sidebarW - 4
	return tuiModel{mode: "normal", status: "click an agent, then just type", input: ti}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(refreshCmd(0), tickEvery())
}

// spaceProject names the registered project matching the current space.
func (m *tuiModel) spaceProject() string {
	for _, p := range m.projects {
		if p.Path == m.space {
			return p.Name
		}
	}
	return ""
}

// spawnPreselect: the selected agent's project, else the current space.
func (m *tuiModel) spawnPreselect() string {
	if r := m.selected(); r != nil {
		if p := projectFor(m.projects, *r); p != "" {
			return p
		}
	}
	return m.spaceProject()
}

// selected returns the selected agent row, or nil.
func (m *tuiModel) selected() *lsRow {
	for i := range m.items {
		if m.items[i].kind == "agent" && m.items[i].row.Name == m.selName {
			return &m.items[i].row
		}
	}
	return nil
}

// agentIdxs lists item indexes that are agents (the selection ring).
func (m *tuiModel) agentIdxs() []int {
	var out []int
	for i, it := range m.items {
		if it.kind == "agent" {
			out = append(out, i)
		}
	}
	return out
}

func (m *tuiModel) moveSel(delta int) {
	ring := m.agentIdxs()
	if len(ring) == 0 {
		m.selName = ""
		return
	}
	cur := -1
	for i, idx := range ring {
		if m.items[idx].row.Name == m.selName {
			cur = i
			break
		}
	}
	next := cur + delta
	if cur == -1 {
		next = 0
	}
	if next < 0 {
		next = 0
	}
	if next >= len(ring) {
		next = len(ring) - 1
	}
	m.selName = m.items[ring[next]].row.Name
	m.roomView = "" // navigating agents leaves the room chat
	m.ensureVisible(ring[next])
}

// openRoom shows proj's room chat in the right pane.
func (m *tuiModel) openRoom(proj string) {
	m.roomView = proj
	m.retarget()
	m.status, m.statErr = "#"+proj+" — select an agent (or esc) to leave", false
}

func (m *tuiModel) ensureVisible(itemIdx int) {
	h := m.listH()
	if itemIdx < m.scroll {
		m.scroll = itemIdx
	}
	if itemIdx >= m.scroll+h {
		m.scroll = itemIdx - h + 1
	}
}

// retarget points the workspace's agent pane at the current selection —
// unless a room chat owns it (cleared by selecting an agent or esc). For a
// live remote row it also schedules a debounced auto-attach (the returned
// Cmd): unlike a local row's cheap switch-client, `ppz terminal attach`
// spawns a fresh process every time, so firing it on every j/k step would
// spawn/kill one per row scrolled past. Scheduling itself is deduped
// against lastSelForAttach, so calling this repeatedly with an unchanged
// selection — notably every 2s refresh tick — never reschedules; only an
// actual change in which row is selected does.
func (m *tuiModel) retarget() tea.Cmd {
	if m.roomView != "" {
		m.wp.showRoom(m.roomView)
		return nil
	}
	r := m.selected()
	if r == nil {
		m.wp.retarget("", "", "")
		m.lastSelForAttach = ""
		return nil
	}
	m.wp.retarget(r.Name, r.Tmux, r.State)
	if !r.Remote || r.State == "dead" {
		m.lastSelForAttach = ""
		return nil
	}
	if r.Name == m.lastSelForAttach {
		return nil
	}
	m.lastSelForAttach = r.Name
	m.selGen++
	return attachSettleCmd(r.Name, m.selGen)
}

func (m *tuiModel) rebuild() tea.Cmd {
	rows := filterRows(m.rows, m.filter)
	if m.byPrio || m.filter != "" {
		// a filtered view is a triage view: flat, attention first, no
		// empty project headers
		m.items = buildPriorityItems(rows)
	} else {
		m.items = buildItems(rows, m.projects, m.space)
	}
	ring := m.agentIdxs()
	if m.pendingSelect != "" {
		for _, idx := range ring {
			if m.items[idx].row.Name == m.pendingSelect {
				m.selName = m.pendingSelect
				m.pendingSelect = ""
				m.roomView = "" // a fresh spawn takes the pane
				m.ensureVisible(idx)
			}
		}
	}
	if m.selected() == nil { // selection vanished (killed --rm, first load)
		m.selName = ""
		if len(ring) > 0 {
			m.selName = m.items[ring[0]].row.Name
		}
	}
	if max := len(m.items) - m.listH(); m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	return m.retarget()
}

// maybeAutoRefresh launches the context-refresh cycle for any idle claude
// agent past the ctx threshold — the user's clear-not-compact ritual with the
// human taken out of the loop. Idle-only (never yank a working agent), one
// in-flight per agent (marker), background self-exec so the 2s tick never
// blocks on a minutes-long cycle.
func (m *tuiModel) maybeAutoRefresh() {
	pct := refreshCtxPct()
	if pct <= 0 {
		return
	}
	for _, r := range m.rows {
		if !shouldAutoRefresh(r.Harness, r.State, r.CtxPct, pct) || refreshInFlight(r.Name) {
			continue
		}
		setRefreshMark(r.Name) // claim before the subprocess starts
		_ = exec.Command(selfExe(), "refresh", r.Name).Start()
		m.status, m.statErr = fmt.Sprintf("auto-refresh %s (ctx %d%% ≥ %d%%): handoff → /clear → resume",
			r.Name, int(r.CtxPct), pct), false
	}
}

// ---- layout -----------------------------------------------------------------
// Fixed vertical layout (1 line per item keeps mouse hit-testing trivial):
//   y0 title · list (listH) · detail (detailH, first line is the separator)
//   · buttons · status · help

const detailH = 5

func (m tuiModel) listH() int {
	n := m.h - detailH - 4
	if n < 3 {
		n = 3
	}
	return n
}

func (m tuiModel) listTop() int { return 1 }
func (m tuiModel) btnY() int    { return 1 + m.listH() + detailH }

// ---- update -----------------------------------------------------------------

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.wp.ensure(m.w)
		return m, m.retarget()

	case tickMsg:
		if msg.seq == -1 { // timer fired: kick a real refresh
			// single-flight: if the previous refresh is still running (ppz
			// calls hang when the daemon wedges), don't stack another on
			// top — hung subprocesses piling up every 2s once helped
			// freeze a laptop. The timeout in ppzRun bounds the wait.
			if !m.refreshingAt.IsZero() && time.Since(m.refreshingAt) < 2*ppzTimeout() {
				return m, tickEvery()
			}
			m.seq++
			m.refreshingAt = time.Now()
			return m, tea.Batch(refreshCmd(m.seq), tickEvery())
		}
		if msg.seq != m.seq { // stale refresh
			return m, nil
		}
		m.refreshingAt = time.Time{}
		m.rows, m.projects, m.space, m.meshOK = msg.rows, msg.projects, msg.space, msg.mesh
		rebuildCmd := m.rebuild()
		m.maybeAutoRefresh()
		if m.mode == "mesh" { // keep the mesh view live (standup replies etc.)
			return m, tea.Batch(rebuildCmd, meshCmd())
		}
		return m, rebuildCmd

	case meshMsg:
		if m.mode == "mesh" {
			m.viewBody = msg.body
		}
		return m, nil

	case attachSettleMsg:
		if msg.gen != m.selGen {
			return m, nil // superseded by further navigation
		}
		r := m.selected()
		if r == nil || r.Name != msg.name || !r.Remote || r.State == "dead" {
			return m, nil
		}
		if m.wp.lastTarget == "attach:"+msg.name {
			return m, nil // already the live attach (scrolled away and back)
		}
		m.wp.attachRemote(msg.name)
		m.status, m.statErr = "attached to "+msg.name+" (mesh) — Ctrl-\\ detaches, Ctrl-C passes through", false
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out+" "+msg.err.Error()), true
		} else {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out), false
		}
		m.seq++
		return m, refreshCmd(m.seq)

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case tea.KeyMsg:
		switch m.mode {
		case "prompt":
			return m.updatePrompt(msg)
		case "form":
			return m.updateForm(msg)
		case "pick":
			return m.updatePick(msg)
		case "filter":
			return m.updateFilter(msg)
		case "view":
			switch msg.String() {
			case "esc", "q", "enter":
				m.mode = "normal"
			}
			return m, nil
		case "cron":
			return m.updateCron(msg)
		case "mesh":
			return m.updateMesh(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m tuiModel) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 3
		if msg.Button == tea.MouseButtonWheelUp {
			d = -3
		}
		m.scroll += d
		if max := len(m.items) - m.listH(); m.scroll > max {
			m.scroll = max
		}
		if m.scroll < 0 {
			m.scroll = 0
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonRight && m.mode == "normal" {
		// menu on RELEASE: a menu opened while the button is down closes
		// the moment you let go (tmux selects/dismisses on button-up)
		if msg.Action == tea.MouseActionRelease {
			return m.rightClick(msg)
		}
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || m.mode == "view" || m.mode == "cron" {
		return m, nil
	}
	if m.mode == "pick" {
		// click a repo row = choose it
		if i := m.pickRowAt(msg.Y); i >= 0 {
			m.pickSel = i
			m.mode = "normal"
			return m, runSelf("project add", "project", "add", m.pickFiltered()[i])
		}
		return m, nil
	}
	if m.mode == "form" {
		// click a field line to focus it
		if i := m.formFieldAt(msg.Y); i >= 0 {
			m.form.setFocus(i)
		}
		return m, nil
	}
	// title line = the pipes summary; clicking it opens the mesh view
	if msg.Y == 0 {
		m.mode = "mesh"
		m.viewTitle = "pipes"
		m.viewBody = "…"
		return m, meshCmd()
	}
	// list rows
	if y := msg.Y - m.listTop(); y >= 0 && y < m.listH() {
		idx := m.scroll + y
		var cmd tea.Cmd
		if idx >= 0 && idx < len(m.items) {
			switch it := m.items[idx]; it.kind {
			case "agent", "sub": // the branch line clicks like its agent
				m.selName = it.row.Name
				m.roomView = ""
				cmd = m.retarget()
			case "proj":
				// the row is the room; the trailing + is the spawn button
				if it.projName == "" || msg.X >= sidebarW-4 {
					m.form = newSpawnForm(m.projects, it.projName, "")
					m.mode = "form"
				} else {
					m.openRoom(it.projName)
				}
			}
		}
		return m, cmd
	}
	// buttons
	if msg.Y == m.btnY() {
		switch hitButton(msg.X) {
		case "agent":
			m.form = newSpawnForm(m.projects, m.spawnPreselect(), "")
			m.mode = "form"
		case "project":
			m.openPicker()
		case "term":
			dir := m.space
			if r := m.selected(); r != nil && !r.Remote {
				dir = r.Dir
			}
			if dir != "" {
				m.wp.terminal(dir)
			}
		}
	}
	return m, nil
}

// rightClick opens a tmux display-menu for the item under the pointer.
// Agent actions reuse plain sidebar keys (the click also selects the agent,
// so send-keys s/K/enter act on it); project actions need the clicked
// project, carried in menuProj and consumed by F6/F7/F8.
func (m tuiModel) rightClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	y := msg.Y - m.listTop()
	if y < 0 || y >= m.listH() {
		return m, nil
	}
	idx := m.scroll + y
	if idx < 0 || idx >= len(m.items) {
		return m, nil
	}
	// menu coordinates: display-menu wants client-absolute, msg.X/Y are
	// pane-relative. Ask tmux where this pane actually sits (was a
	// hardcoded "+2 for the border row" guess — session-4 known issue);
	// numeric -y anchors the menu's BOTTOM edge, so +1 puts it under the
	// pointer. Center as the fallback if the query fails.
	mx, my := "C", "C"
	if out, err := tmuxRun("display-message", "-p", "-t", m.wp.left, "#{pane_left}\t#{pane_top}"); err == nil {
		if l, t, ok := strings.Cut(out, "\t"); ok {
			left, _ := strconv.Atoi(l)
			top, _ := strconv.Atoi(t)
			mx, my = strconv.Itoa(left+msg.X), strconv.Itoa(top+msg.Y+1)
		}
	}
	self := "send-keys -t " + m.wp.left + " "
	switch it := m.items[idx]; it.kind {
	case "agent", "sub":
		m.selName = it.row.Name
		cmd := m.retarget()
		menu := []string{"display-menu", "-T", " " + it.row.Name + " ", "-x", mx, "-y", my}
		switch {
		case it.row.Remote && it.row.State == "dead":
			// K triggers the confirm prompt in the TUI's key handler (#4)
			menu = append(menu, "clear from mesh…", "k", self+"K")
		case it.row.Remote:
			// mesh-only: no local tmux/dir/worktree, so only the
			// mesh-messaging actions apply — everything else is local-only
			// (see updateNormal's sel.Remote guards).
			menu = append(menu,
				"attach (type into agent)", "t", self+"Enter",
				"send message…", "s", self+"s",
				"inbox", "i", self+"i",
				"schedule…", "c", self+"c")
		case it.row.State == "dead":
			menu = append(menu,
				"resume", "r", self+"r",
				"kill / remove…", "k", self+"K")
		default:
			menu = append(menu, "type into agent", "t", self+"Enter")
			if m.wp.right != "" {
				wpin := func(dir string) string {
					return "run-shell -b " + shQuote(shQuote(selfExe())+" wpin "+it.row.Name+" "+dir+" "+m.wp.right)
				}
				menu = append(menu,
					"split right", "l", wpin("right"),
					"split down", "j", wpin("down"),
					"split left", "h", wpin("left"),
					"zoom", "z", "resize-pane -Z -t "+m.wp.right,
					"", "", "")
			}
			menu = append(menu,
				"recap", "e", self+"e",
				"refresh context…", "f", self+"f",
				"send message…", "s", self+"s",
				"terminal here", "!", self+"t",
				"open a file…", "v", self+"v",
				"schedule…", "c", self+"c",
				"inbox", "i", self+"i",
				"", "", "",
				"review handoff", "w", self+"w")
			if it.row.Wt {
				menu = append(menu, "done (merge & clean)…", "D", self+"D")
			}
			menu = append(menu, "kill…", "k", self+"K",
				"kill+remove…", "x", self+"X")
		}
		_, _ = tmuxRun(menu...)
		return m, cmd
	case "proj":
		m.menuProj, m.menuProjPath = it.projName, it.projPath
		title := it.projName
		if title == "" {
			title = "workspace"
		}
		menu := []string{"display-menu", "-T", " " + title + " ", "-x", mx, "-y", my,
			"new agent…", "a", self + "F6",
			"new worktree agent…", "w", self + "F7"}
		if it.projName != "" {
			menu = append(menu,
				"room chat", "g", self+"F9",
				"terminal here", "!", self+"F10",
				"", "", "",
				"remove from sidebar", "x", self+"F8")
		}
		_, _ = tmuxRun(menu...)
		return m, nil
	}
	return m, nil
}

// updateMesh handles keys inside the mesh view: the connect actions when
// pipes is off, esc/q/M to close.
func (m tuiModel) updateMesh(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "M":
		m.mode = "normal"
		return m, nil
	case "1":
		if !m.meshOK {
			m.status, m.statErr = "starting ppz daemon…", false
			return m, func() tea.Msg {
				out, err := ppzOut(ctlSession, "daemon", "start")
				return execDoneMsg{label: "daemon start", out: strings.TrimSpace(string(out)), err: err}
			}
		}
	case "2":
		if !m.meshOK {
			then := ""
			if r := m.selected(); r != nil && r.State != "dead" {
				then = r.Tmux
			}
			m.wp.runSetup(shQuote(ppzBin())+" login pipescloud.io", then)
			m.status, m.statErr = "login running in the agent pane →", false
			return m, nil
		}
	case "T":
		return m, runSelf("standup", "standup")
	}
	return m, nil
}

func (m tuiModel) updateCron(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "C":
		m.mode = "normal"
		return m, nil
	case "j", "down":
		if m.cronSel < len(m.cronEntries)-1 {
			m.cronSel++
		}
	case "k", "up":
		if m.cronSel > 0 {
			m.cronSel--
		}
	case "d":
		if len(m.cronEntries) == 0 {
			return m, nil
		}
		e := m.cronEntries[m.cronSel]
		target := e.Handle + "." + e.Pipe
		preview := clip(e.Payload, 30)
		id := e.ID
		m.openPrompt(promptSpec{
			label:       "delete schedule " + id,
			placeholder: "y to remove  " + target + ": " + preview,
			argv: func(t string) []string {
				if !strings.HasPrefix(t, "y") {
					return []string{"ls"}
				}
				return []string{"cron", "rm", id}
			},
		})
		m.mode = "prompt" // openPrompt already sets mode, but be explicit
		return m, nil
	}
	return m, nil
}

// updateFilter: live fleet filtering. Every keystroke narrows the list;
// enter keeps the filter and returns to normal keys, esc clears it.
func (m tuiModel) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "normal"
		m.filter = ""
		cmd := m.rebuild()
		m.status, m.statErr = "filter cleared", false
		return m, cmd
	case "enter":
		m.mode = "normal"
		if m.filter != "" {
			m.status, m.statErr = "filtered: "+m.filter+" — esc clears", false
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filter = strings.TrimSpace(m.input.Value())
	return m, tea.Batch(cmd, m.rebuild())
}

func (m tuiModel) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "normal"
		m.status, m.statErr = "cancelled", false
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.mode = "normal"
		if text == "" {
			m.status, m.statErr = "cancelled (empty)", false
			return m, nil
		}
		argv := m.prompt.argv(text)
		m.status, m.statErr = "running: muster "+strings.Join(argv, " "), false
		return m, runSelf(argv[0], argv...)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m tuiModel) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.form
	switch msg.String() {
	case "esc":
		m.mode = "normal"
		m.status, m.statErr = "cancelled", false
		return m, nil
	case "tab", "down":
		f.setFocus((f.focus + 1) % len(f.fields))
		return m, nil
	case "shift+tab", "up":
		f.setFocus((f.focus + len(f.fields) - 1) % len(f.fields))
		return m, nil
	case "left", "right":
		if fl := &f.fields[f.focus]; fl.sel != nil {
			d := 1
			if msg.String() == "left" {
				d = len(fl.sel) - 1
			}
			fl.selIx = (fl.selIx + d) % len(fl.sel)
			return m, nil
		}
	case "enter":
		return m.submitForm()
	}
	if f.fields[f.focus].sel == nil {
		var cmd tea.Cmd
		f.fields[f.focus].ti, cmd = f.fields[f.focus].ti.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m tuiModel) updatePick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "normal"
		m.status, m.statErr = "cancelled", false
		return m, nil
	case "up", "shift+tab":
		if m.pickSel > 0 {
			m.pickSel--
		}
		return m, nil
	case "down", "tab":
		if m.pickSel < len(m.pickFiltered())-1 {
			m.pickSel++
		}
		return m, nil
	case "enter":
		path := m.pickChoice()
		if path == "" {
			m.status, m.statErr = "nothing to add — type a path or pick a repo", true
			return m, nil
		}
		m.mode = "normal"
		return m, runSelf("project add", "project", "add", path)
	}
	var cmd tea.Cmd
	m.pickInput, cmd = m.pickInput.Update(msg)
	m.pickSel = 0 // filter changed → selection back to top
	return m, cmd
}

func (m tuiModel) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	switch f.kind {
	case "spawn":
		name, role, branch, cmdline := f.val(0), f.val(1), f.val(3), f.val(4)
		projPath := ""
		if ix := f.fields[2].selIx; ix > 0 {
			projPath = m.projects[ix-1].Path
		}
		if err := validName(name); err != nil {
			m.status, m.statErr = err.Error(), true
			return m, nil
		}
		if branch != "" && projPath == "" {
			m.status, m.statErr = "a worktree needs a project — pick one (or clear branch)", true
			return m, nil
		}
		argv := []string{"spawn", name}
		if role != "" {
			argv = append(argv, "--role", role)
		}
		switch {
		case branch != "":
			argv = append(argv, "--repo", projPath, "-b", branch)
		case projPath != "":
			argv = append(argv, "-C", projPath)
		}
		if cmdline != "" {
			argv = append(argv, "--")
			argv = append(argv, strings.Fields(cmdline)...)
		}
		m.mode = "normal"
		m.pendingSelect = name
		m.status, m.statErr = "spawning "+name+"…", false
		return m, runSelf("spawn", argv...)
	}
	m.mode = "normal"
	return m, nil
}

func (m *tuiModel) openPrompt(p promptSpec) {
	m.mode = "prompt"
	m.prompt = p
	m.input.Placeholder = p.placeholder
	m.input.SetValue("")
	m.input.Focus()
}

func (m tuiModel) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sel := m.selected()
	name := ""
	if sel != nil {
		name = sel.Name
	}
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitKill = true
		return m, tea.Quit

	case "d":
		leaveWorkspace()
		return m, nil

	case "j", "down":
		m.moveSel(1)
		return m, m.retarget()
	case "k", "up":
		m.moveSel(-1)
		return m, m.retarget()

	case "g":
		m.seq++
		return m, refreshCmd(m.seq)

	case "o":
		m.byPrio = !m.byPrio
		cmd := m.rebuild()
		if m.byPrio {
			m.status, m.statErr = "sorted by attention (blocked first) — o restores projects", false
		} else {
			m.status, m.statErr = "grouped by project", false
		}
		return m, cmd

	case "enter", "l", "tab":
		if sel == nil {
			return m, nil
		}
		if sel.State == "dead" {
			if sel.Remote {
				m.status, m.statErr = name+" looks offline on the mesh — nothing to resume from here", true
				return m, nil
			}
			return m, runSelf("resume", "resume", name)
		}
		if sel.Remote && m.wp.lastTarget != "attach:"+name {
			// selection already schedules a debounced auto-attach (see
			// retarget/attachSettleMsg) — this is the "don't make me wait
			// ~250ms" override for anyone who wants it now. No-op if the
			// debounce already settled and it's live, so pressing enter on
			// an already-attached row doesn't force an unnecessary
			// respawn/reconnect flicker.
			m.wp.attachRemote(name)
			m.status, m.statErr = "attached to "+name+" (mesh) — Ctrl-\\ detaches, Ctrl-C passes through", false
		}
		m.wp.focus()
		// advance mstrctl's read cursor so the unread badge clears (#8)
		return m, markReadCmd(name)

	case "s":
		if name == "" {
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "send " + n, placeholder: "message… (delivered when idle)",
			argv: func(t string) []string { return []string{"send", n, t} },
		})
		return m, nil

	case "b":
		m.openPrompt(promptSpec{
			label: "broadcast", placeholder: "message to every live agent…",
			argv: func(t string) []string { return []string{"broadcast", t} },
		})
		return m, nil

	case "S", "a":
		m.form = newSpawnForm(m.projects, m.spawnPreselect(), "")
		m.mode = "form"
		return m, nil

	case "z":
		m.wp.zoom()
		return m, nil

	case "esc":
		if m.filter != "" {
			m.filter = ""
			cmd := m.rebuild()
			m.status, m.statErr = "filter cleared", false
			return m, cmd
		}
		if m.roomView != "" {
			m.roomView = ""
			return m, m.retarget()
		}
		return m, nil

	case "/":
		m.mode = "filter"
		m.input.Placeholder = "name, role, state, branch…"
		m.input.SetValue(m.filter)
		m.input.Focus()
		return m, nil

	case "u": // jump to whoever needs you most: blocked > stalled > unread
		ring := m.agentIdxs()
		best, bestKey := -1, 99
		for _, idx := range ring {
			r := m.items[idx].row
			key := stateRank(r.State)
			if key > 1 {
				if r.Unread == 0 || r.State == "dead" {
					continue
				}
				key = 2
			}
			if key < bestKey {
				bestKey, best = key, idx
			}
		}
		if best < 0 {
			m.status, m.statErr = "nobody needs you — all quiet", false
			return m, nil
		}
		m.selName = m.items[best].row.Name
		m.roomView = ""
		m.ensureVisible(best)
		return m, m.retarget()

	case "1", "2", "3", "4", "5", "6", "7", "8", "9": // jump to Nth agent
		n := int(msg.String()[0] - '1')
		ring := m.agentIdxs()
		if n < len(ring) {
			m.selName = m.items[ring[n]].row.Name
			m.roomView = ""
			m.ensureVisible(ring[n])
			return m, m.retarget()
		}
		return m, nil

	case "e": // recap: the 10-second catch-up, full-width popup
		if name == "" {
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = "recap is local-only — "+name+" has no session on this machine", true
			return m, nil
		}
		popupSelf("recap " + name)
		return m, nil

	case "f": // fresh context: flush handoff → /clear → re-inject ("R" = resume --all)
		if sel == nil || sel.State == "dead" {
			m.status, m.statErr = "select a live agent first", true
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = "fresh-context refresh is local-only — "+name+" has no session on this machine", true
			return m, nil
		}
		if sel.Harness != "claude" {
			m.status, m.statErr = name+" isn't a claude agent — nothing to /clear", true
			return m, nil
		}
		if refreshInFlight(name) {
			m.status, m.statErr = name+" is already refreshing", false
			return m, nil
		}
		setRefreshMark(name) // claim now — the subprocess re-marks on start
		_ = exec.Command(selfExe(), "refresh", name).Start()
		m.status, m.statErr = "refreshing "+name+": handoff → /clear → resume", false
		return m, nil

	case "w": // review handoff to a reviewer-role agent over the mesh
		if name == "" {
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = "review needs "+name+"'s local worktree — nothing to diff from here", true
			return m, nil
		}
		return m, runSelf("review", "review", name)

	case "D": // done: merge the worktree branch back & clean up
		if sel == nil {
			return m, nil
		}
		if !sel.Wt { // live Branch is set for ANY git checkout now — done needs a muster worktree
			m.status, m.statErr = name+" has no worktree — done is for worktree agents", true
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "done " + n, placeholder: "y = merge into repo & clean up · y --squash",
			argv: func(t string) []string {
				if !strings.HasPrefix(t, "y") {
					return []string{"ls"} // anything but y… = no-op
				}
				args := []string{"done", n}
				if strings.Contains(t, "--squash") {
					args = append(args, "--squash")
				}
				if strings.Contains(t, "--force") {
					args = append(args, "--force")
				}
				return args
			},
		})
		return m, nil

	case "t": // quick shell in the agent's dir (or the space)
		dir := m.space
		if sel != nil && !sel.Remote { // remote rows have no local dir — fall back to space
			dir = sel.Dir
		}
		if dir == "" {
			m.status, m.statErr = "no agent or space to open a terminal in", true
			return m, nil
		}
		m.wp.terminal(dir)
		return m, nil

	case "v": // pick from files mentioned on the agent's screen (v then 1-9)
		if sel == nil || sel.State == "dead" {
			m.status, m.statErr = "select a live agent first", true
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = "file menu is local-only — "+name+" has no screen on this machine", true
			return m, nil
		}
		fileMenu(sel.Name, m.wp.right)
		return m, nil

	case "V": // open the most recently mentioned file — no menu
		if sel == nil || sel.State == "dead" {
			m.status, m.statErr = "select a live agent first", true
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = "file menu is local-only — "+name+" has no screen on this machine", true
			return m, nil
		}
		files := screenFiles(sel.Tmux, sel.Dir)
		if len(files) == 0 {
			m.status, m.statErr = "no file paths on "+sel.Name+"'s screen", true
			return m, nil
		}
		openFile(m.wp.right, files[0])
		m.status, m.statErr = "→ "+collapseHome(files[0])+" (q closes)", false
		return m, nil

	// F6…F10 arrive from right-click display-menu items (see rightClick)
	case "f6":
		m.form = newSpawnForm(m.projects, m.menuProj, "")
		m.mode = "form"
		return m, nil
	case "f7":
		m.form = newSpawnForm(m.projects, m.menuProj, "wt-"+newUUID()[:4])
		m.mode = "form"
		return m, nil
	case "f8":
		if m.menuProj != "" {
			return m, runSelf("project rm", "project", "rm", m.menuProj)
		}
		return m, nil
	case "f9":
		if m.menuProj != "" {
			m.openRoom(m.menuProj)
		}
		return m, nil
	case "f10":
		if m.menuProjPath != "" {
			m.wp.terminal(m.menuProjPath)
		}
		return m, nil

	case "P":
		m.openPicker()
		return m, nil

	case "c":
		if name == "" {
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "cron " + n, placeholder: "--every 4h|--cron '0 9 * * 1'|--at +10m prompt…",
			argv: func(t string) []string {
				return append([]string{"cron", "add", n}, strings.Fields(t)...)
			},
		})
		return m, nil

	case "C":
		m.cronEntries = ppzScheduleList()
		m.cronSel = 0
		m.mode = "cron"
		return m, nil

	case "M":
		m.mode = "mesh"
		m.viewTitle = "pipes"
		m.viewBody = "…"
		return m, meshCmd()

	case "T":
		return m, runSelf("standup", "standup")

	case "i": // popup, not the sidebar — inboxes deserve more than 38 cols
		if name == "" {
			return m, nil
		}
		popupSelf("inbox " + name)
		// opening inbox means the user is reading it — advance cursor (#8)
		return m, markReadCmd(name)

	case "K":
		if name == "" {
			return m, nil
		}
		if sel.Remote {
			if sel.State != "dead" {
				m.status, m.statErr = name+" is a remote mesh agent — use X to clear offline ones", true
				return m, nil
			}
			// offline remote: offer to destroy its ppz source (#4)
			n := name
			m.openPrompt(promptSpec{
				label: "clear " + n + " from mesh", placeholder: "y to destroy its ppz source (permanent)",
				argv: func(t string) []string {
					if !strings.HasPrefix(t, "y") {
						return []string{"ls"}
					}
					return []string{"source-destroy", n}
				},
			})
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "kill " + n, placeholder: "y = kill (spec kept for resume) · see X to also remove",
			argv: func(t string) []string {
				args := []string{"kill", n}
				if !strings.HasPrefix(t, "y") {
					return []string{"ls"} // anything but y… = no-op
				}
				return args
			},
		})
		return m, nil

	case "X": // kill + remove spec in one step (#5)
		if name == "" {
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = name+" is a remote mesh agent — K to clear it from the mesh", true
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "kill+remove " + n, placeholder: "y to kill and wipe spec (use K to keep for resume)",
			argv: func(t string) []string {
				if !strings.HasPrefix(t, "y") {
					return []string{"ls"}
				}
				return []string{"kill", n, "--rm"}
			},
		})
		return m, nil

	case "r":
		if name == "" {
			return m, nil
		}
		if sel.Remote {
			m.status, m.statErr = name+" isn't a local agent — nothing to resume here", true
			return m, nil
		}
		return m, runSelf("resume", "resume", name)
	case "R":
		return m, runSelf("resume all", "resume", "--all")

	case "?":
		m.mode = "view"
		m.viewTitle = "keys"
		m.viewBody = helpText
		return m, nil
	}
	return m, nil
}

const helpText = `sidebar
 j/k ↑/↓  select agent   1-9 jump
 u        jump to who needs you
 /        filter fleet (esc clears)
 enter/l  type into the agent →
 e        recap: events, git, inbox
 f        fresh context (handoff→/clear)
 a / S    spawn (form)  P add project
 t        terminal in agent/space dir
 v        file menu (v then 1-9)
 V        open latest mentioned file
 click a project = room chat (#proj)
 right-click  menu — works on BOTH
          panes; esc/click closes
 z        zoom agent pane
 s / b    send msg / broadcast
 w        review handoff (reviewer role)
 D        done: merge worktree & clean
 T        standup — all agents report
 M        pipes: team, messages, setup
 K        kill (keep spec for resume)
 X        kill+remove (wipe spec too)
 r / R    resume sel / all
 i        inbox   c add schedule
 C        schedules (j/k nav, d delete)
 o        sort: attention ⇄ projects
 g        refresh   esc leave room
 d        leave (fleet keeps running)
 q        quit workspace

 ·ext = mesh-only agent (another
 machine): selecting auto-attaches
 live (Ctrl-\ detaches, Ctrl-C passes
 through) — enter/l skip the wait.
 s/i/c still work — rest
 is local-only

agent pane (right)
 click it or press enter, then type
 as normal — it IS the agent's
 terminal, not a copy.
 back here: click sidebar or prefix ←
 scrollback: prefix prefix [
 file splits close with q
 terminal strips: ctrl-d / exit

full map: docs/KEYMAP.md
every action is also a CLI: muster help`

// ---- view -------------------------------------------------------------------

func clip(s string, w int) string {
	if w <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func (m tuiModel) View() string {
	if m.w == 0 {
		return "loading…"
	}
	mesh := "pipes off"
	if m.meshOK {
		mesh = "pipes ok"
	}
	info := m.triageCounts() + " · " + mesh
	if p, end := m.fiveHr(); p > 0 { // account-wide, freshest agent wins
		info += fmt.Sprintf(" · 5h %d%%", int(p))
		if end != "" {
			info += "→" + end
		}
	}
	if m.filter != "" {
		info += " /" + m.filter
	}
	title := sTitle.Render(" muster ") + sDim.Render(clip(info, sidebarW-9))
	var mid string
	switch m.mode {
	case "form":
		mid = m.viewForm()
	case "pick":
		mid = m.viewPick()
	case "view", "mesh":
		mid = m.viewScroll()
	case "cron":
		mid = m.viewCron()
	default:
		mid = m.viewList() + "\n" + m.viewDetail() + "\n" + m.viewButtons()
	}
	return title + "\n" + mid + "\n" + m.viewBottom()
}

// meshBody renders the pipes view: status, team presence, recent traffic,
// schedules — the whole mesh legible at a glance. When the mesh is off it
// becomes the guided connect screen instead.
func meshBody() string {
	if !ppzReady() {
		return `pipes is OFF.

agents still run fine — but they can't
message each other, no schedules fire,
and standup/broadcast are offline.

pipes gives your team a mesh: agents
message each other BY NAME, you
schedule prompts that fire server-side
even while this machine sleeps.

connect:
 [1]  start the local daemon
 [2]  log in to pipescloud.io
      (interactive, runs in the
       agent pane on the right)

self-hosting? see ppz docs/self-hosting`
	}
	var b strings.Builder
	b.WriteString(ppzStatusText() + "\n")

	b.WriteString("\n" + sProj.Render("team") + "\n")
	specs, _ := listSpecs()
	ours := map[string]string{} // handle → role
	for _, s := range specs {
		if s.PpzHandle != "" {
			ours[s.PpzHandle] = s.Role
		}
	}
	who := ppzWho()
	if len(who) == 0 {
		b.WriteString(sDim.Render(" nobody on the mesh yet") + "\n")
	}
	handles := make([]string, 0, len(who))
	for h := range who {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	for _, h := range handles {
		hb := who[h]
		state := hb.Status
		if hb.State != "" {
			state += "·" + hb.State
		}
		line := fmt.Sprintf(" %-12s %-14s", clip(h, 12), state)
		if role := ours[h]; role != "" {
			line += clip(role, sidebarW-len([]rune(line))-2)
		} else if _, mine := ours[h]; !mine {
			line += "·ext" // on the mesh, but not one of muster's agents
		}
		b.WriteString(clip(line, sidebarW-2) + "\n")
	}

	b.WriteString("\n" + sProj.Render("recent messages → you (mstrctl)") + "\n")
	msgs := ppzReread(ctlHandle+".inbox", "6h")
	shown := 0
	for i := len(msgs) - 1; i >= 0 && shown < 8; i-- {
		e := msgs[i]
		if e.Subject == "ack:read" || e.Sender == "" {
			continue
		}
		ts := ""
		if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
			ts = t.Local().Format("15:04")
		}
		b.WriteString(clip(fmt.Sprintf(" %s %s: %s", ts, e.Sender, firstLine(e.Payload)), sidebarW-2) + "\n")
		shown++
	}
	if shown == 0 {
		b.WriteString(sDim.Render(" none in the last 6h — try T (standup)") + "\n")
	}

	b.WriteString("\n" + sProj.Render("schedules") + "\n")
	sch := ppzScheduleText()
	if sch == "" || strings.HasPrefix(sch, "ID") && strings.Count(sch, "\n") == 0 {
		sch = sDim.Render(" none — c on an agent creates one")
	}
	b.WriteString(sch + "\n")
	b.WriteString("\n" + sDim.Render("msgs cap 64KiB · history 24h · T standup"))
	return b.String()
}

// triageCounts is the header's who-needs-me glance: counts per state, worst
// first, glyphs only ("✋1 ⌛1 ⚙3"). One look answers the fleet's #1 question.
func (m tuiModel) triageCounts() string {
	counts := map[string]int{}
	for _, r := range m.rows {
		counts[r.State]++
	}
	var parts []string
	for _, st := range []string{"blocked", "stalled", "working", "idle", "unknown", "dead"} {
		n := counts[st]
		if st == "dead" {
			n += counts["ended"]
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s%d", stateGlyph(st), n))
		}
	}
	if len(parts) == 0 {
		return "0 agents"
	}
	return strings.Join(parts, " ")
}

// fiveHr returns the account-wide 5h rate-limit window from whichever agent
// reported it (it's per account, not per agent — any reporter is fine).
func (m tuiModel) fiveHr() (pct float64, end string) {
	for _, r := range m.rows {
		if r.FivePct > pct {
			pct, end = r.FivePct, r.FiveEnd
		}
	}
	return
}

func (m tuiModel) viewList() string {
	h := m.listH()
	lines := make([]string, 0, h)
	if len(m.items) == 0 {
		if m.filter != "" {
			lines = append(lines, "", sDim.Render("  nothing matches /"+clip(m.filter, sidebarW-22)), "", sDim.Render("  esc clears the filter"))
		} else {
			lines = append(lines, "", sDim.Render("  no agents yet"), "", sDim.Render("  press a (or click + agent)"), sDim.Render("  to spawn your first one"))
		}
	}
	for i := m.scroll; i < len(m.items) && len(lines) < h; i++ {
		lines = append(lines, m.renderItem(i))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) renderItem(i int) string {
	it := m.items[i]
	w := sidebarW - 1
	if it.kind == "proj" {
		name := it.projName
		if name == "" {
			name = "workspace"
		}
		label := clip(name, w-8)
		mark := ""
		if it.projPath != "" && it.projPath == m.space {
			mark = " " + sStatus.Render("●") // the space you opened muster in
		}
		pad := w - len([]rune(label)) - 4
		if mark != "" {
			pad -= 2
		}
		if pad < 1 {
			pad = 1
		}
		// highlight the active room's project header (#3)
		if it.projName != "" && it.projName == m.roomView {
			full := "▍" + label + mark + strings.Repeat(" ", pad) + "+"
			return sProjSel.Render(fmt.Sprintf("%-*s", w, full))
		}
		// per-project colour (#6)
		pc := lipgloss.NewStyle().Bold(true).Foreground(projColor(it.projName))
		return pc.Render("▍"+label) + mark + strings.Repeat(" ", pad) + sDim.Render("+")
	}
	if it.kind == "sub" { // the agent's checkout, dim, under its row
		b := "⎇ " + it.row.Branch
		if it.row.Wt {
			b += " ·wt"
		}
		body := "   " + clip(b, w-4)
		if it.row.Name == m.selName {
			return sSelected.Render(fmt.Sprintf("%-*s", w, body))
		}
		return sDim.Render(body)
	}
	r := it.row
	// idle+unread → show as "has messages" rather than plain idle (#2)
	glyph := stateGlyph(r.State)
	if r.State == "idle" && r.Unread > 0 {
		glyph = "→"
	}
	// agents under a named project get extra indent to read as children (#7)
	indent := 1
	nameW := 13
	if it.projName != "" {
		indent = 2
		nameW = 12
	}
	name := clip(r.Name, nameW)
	unread := ""
	if r.Unread > 0 {
		unread = fmt.Sprintf("✉%d", r.Unread)
	}
	ctx := ""
	if r.CtxPct > 0 {
		ctx = fmt.Sprintf("%d%%", int(r.CtxPct))
	}
	ext := ""
	if r.Remote { // mesh-only agent, no local session — meshBody uses the same marker
		ext = "·ext"
	}
	right := strings.TrimSpace(strings.Join([]string{ext, unread, ctx, r.Age}, " "))
	rightW := w - indent - 1 - 1 - nameW - 1 // indent + glyph + sp + name + sp
	body := fmt.Sprintf("%*s%s %-*s %*s", indent, "", glyph, nameW, name, rightW, right)
	if r.Name == m.selName {
		return sSelected.Render(clip(body, w))
	}
	line := strings.Repeat(" ", indent) + stateStyle(r.State).Render(glyph) + " " + fmt.Sprintf("%-*s ", nameW, name)
	rightStyle := sDim
	switch {
	case unread != "":
		rightStyle = lipgloss.NewStyle().Foreground(cWorking)
	case r.CtxPct >= 80:
		rightStyle = sErr
	case r.CtxPct >= 60:
		rightStyle = lipgloss.NewStyle().Foreground(cWorking)
	}
	line += rightStyle.Render(fmt.Sprintf("%*s", rightW, right))
	return line
}

// viewDetail: one fact per line so the glance works — who/state, harness
// numbers (or the blocked reason, promoted), where, charter.
func (m tuiModel) viewDetail() string {
	w := sidebarW - 2
	sep := sDim.Render(strings.Repeat("─", w))
	r := m.selected()
	if r == nil {
		return sep + "\n\n\n\n"
	}
	ageSuffix := ""
	if r.Age != "" {
		ageSuffix = " · " + r.Age
	}
	l1 := " " + stateStyle(r.State).Bold(true).Render(stateGlyph(r.State)+" "+clip(r.Name, 18)) +
		sDim.Render("  "+r.State+ageSuffix)
	var l2 string
	switch {
	case r.Reason != "" && r.Reason != "heartbeat":
		l2 = " " + sErr.Render(clip("✋ "+r.Reason, w-1))
	case r.Model != "":
		info := fmt.Sprintf("%s · ctx %d%%", r.Model, int(r.CtxPct))
		if r.Unread > 0 {
			info += fmt.Sprintf(" · ✉ %d", r.Unread)
		}
		l2 = " " + sDim.Render(clip(info, w-1))
	case r.Harness != "":
		l2 = " " + sDim.Render(r.Harness)
	default:
		l2 = " " + sDim.Render("shell")
	}
	var l3, l4 string
	if r.Remote {
		host := r.Host
		if host == "" {
			host = "mesh"
		}
		l3 = " " + sDim.Render(clip("·ext — on "+host+", no local session", w-1))
		l4 = " " + sDim.Render("auto-attaches (Ctrl-\\ detach) · s send")
	} else {
		dir := collapseHome(r.Dir)
		if r.Branch != "" {
			dir += "  ⎇ " + r.Branch
		}
		l3 = " " + sDim.Render(clip(dir, w-1))
		l4 = " " + sDim.Render(clip("$ "+r.Cmd, w-1))
		if r.Role != "" {
			l4 = " " + sDim.Render(clip("★ "+r.Role, w-1)) // the charter beats the argv
		}
	}
	return sep + "\n" + l1 + "\n" + l2 + "\n" + l3 + "\n" + l4
}

// button extents are fixed: " [+ agent] [+ project] [term] "
const btnAgent = "[+ agent]"
const btnProject = "[+ project]"
const btnTerm = "[term]"

func (m tuiModel) viewButtons() string {
	return " " + sButton.Render(btnAgent) + " " + sButton.Render(btnProject) + " " + sButton.Render(btnTerm)
}

func hitButton(x int) string {
	a0, a1 := 1, 1+len(btnAgent)
	p0, p1 := a1+1, a1+1+len(btnProject)
	t0, t1 := p1+1, p1+1+len(btnTerm)
	switch {
	case x >= a0 && x < a1:
		return "agent"
	case x >= p0 && x < p1:
		return "project"
	case x >= t0 && x < t1:
		return "term"
	}
	return ""
}

func (m tuiModel) viewForm() string {
	f := m.form
	h := m.listH() + detailH + 1 // replaces list+detail+buttons
	lines := []string{sTitle.Render(" " + f.title)}
	for i := range f.fields {
		fl := &f.fields[i]
		cursor := "  "
		lab := sDim.Render(fl.label)
		if i == f.focus {
			cursor = sTitle.Render("› ")
			lab = sField.Render(fl.label)
		}
		lines = append(lines, cursor+lab)
		if fl.sel != nil {
			v := fl.sel[fl.selIx]
			if i == f.focus {
				lines = append(lines, "  ◂ "+sField.Render(clip(v, sidebarW-10))+" ▸")
			} else {
				lines = append(lines, "    "+clip(v, sidebarW-8))
			}
		} else {
			lines = append(lines, "  "+fl.ti.View())
		}
	}
	lines = append(lines, "", sDim.Render("  "+f.fields[f.focus].hint))
	verb := "spawn"
	if f.kind == "project" {
		verb = "add"
	}
	lines = append(lines, "", sHelp.Render(" enter "+verb+" · tab next · esc cancel"))
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

// viewPick renders the project picker: filter input + discovered repos.
// Rows start at screen y=4 (title 1, input 2, blank 3) — keep pickRowAt in sync.
func (m tuiModel) viewPick() string {
	h := m.listH() + detailH + 1 // replaces list+detail+buttons
	lines := []string{sTitle.Render(" add project"), "  " + m.pickInput.View(), ""}
	f := m.pickFiltered()
	maxRows := h - 5
	if len(f) == 0 {
		lines = append(lines, sDim.Render("  no unregistered repos found"), "", sDim.Render("  paste a path above, or set"), sDim.Render("  MUSTER_REPO_ROOTS (colon-sep)"))
	}
	for i, r := range f {
		if i >= maxRows {
			lines = append(lines, sDim.Render(fmt.Sprintf("  … %d more — type to filter", len(f)-maxRows)))
			break
		}
		name := fmt.Sprintf("%-18s", clip(filepath.Base(r), 18))
		dir := clip(collapseHome(filepath.Dir(r)), sidebarW-22)
		if i == m.pickSel {
			lines = append(lines, sSelected.Render(clip(" "+name+" "+dir, sidebarW-1)))
		} else {
			lines = append(lines, " "+name+" "+sDim.Render(dir))
		}
	}
	lines = append(lines, "", sHelp.Render(" ↑/↓ + enter (or click) · esc cancel"))
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// pickRowAt maps a screen row to a filtered-repo index (see viewPick layout).
func (m tuiModel) pickRowAt(y int) int {
	i := y - 4
	if i >= 0 && i < len(m.pickFiltered()) {
		return i
	}
	return -1
}

// formFieldAt maps a screen row to a form field index (label or input line).
func (m tuiModel) formFieldAt(y int) int {
	i := (y - 2) / 2 // title on y=1, each field = 2 lines
	if m.form != nil && i >= 0 && i < len(m.form.fields) {
		return i
	}
	return -1
}

// cronNextRel formats a ppz next_at timestamp as a human relative string.
func cronNextRel(nextAt string) string {
	if nextAt == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, nextAt)
	if err != nil {
		return nextAt[:min(len(nextAt), 16)]
	}
	d := time.Until(t)
	if d < 0 {
		return "past"
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("in %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("in %dd", int(d.Hours()/24))
	}
}

func (m tuiModel) viewCron() string {
	h := m.listH() + detailH + 1
	lines := []string{sTitle.Render(" schedules")}
	if len(m.cronEntries) == 0 {
		lines = append(lines, "", sDim.Render("  no schedules — use c to add one"))
	} else {
		for i, e := range m.cronEntries {
			target := clip(e.Handle+"."+e.Pipe, 14)
			next := cronNextRel(e.NextAt)
			row := fmt.Sprintf(" %-14s %-7s  %s", target, next, clip(e.Payload, sidebarW-26))
			if i == m.cronSel {
				lines = append(lines, sSelected.Render(fmt.Sprintf("%-*s", sidebarW-1, ">"+row)))
			} else {
				lines = append(lines, " "+row)
			}
		}
		if m.cronSel < len(m.cronEntries) {
			e := m.cronEntries[m.cronSel]
			lines = append(lines, "", sDim.Render(" id: "+e.ID+"  "+e.Schedule+" "+e.Spec))
			lines = append(lines, sDim.Render(" "+clip(e.Payload, sidebarW-2)))
		}
	}
	lines = append(lines, "", sHelp.Render(" j/k nav · d delete · esc back"))
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines[:h], "\n")
}

func (m tuiModel) viewScroll() string {
	h := m.listH() + detailH + 1 // replaces list+detail+buttons
	lines := []string{sTitle.Render(" " + m.viewTitle)}
	for _, l := range strings.Split(m.viewBody, "\n") {
		lines = append(lines, clip(l, sidebarW-1))
	}
	lines = append(lines, "", sHelp.Render(" esc back"))
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) viewBottom() string {
	if m.mode == "prompt" {
		return sTitle.Render(" "+m.prompt.label+" › ") + m.input.View() + "\n" + sHelp.Render(" enter send · esc cancel")
	}
	if m.mode == "filter" {
		return sTitle.Render(" / ") + m.input.View() + "\n" + sHelp.Render(" enter keep filter · esc clear")
	}
	st := m.status
	style := sStatus
	if m.statErr {
		style = sErr
	}
	help := sHelp.Render(" a spawn · t term · enter type · ? keys")
	return style.Render(" "+clip(st, sidebarW-1)) + "\n" + help
}

func cmdTUI(args []string) int {
	if !isEmbedded() {
		return bootstrapWorkspace()
	}
	p := tea.NewProgram(newTUI(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	final, err := p.Run()
	if err != nil {
		return fail(err)
	}
	if fm, ok := final.(tuiModel); ok && fm.quitKill {
		quitWorkspace()
	}
	return 0
}
