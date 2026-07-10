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
	sSelected = lipgloss.NewStyle().Bold(true).Background(cAccent).Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "232"})
	sHelp     = lipgloss.NewStyle().Foreground(cDim)
	sStatus   = lipgloss.NewStyle().Foreground(cIdle)
	sErr      = lipgloss.NewStyle().Foreground(cBlocked)
	sButton   = lipgloss.NewStyle().Bold(true).Foreground(cAccent).Background(lipgloss.AdaptiveColor{Light: "254", Dark: "236"})
	sField    = lipgloss.NewStyle().Foreground(cAccent)
)

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
	sh := selfExe() + " " + argv + `; printf '\n[enter to close] '; read -r _`
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

	mode      string // normal | prompt | form | view
	prompt    promptSpec
	input     textinput.Model
	form      *uiForm
	viewTitle string
	viewBody  string

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
// unless a room chat owns it (cleared by selecting an agent or esc).
func (m *tuiModel) retarget() {
	if m.roomView != "" {
		m.wp.showRoom(m.roomView)
		return
	}
	if r := m.selected(); r != nil {
		m.wp.retarget(r.Name, r.Tmux, r.State)
	} else {
		m.wp.retarget("", "", "")
	}
}

func (m *tuiModel) rebuild() {
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
	m.retarget()
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
		m.retarget()
		return m, nil

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
		m.rebuild()
		m.maybeAutoRefresh()
		if m.mode == "mesh" { // keep the mesh view live (standup replies etc.)
			return m, meshCmd()
		}
		return m, nil

	case meshMsg:
		if m.mode == "mesh" {
			m.viewBody = msg.body
		}
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out+" "+msg.err.Error()), true
		} else {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out), false
			if msg.label == "cron ls" {
				m.mode, m.viewTitle, m.viewBody = "view", "schedules", msg.out
			}
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
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || m.mode == "view" {
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
		if idx >= 0 && idx < len(m.items) {
			switch it := m.items[idx]; it.kind {
			case "agent", "sub": // the branch line clicks like its agent
				m.selName = it.row.Name
				m.roomView = ""
				m.retarget()
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
		return m, nil
	}
	// buttons
	if msg.Y == m.btnY() {
		switch hitButton(msg.X) {
		case "agent":
			m.form = newSpawnForm(m.projects, m.spawnPreselect(), "")
			m.mode = "form"
		case "project":
			m.openPicker()
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
		m.retarget()
		menu := []string{"display-menu", "-T", " " + it.row.Name + " ", "-x", mx, "-y", my}
		if it.row.State == "dead" {
			menu = append(menu,
				"resume", "r", self+"r",
				"kill / remove…", "k", self+"K")
		} else {
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
			menu = append(menu, "kill…", "k", self+"K")
		}
		_, _ = tmuxRun(menu...)
		return m, nil
	case "proj":
		m.menuProj, m.menuProjPath = it.projName, it.projPath
		title := it.projName
		if title == "" {
			title = "unassigned"
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

// updateFilter: live fleet filtering. Every keystroke narrows the list;
// enter keeps the filter and returns to normal keys, esc clears it.
func (m tuiModel) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = "normal"
		m.filter = ""
		m.rebuild()
		m.status, m.statErr = "filter cleared", false
		return m, nil
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
	m.rebuild()
	return m, cmd
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
		m.retarget()
		return m, nil
	case "k", "up":
		m.moveSel(-1)
		m.retarget()
		return m, nil

	case "g":
		m.seq++
		return m, refreshCmd(m.seq)

	case "o":
		m.byPrio = !m.byPrio
		m.rebuild()
		if m.byPrio {
			m.status, m.statErr = "sorted by attention (blocked first) — o restores projects", false
		} else {
			m.status, m.statErr = "grouped by project", false
		}
		return m, nil

	case "enter", "l", "tab":
		if sel == nil {
			return m, nil
		}
		if sel.State == "dead" {
			return m, runSelf("resume", "resume", name)
		}
		m.wp.focus()
		return m, nil

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
			m.rebuild()
			m.status, m.statErr = "filter cleared", false
			return m, nil
		}
		if m.roomView != "" {
			m.roomView = ""
			m.retarget()
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
		m.retarget()
		return m, nil

	case "1", "2", "3", "4", "5", "6", "7", "8", "9": // jump to Nth agent
		n := int(msg.String()[0] - '1')
		ring := m.agentIdxs()
		if n < len(ring) {
			m.selName = m.items[ring[n]].row.Name
			m.roomView = ""
			m.ensureVisible(ring[n])
			m.retarget()
		}
		return m, nil

	case "e": // recap: the 10-second catch-up, full-width popup
		if name == "" {
			return m, nil
		}
		popupSelf("recap " + name)
		return m, nil

	case "f": // fresh context: flush handoff → /clear → re-inject ("R" = resume --all)
		if sel == nil || sel.State == "dead" {
			m.status, m.statErr = "select a live agent first", true
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
		if sel != nil {
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
		fileMenu(sel.Name, m.wp.right)
		return m, nil

	case "V": // open the most recently mentioned file — no menu
		if sel == nil || sel.State == "dead" {
			m.status, m.statErr = "select a live agent first", true
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
		return m, runSelf("cron ls", "cron", "ls")

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
		return m, nil

	case "K":
		if name == "" {
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "kill " + n, placeholder: "y = kill · y --rm = kill+remove worktree/spec",
			argv: func(t string) []string {
				args := []string{"kill", n}
				if strings.Contains(t, "--rm") {
					args = append(args, "--rm")
				}
				if strings.Contains(t, "--force") {
					args = append(args, "--force")
				}
				if !strings.HasPrefix(t, "y") {
					return []string{"ls"} // anything but y… = no-op
				}
				return args
			},
		})
		return m, nil

	case "r":
		if name == "" {
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
 K        kill (y / y --rm)
 r / R    resume sel / all
 i        inbox   c/C schedule/list
 o        sort: attention ⇄ projects
 g        refresh   esc leave room
 d        leave (fleet keeps running)
 q        quit workspace

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
			name = "unassigned"
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
		return sProj.Render("▍"+label) + mark + strings.Repeat(" ", pad) + sDim.Render("+")
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
	glyph := stateGlyph(r.State)
	name := clip(r.Name, 13)
	unread := ""
	if r.Unread > 0 {
		unread = fmt.Sprintf("✉%d", r.Unread)
	}
	ctx := ""
	if r.CtxPct > 0 {
		ctx = fmt.Sprintf("%d%%", int(r.CtxPct))
	}
	right := strings.TrimSpace(strings.Join([]string{unread, ctx, r.Age}, " "))
	body := fmt.Sprintf(" %s %-13s %*s", glyph, name, w-19, right)
	if r.Name == m.selName {
		return sSelected.Render(clip(body, w))
	}
	line := " " + stateStyle(r.State).Render(glyph) + " " + fmt.Sprintf("%-13s ", name)
	rightStyle := sDim
	switch {
	case unread != "":
		rightStyle = lipgloss.NewStyle().Foreground(cWorking)
	case r.CtxPct >= 80:
		rightStyle = sErr
	case r.CtxPct >= 60:
		rightStyle = lipgloss.NewStyle().Foreground(cWorking)
	}
	line += rightStyle.Render(fmt.Sprintf("%*s", w-19, right))
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
	l1 := " " + stateStyle(r.State).Bold(true).Render(stateGlyph(r.State)+" "+clip(r.Name, 18)) +
		sDim.Render("  "+r.State+" · "+r.Age)
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
	dir := collapseHome(r.Dir)
	if r.Branch != "" {
		dir += "  ⎇ " + r.Branch
	}
	l3 := " " + sDim.Render(clip(dir, w-1))
	l4 := " " + sDim.Render(clip("$ "+r.Cmd, w-1))
	if r.Role != "" {
		l4 = " " + sDim.Render(clip("★ "+r.Role, w-1)) // the charter beats the argv
	}
	return sep + "\n" + l1 + "\n" + l2 + "\n" + l3 + "\n" + l4
}

// button extents are fixed: " [+ agent] [+ project] "
const btnAgent = "[+ agent]"
const btnProject = "[+ project]"

func (m tuiModel) viewButtons() string {
	return " " + sButton.Render(btnAgent) + " " + sButton.Render(btnProject)
}

func hitButton(x int) string {
	a0, a1 := 1, 1+len(btnAgent)
	p0, p1 := a1+1, a1+1+len(btnProject)
	switch {
	case x >= a0 && x < a1:
		return "agent"
	case x >= p0 && x < p1:
		return "project"
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
	help := sHelp.Render(" a spawn · P project · enter type · ? keys")
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
