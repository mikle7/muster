package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The TUI is a thin shell over muster's own CLI: every action key
// self-execs `muster <cmd> ...` so the UI and CLI can never disagree.
// The pane preview is tmux capture-pane; attach is switch-client (inside
// tmux) or a suspended `tmux attach` (outside). No PTY ownership, ever.

const sidebarW = 30

var (
	cWorking = lipgloss.AdaptiveColor{Light: "166", Dark: "214"}
	cBlocked = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	cIdle    = lipgloss.AdaptiveColor{Light: "28", Dark: "78"}
	cDead    = lipgloss.AdaptiveColor{Light: "244", Dark: "242"}
	cAccent  = lipgloss.AdaptiveColor{Light: "61", Dark: "141"}
	cDim     = lipgloss.AdaptiveColor{Light: "245", Dark: "243"}

	sTitle    = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sDim      = lipgloss.NewStyle().Foreground(cDim)
	sSelected = lipgloss.NewStyle().Bold(true).Background(cAccent).Foreground(lipgloss.AdaptiveColor{Light: "255", Dark: "232"})
	sHelp     = lipgloss.NewStyle().Foreground(cDim)
	sStatus   = lipgloss.NewStyle().Foreground(cIdle)
	sErr      = lipgloss.NewStyle().Foreground(cBlocked)
	sBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cDim)
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

type tickMsg struct {
	rows    []lsRow
	preview string
	seq     int
}
type execDoneMsg struct {
	label string
	out   string
	err   error
}

type promptSpec struct {
	label       string
	placeholder string
	// build the muster CLI argv from the submitted text
	argv func(text string) []string
}

type tuiModel struct {
	rows    []lsRow
	sel     int
	preview string
	w, h    int

	mode      string // normal | prompt | view
	prompt    promptSpec
	input     textinput.Model
	viewTitle string
	viewBody  string

	status  string
	statErr bool
	seq     int
}

func selName(m *tuiModel) string {
	if m.sel >= 0 && m.sel < len(m.rows) {
		return m.rows[m.sel].Name
	}
	return ""
}

func refreshCmd(sel string, seq, lines int) tea.Cmd {
	return func() tea.Msg {
		rows, _ := gatherRows()
		prev := ""
		for _, r := range rows {
			if r.Name == sel && r.State != "dead" {
				out, err := tmuxRun("capture-pane", "-t", "="+r.Tmux+":", "-p")
				if err == nil {
					prev = tailLines(out, lines)
				}
			}
		}
		return tickMsg{rows: rows, preview: prev, seq: seq}
	}
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	// drop trailing blank region tmux pads panes with
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:end], "\n")
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

func newTUI() tuiModel {
	ti := textinput.New()
	ti.CharLimit = 4096
	ti.Width = 120
	return tuiModel{mode: "normal", status: "muster — j/k move · ? help", input: ti}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(refreshCmd("", 0, 40), tickEvery())
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		if msg.seq == -1 { // timer fired: kick a real refresh
			m.seq++
			return m, tea.Batch(refreshCmd(selName(&m), m.seq, m.previewLines()), tickEvery())
		}
		if msg.seq != m.seq { // stale refresh
			return m, nil
		}
		m.rows = msg.rows
		if m.sel >= len(m.rows) {
			m.sel = len(m.rows) - 1
		}
		if m.sel < 0 {
			m.sel = 0
		}
		m.preview = msg.preview
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out+" "+msg.err.Error()), true
		} else {
			m.status, m.statErr = msg.label+": "+firstLine(msg.out), false
			if msg.label == "inbox" || msg.label == "cron ls" {
				m.mode, m.viewTitle, m.viewBody = "view", msg.label+" — "+selName(&m), msg.out
				if msg.label == "cron ls" {
					m.viewTitle = "schedules"
				}
			}
		}
		m.seq++
		return m, refreshCmd(selName(&m), m.seq, m.previewLines())

	case tea.KeyMsg:
		switch m.mode {
		case "prompt":
			return m.updatePrompt(msg)
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

func (m *tuiModel) openPrompt(p promptSpec) {
	m.mode = "prompt"
	m.prompt = p
	m.input.Placeholder = p.placeholder
	m.input.SetValue("")
	m.input.Focus()
}

func (m tuiModel) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := selName(&m)
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.sel < len(m.rows)-1 {
			m.sel++
		}
		m.seq++
		return m, refreshCmd(selName(&m), m.seq, m.previewLines())
	case "k", "up":
		if m.sel > 0 {
			m.sel--
		}
		m.seq++
		return m, refreshCmd(selName(&m), m.seq, m.previewLines())

	case "g":
		m.seq++
		return m, refreshCmd(name, m.seq, m.previewLines())

	case "enter":
		if name == "" {
			return m, nil
		}
		sess := "=" + m.rows[m.sel].Tmux
		if m.rows[m.sel].State == "dead" {
			return m, runSelf("resume", "resume", name)
		}
		if os.Getenv("TMUX") != "" {
			out, err := tmuxRun("switch-client", "-t", sess)
			if err != nil {
				m.status, m.statErr = "switch-client: "+out, true
			}
			return m, nil
		}
		// standalone: hand the terminal to tmux attach, come back on detach
		argv := []string{tmuxBin()}
		if extra := os.Getenv("MUSTER_TMUX_ARGS"); extra != "" {
			argv = append(argv, strings.Fields(extra)...)
		}
		argv = append(argv, "attach", "-t", sess)
		c := exec.Command(argv[0], argv[1:]...)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			return execDoneMsg{label: "attach", out: "returned to muster", err: err}
		})

	case "s":
		if name == "" {
			return m, nil
		}
		n := name
		m.openPrompt(promptSpec{
			label: "send " + n, placeholder: "message… (delivered when agent is idle)",
			argv: func(t string) []string { return []string{"send", n, t} },
		})
		return m, nil

	case "b":
		m.openPrompt(promptSpec{
			label: "broadcast", placeholder: "message to every live agent…",
			argv: func(t string) []string { return []string{"broadcast", t} },
		})
		return m, nil

	case "S":
		m.openPrompt(promptSpec{
			label: "spawn", placeholder: "name [-C dir | --repo dir -b branch] [-- cmd…]",
			argv: func(t string) []string { return append([]string{"spawn"}, strings.Fields(t)...) },
		})
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

const helpText = `j/k or ↑/↓   select agent          enter   attach (resume if dead)
s            send message          b       broadcast to all
S            spawn agent           K       kill (asks y / y --rm)
r / R        resume sel / all      i       inbox (peek, no cursor move)
c / C        schedule / list       g       refresh now
?            this help             q       quit

prompts: enter submits · esc cancels
everything here is also a CLI: muster <cmd> — see muster help`

// boxH is the content height of the two main boxes: total height minus
// title(1), borders(2), status(1), help(1).
func (m tuiModel) boxH() int {
	n := m.h - 5
	if n < 5 {
		n = 5
	}
	return n
}

func (m tuiModel) previewLines() int {
	n := m.boxH() - 3 // head + info + separator
	if n < 5 {
		n = 5
	}
	return n
}

// clip truncates s to width w (rune-aware enough for our ASCII+glyph mix).
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

func clipLines(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = clip(lines[i], w)
	}
	return strings.Join(lines, "\n")
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
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.viewSidebar(), m.viewMain())
	bottom := m.viewBottom()
	title := sTitle.Render(" muster ") + sDim.Render("· "+fmt.Sprintf("%d agents", len(m.rows))+" · mesh "+meshWord())
	return title + "\n" + body + "\n" + bottom
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

func (m tuiModel) viewSidebar() string {
	h := m.boxH()
	var b strings.Builder
	if len(m.rows) == 0 {
		b.WriteString(sDim.Render("no agents\n\nS to spawn"))
	}
	for i, r := range m.rows {
		glyph := stateStyle(r.State).Render(stateGlyph(r.State))
		unread := ""
		if r.Unread > 0 {
			b := lipgloss.NewStyle().Foreground(cWorking)
			unread = b.Render(fmt.Sprintf(" ✉%d", r.Unread))
		}
		name := r.Name
		if len(name) > 14 {
			name = name[:13] + "…"
		}
		line1 := fmt.Sprintf("%s %-14s%s", glyph, name, unread)
		harness := r.Harness
		if harness == "" {
			harness = "shell"
		}
		line2 := "   " + sDim.Render(fmt.Sprintf("%s · %s · %s", r.State, harness, r.Age))
		if i == m.sel {
			line1 = sSelected.Render(fmt.Sprintf(" %s %-14s", stateGlyph(r.State), name)) + unread
		}
		b.WriteString(line1 + "\n" + line2 + "\n")
	}
	return sBorder.Width(sidebarW).Height(h).Render(strings.TrimRight(b.String(), "\n"))
}

func (m tuiModel) viewMain() string {
	w := m.w - sidebarW - 4
	if w < 20 {
		w = 20
	}
	h := m.boxH()

	if m.mode == "view" {
		return sBorder.Width(w).Height(h).Render(sTitle.Render(m.viewTitle) + "\n\n" + m.viewBody)
	}

	if len(m.rows) == 0 {
		return sBorder.Width(w).Height(h).Render(sDim.Render("spawn your first agent: press S\n\nname [-C dir | --repo dir -b branch] [-- cmd…]\ndefault cmd: $MUSTER_DEFAULT_CMD or claude"))
	}
	r := m.rows[m.sel]
	head := stateStyle(r.State).Bold(true).Render(r.Name) +
		sDim.Render(clip("  "+r.State+" · "+r.Cmd, w-len([]rune(r.Name))-2))
	infoTxt := clip(collapseHome(r.Dir), w-2)
	info := sDim.Render(infoTxt)
	if r.Reason != "" && r.Reason != "heartbeat" {
		info = sDim.Render(clip(infoTxt, w-len([]rune(r.Reason))-6)) + "  " + sErr.Render("("+clip(r.Reason, 40)+")")
	}
	sep := sDim.Render(strings.Repeat("─", max(0, w-2)))
	prev := clipLines(m.preview, w-2)
	if r.State == "dead" {
		prev = sDim.Render("agent is dead — enter or r to resume with its exact original command:\n\n  " + clip(r.Cmd, w-4))
	}
	return sBorder.Width(w).Height(h).Render(head + "\n" + info + "\n" + sep + "\n" + prev)
}

func (m tuiModel) viewBottom() string {
	if m.mode == "prompt" {
		return sTitle.Render(" "+m.prompt.label+" › ") + m.input.View()
	}
	st := m.status
	style := sStatus
	if m.statErr {
		style = sErr
	}
	help := sHelp.Render("enter attach · s send · S spawn · K kill · r/R resume · i inbox · c/C cron · b broadcast · ? help · q quit")
	return style.Render(" "+st) + "\n" + help
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func cmdTUI(args []string) int {
	p := tea.NewProgram(newTUI(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fail(err)
	}
	return 0
}
