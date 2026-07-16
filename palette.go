package main

// The dispatch palette: the "get it out of my head" surface. One key (;)
// opens a full-width popup with a single focused input — you type the TASK
// first; routing is inline tokens (all optional), and a live preview shows
// exactly what enter will do before you commit. The deliberate inverse of
// the spawn form: work first, routing second, decisions defaulted.
//
// Runs as its own tiny bubbletea program inside a tmux display-popup
// (`muster palette`), so the sidebar never crowds and the input gets real
// estate. Same binary, same dispatch functions the CLI uses — the preview
// calls resolveDispatch, enter calls executeDispatch; what you see is what
// runs.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type paletteModel struct {
	input     textarea.Model
	rows      []lsRow
	templates []Template
	projects  []Project
	space     string
	target    string // locked target (F on an agent): every task goes here
	w, h      int
	done      bool
	results   []string
	errs      []string
}

var (
	pTitle = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	pDim   = lipgloss.NewStyle().Foreground(cDim)
	pOK    = lipgloss.NewStyle().Foreground(cIdle)
	pWarn  = lipgloss.NewStyle().Foreground(cStalled)
	pBad   = lipgloss.NewStyle().Foreground(cBlocked)
)

func newPalette(target string) paletteModel {
	ta := textarea.New()
	ta.Placeholder = "task…   @agent/@template  !model  /skill  #project   ('- ' bullets split an epic)"
	if target != "" {
		ta.Placeholder = "new task for " + target + " — fresh seeded context…"
	}
	ta.CharLimit = 8192
	ta.SetHeight(5)
	ta.ShowLineNumbers = false
	ta.Focus()
	rows, _ := gatherRows()
	return paletteModel{
		input:     ta,
		rows:      rows,
		templates: loadTemplates(),
		projects:  loadProjects(),
		space:     currentSpace(),
		target:    target,
	}
}

func (m paletteModel) Init() tea.Cmd { return textarea.Blink }

// tasks parses the current input, applying the locked target if any.
func (m paletteModel) tasks() []dispatchTask {
	text := m.input.Value()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	ts := parseDispatch(text)
	if m.target != "" {
		for i := range ts {
			ts[i].Target = m.target
		}
	}
	return ts
}

func (m paletteModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(msg.Width - 4)
		return m, nil
	case tea.KeyMsg:
		if m.done {
			return m, tea.Quit // any key closes the result screen
		}
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "ctrl+j":
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return m, cmd
		case "enter":
			tasks := m.tasks()
			if len(tasks) == 0 {
				return m, tea.Quit
			}
			for _, t := range tasks {
				act := resolveDispatch(t, m.rows, m.templates, m.projects, m.space)
				out, err := executeDispatch(act, m.projects, m.space)
				if err != nil {
					m.errs = append(m.errs, err.Error())
				} else {
					m.results = append(m.results, out)
				}
			}
			m.done = true
			if len(m.errs) == 0 {
				// all good — flash the result and close by itself
				return m, tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return tea.Quit() })
			}
			return m, nil // something refused — stay up until a key
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m paletteModel) View() string {
	if m.w == 0 {
		return ""
	}
	var b strings.Builder
	title := " ⚡ dispatch"
	if m.target != "" {
		title = " ⚡ retask " + m.target
	}
	b.WriteString(pTitle.Render(title) + "\n\n")
	if m.done {
		for _, r := range m.results {
			b.WriteString(pOK.Render(" → "+r) + "\n")
		}
		for _, e := range m.errs {
			b.WriteString(pBad.Render(" ✗ "+e) + "\n")
		}
		if len(m.errs) > 0 {
			b.WriteString("\n" + pDim.Render(" any key closes"))
		}
		return b.String()
	}
	b.WriteString(m.input.View() + "\n\n")
	// live routing preview: one line per task, exactly what enter will run
	tasks := m.tasks()
	if len(tasks) == 0 {
		b.WriteString(pDim.Render(" type the task first — routing comes after (or never: defaults work)") + "\n")
	}
	shown := 0
	for _, t := range tasks {
		if shown >= 6 {
			b.WriteString(pDim.Render(fmt.Sprintf(" … %d more", len(tasks)-shown)) + "\n")
			break
		}
		act := resolveDispatch(t, m.rows, m.templates, m.projects, m.space)
		line := previewLine(act)
		switch act.Kind {
		case "refuse":
			b.WriteString(pWarn.Render(" → "+line) + "\n")
		default:
			b.WriteString(pOK.Render(" → "+line) + "\n")
		}
		shown++
	}
	b.WriteString("\n" + pDim.Render(" enter dispatch · ^J newline · esc cancel"))
	return b.String()
}

// previewLine renders one resolved action for the preview: target · what
// happens · model/skill — transparent enough that a mistake is visible
// before enter.
func previewLine(act dispatchAction) string {
	t := act.Task
	var parts []string
	switch act.Kind {
	case "ask":
		model := t.Model
		if model == "" {
			model = askModel()
		}
		parts = append(parts, "ask · "+model, act.Why)
	case "retask":
		parts = append(parts, "retask "+act.Agent, act.Why)
	case "claim":
		parts = append(parts, "claim "+act.Agent, act.Why)
	case "send":
		parts = append(parts, "queue → "+act.Agent, act.Why)
	case "spawn":
		parts = append(parts, "hire "+act.Template+"-•", act.Why)
	case "refuse":
		parts = append(parts, "?? ", act.Why)
	}
	if t.Skill != "" {
		parts = append(parts, "/"+t.Skill)
	}
	if t.Model != "" && act.Kind != "ask" {
		parts = append(parts, t.Model)
	}
	line := strings.Join(parts, " · ")
	if t.Text != "" {
		line += pDim.Render("  « " + clip(t.Text, 30))
	}
	return line
}

func cmdPalette(args []string) int {
	target := ""
	if len(args) == 2 && args[0] == "--target" {
		target = args[1]
	}
	p := tea.NewProgram(newPalette(target), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fail(err)
	}
	return 0
}
