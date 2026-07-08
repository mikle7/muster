package main

import (
	"fmt"
	"os/exec"
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
	case "blocked":
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
	seq      int
}
type execDoneMsg struct {
	label string
	out   string
	err   error
}

func refreshCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		rows, _ := gatherRows()
		return tickMsg{rows: rows, projects: loadProjects(), seq: seq}
	}
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
	kind     string // "proj" | "agent"
	projName string // proj: display name ("" = unassigned bucket)
	projPath string
	row      lsRow // agent
}

func buildItems(rows []lsRow, projects []Project) []sideItem {
	byProj := map[string][]lsRow{}
	for _, r := range rows {
		p := projectFor(projects, r)
		byProj[p] = append(byProj[p], r)
	}
	var items []sideItem
	for _, p := range projects {
		items = append(items, sideItem{kind: "proj", projName: p.Name, projPath: p.Path})
		for _, r := range byProj[p.Name] {
			items = append(items, sideItem{kind: "agent", row: r, projName: p.Name, projPath: p.Path})
		}
	}
	if loose := byProj[""]; len(loose) > 0 {
		if len(projects) > 0 {
			items = append(items, sideItem{kind: "proj", projName: ""})
		}
		for _, r := range loose {
			items = append(items, sideItem{kind: "agent", row: r})
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

func newSpawnForm(projects []Project, preselect string) *uiForm {
	sel := []string{"(none — spawn in cwd)"}
	ix := 0
	for i, p := range projects {
		sel = append(sel, p.Name)
		if p.Name == preselect {
			ix = i + 1
		}
	}
	f := &uiForm{kind: "spawn", title: "new agent", fields: []ffield{
		textField("name", "e.g. fixer", "lowercase, digits, dashes"),
		{label: "project", sel: sel, selIx: ix, hint: "←/→ to change"},
		textField("branch", "(optional)", "creates a git worktree for it"),
		textField("command", "claude (default)", "the agent's exact command"),
	}}
	f.setFocus(0)
	return f
}

func newProjectForm() *uiForm {
	f := &uiForm{kind: "project", title: "add project", fields: []ffield{
		textField("path", "~/Repos/my-app", "repo or directory"),
		textField("name", "(basename)", "shown in the sidebar"),
	}}
	f.setFocus(0)
	return f
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
	items    []sideItem
	selName  string
	scroll   int
	w, h     int

	wp workspacePanes

	mode      string // normal | prompt | form | view
	prompt    promptSpec
	input     textinput.Model
	form      *uiForm
	viewTitle string
	viewBody  string

	status        string
	statErr       bool
	seq           int
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
	m.ensureVisible(ring[next])
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

// retarget points the workspace's agent pane at the current selection.
func (m *tuiModel) retarget() {
	if r := m.selected(); r != nil {
		m.wp.retarget(r.Name, r.Tmux, r.State)
	} else {
		m.wp.retarget("", "", "")
	}
}

func (m *tuiModel) rebuild() {
	m.items = buildItems(m.rows, m.projects)
	ring := m.agentIdxs()
	if m.pendingSelect != "" {
		for _, idx := range ring {
			if m.items[idx].row.Name == m.pendingSelect {
				m.selName = m.pendingSelect
				m.pendingSelect = ""
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
			m.seq++
			return m, tea.Batch(refreshCmd(m.seq), tickEvery())
		}
		if msg.seq != m.seq { // stale refresh
			return m, nil
		}
		m.rows, m.projects = msg.rows, msg.projects
		m.rebuild()
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out+" "+msg.err.Error()), true
		} else {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out), false
			if msg.label == "inbox" || msg.label == "cron ls" {
				m.mode, m.viewTitle, m.viewBody = "view", msg.label, msg.out
				if msg.label == "cron ls" {
					m.viewTitle = "schedules"
				}
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
		case "view":
			switch msg.String() {
			case "esc", "q", "enter":
				m.mode = "normal"
			}
			return m, nil
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
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || m.mode == "view" {
		return m, nil
	}
	if m.mode == "form" {
		// click a field line to focus it
		if i := m.formFieldAt(msg.Y); i >= 0 {
			m.form.setFocus(i)
		}
		return m, nil
	}
	// list rows
	if y := msg.Y - m.listTop(); y >= 0 && y < m.listH() {
		idx := m.scroll + y
		if idx >= 0 && idx < len(m.items) {
			switch it := m.items[idx]; it.kind {
			case "agent":
				m.selName = it.row.Name
				m.retarget()
			case "proj":
				m.form = newSpawnForm(m.projects, it.projName)
				m.mode = "form"
			}
		}
		return m, nil
	}
	// buttons
	if msg.Y == m.btnY() {
		switch hitButton(msg.X) {
		case "agent":
			r := m.selected()
			pre := ""
			if r != nil {
				pre = projectFor(m.projects, *r)
			}
			m.form = newSpawnForm(m.projects, pre)
			m.mode = "form"
		case "project":
			m.form = newProjectForm()
			m.mode = "form"
		}
	}
	return m, nil
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

func (m tuiModel) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	switch f.kind {
	case "spawn":
		name, branch, cmdline := f.val(0), f.val(2), f.val(3)
		projPath := ""
		if ix := f.fields[1].selIx; ix > 0 {
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
	case "project":
		path, name := f.val(0), f.val(1)
		if path == "" {
			m.status, m.statErr = "path is required", true
			return m, nil
		}
		argv := []string{"project", "add", path}
		if name != "" {
			argv = append(argv, "--name", name)
		}
		m.mode = "normal"
		return m, runSelf("project add", argv...)
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
		pre := ""
		if sel != nil {
			pre = projectFor(m.projects, *sel)
		}
		m.form = newSpawnForm(m.projects, pre)
		m.mode = "form"
		return m, nil

	case "P":
		m.form = newProjectForm()
		m.mode = "form"
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

	case "i":
		if name == "" {
			return m, nil
		}
		return m, runSelf("inbox", "inbox", name)

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
 j/k ↑/↓  select agent
 enter/l  type into the agent →
 S or a   spawn (form)
 P        add project
 s / b    send msg / broadcast
 K        kill (y / y --rm)
 r / R    resume sel / all
 i        inbox   c/C schedule/list
 g        refresh
 d        leave (fleet keeps running)
 q        quit workspace

agent pane (right)
 click it or press enter, then type
 as normal — it IS the agent's
 terminal, not a copy.
 back here: click sidebar or C-b ←
 scrollback: C-b C-b [

every action is also a CLI:
 muster help`

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
	title := sTitle.Render(" muster ") + sDim.Render(fmt.Sprintf("%d agents · mesh %s", len(m.rows), meshWord()))
	var mid string
	switch m.mode {
	case "form":
		mid = m.viewForm()
	case "view":
		mid = m.viewScroll()
	default:
		mid = m.viewList() + "\n" + m.viewDetail() + "\n" + m.viewButtons()
	}
	return title + "\n" + mid + "\n" + m.viewBottom()
}

var meshOK *bool

// meshWord caches ppzReady for the process lifetime — the TUI hits View
// often and ppz status is a subprocess call.
func meshWord() string {
	if meshOK == nil {
		v := ppzReady()
		meshOK = &v
	}
	if *meshOK {
		return "ok"
	}
	return "off"
}

func (m tuiModel) viewList() string {
	h := m.listH()
	lines := make([]string, 0, h)
	if len(m.items) == 0 {
		lines = append(lines, "", sDim.Render("  no agents yet"), "", sDim.Render("  press a (or click + agent)"), sDim.Render("  to spawn your first one"))
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
		label := clip(name, w-6)
		pad := w - len([]rune(label)) - 4
		if pad < 1 {
			pad = 1
		}
		return sProj.Render("▍"+label) + strings.Repeat(" ", pad) + sDim.Render("+")
	}
	r := it.row
	glyph := stateGlyph(r.State)
	name := clip(r.Name, 16)
	unread := ""
	if r.Unread > 0 {
		unread = fmt.Sprintf("✉%d", r.Unread)
	}
	right := strings.TrimSpace(unread + " " + r.Age)
	body := fmt.Sprintf(" %s %-16s %*s", glyph, name, w-22, right)
	if r.Name == m.selName {
		return sSelected.Render(clip(body, w))
	}
	line := " " + stateStyle(r.State).Render(glyph) + " " + fmt.Sprintf("%-16s ", name)
	if unread != "" {
		line += lipgloss.NewStyle().Foreground(cWorking).Render(fmt.Sprintf("%*s", w-22, right))
	} else {
		line += sDim.Render(fmt.Sprintf("%*s", w-22, right))
	}
	return line
}

func (m tuiModel) viewDetail() string {
	w := sidebarW - 2
	sep := sDim.Render(strings.Repeat("─", w))
	r := m.selected()
	if r == nil {
		return sep + "\n\n\n\n"
	}
	harness := r.Harness
	if harness == "" {
		harness = "shell"
	}
	l1 := stateStyle(r.State).Bold(true).Render(clip(r.Name, 18)) + sDim.Render(" · "+r.State+" · "+harness)
	l2 := sDim.Render(clip(collapseHome(r.Dir), w))
	l3 := ""
	if r.Branch != "" {
		l3 = sDim.Render(clip("⎇ "+r.Branch, w))
	}
	l4 := sDim.Render(clip("$ "+r.Cmd, w))
	if r.Reason != "" && r.Reason != "heartbeat" {
		l3 = sErr.Render(clip(r.Reason, w))
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
