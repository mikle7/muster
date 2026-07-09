package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A room IS a project/space: its chat is the sum of its members' inboxes —
// every mesh message to any agent in the space (and to you from them),
// rendered as one conversation. Clicking a project in the sidebar shows it
// in the right pane (`muster room <project> --watch`). Read receipts (✓✓)
// come from ppz ack:read envelopes. A compose line at the bottom sends
// whatever you type to every member's ppz handle at once — a real group
// chat, not just a transcript + the T-key standup broadcast.

type roomMsg struct {
	t        time.Time
	from, to string
	text     string
	id       string
	read     bool
}

// roomAgents returns the specs grouped under proj (same matching as the sidebar).
func roomAgents(proj string) []*AgentSpec {
	ps := loadProjects()
	specs, _ := listSpecs()
	var out []*AgentSpec
	for _, s := range specs {
		if projectFor(ps, lsRow{Dir: s.Dir, Repo: s.Repo}) == proj {
			out = append(out, s)
		}
	}
	return out
}

// gatherRoom collects the last 24h (ppz's retention) of room traffic plus
// the read-receipt set. mstrctl's inbox is filtered to room members so one
// room doesn't show another room's replies to you.
func gatherRoom(proj string) (msgs []roomMsg, members []string) {
	handles := map[string]bool{}
	for _, s := range roomAgents(proj) {
		members = append(members, s.Name)
		if s.PpzHandle != "" {
			handles[s.PpzHandle] = true
		}
	}
	read := map[string]bool{}
	scan := func(owner string, membersOnly bool) {
		for _, e := range ppzReread(owner+".inbox", "24h") {
			if e.Subject == "ack:read" {
				read[e.InReplyTo] = true
				continue
			}
			if e.Sender == "" || (membersOnly && !handles[e.Sender]) {
				continue
			}
			t, _ := time.Parse(time.RFC3339, e.CreatedAt)
			msgs = append(msgs, roomMsg{t: t, from: e.Sender, to: owner, text: e.Payload, id: e.ID})
		}
	}
	for h := range handles {
		scan(h, false)
	}
	scan(ctlHandle, true)
	for i := range msgs {
		msgs[i].read = read[msgs[i].id]
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].t.Before(msgs[j].t) })
	return msgs, members
}

func roomName(h string) string {
	if h == ctlHandle {
		return "you"
	}
	return h
}

var (
	sRoomHead   = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sRoomFrom   = lipgloss.NewStyle().Bold(true).Foreground(cWorking)
	sRoomYou    = lipgloss.NewStyle().Bold(true).Foreground(cIdle)
	sRoomTick   = lipgloss.NewStyle().Foreground(cIdle)
	sRoomPrompt = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sRoomErr    = lipgloss.NewStyle().Foreground(cBlocked)
)

// renderRoom builds the whole transcript at width w.
func renderRoom(proj string, w int) string {
	if w < 20 {
		w = 20
	}
	if !ppzReady() {
		return "pipes is off — the room chat needs the mesh.\nopen the sidebar's M view to connect."
	}
	msgs, members := gatherRoom(proj)
	var b strings.Builder
	b.WriteString(sRoomHead.Render("#"+proj) + sDim.Render("  "+strings.Join(members, ", ")) + "\n")
	b.WriteString(sDim.Render(strings.Repeat("─", w-1)) + "\n")
	if len(msgs) == 0 {
		b.WriteString(sDim.Render("\nno room traffic in the last 24h.\n\ntype below to message everyone here at once —\nagent↔agent chatter shows up here too.") + "\n")
		return b.String()
	}
	body := lipgloss.NewStyle().Width(w - 8)
	lastDay := ""
	for _, m := range msgs {
		day := m.t.Local().Format("Mon 2 Jan")
		if day != lastDay {
			b.WriteString(sDim.Render("— "+day+" —") + "\n")
			lastDay = day
		}
		from := sRoomFrom
		if m.from == ctlHandle {
			from = sRoomYou
		}
		head := sDim.Render(m.t.Local().Format("15:04")+" ") +
			from.Render(roomName(m.from)) + sDim.Render(" → "+roomName(m.to))
		if m.read {
			head += " " + sRoomTick.Render("✓✓")
		}
		b.WriteString(head + "\n")
		for _, l := range strings.Split(body.Render(strings.TrimSpace(m.text)), "\n") {
			b.WriteString("       " + l + "\n")
		}
	}
	return b.String()
}

// ---- live viewer (runs inside the workspace's right pane) -------------------

type roomModel struct {
	proj    string
	vp      viewport.Model
	input   textinput.Model
	ready   bool
	pinned  bool   // stick to the newest message unless the user scrolled up
	sendErr string // last send failure, cleared on the next keystroke
}

type roomTickMsg struct{ body string }
type roomSentMsg struct{ err error }

func roomTick(proj string, w int) tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return roomTickMsg{body: renderRoom(proj, w)}
	})
}

// refreshRoomCmd re-renders immediately (off the update loop, like roomTick)
// so a message you send shows up without waiting for the next 2s tick.
func refreshRoomCmd(proj string, w int) tea.Cmd {
	return func() tea.Msg { return roomTickMsg{body: renderRoom(proj, w)} }
}

// sendRoomCmd fans text out to every room member's ppz handle — a group
// chat, built entirely from ppz's per-handle send (ppz has no broadcast
// pipe of its own, see docs/WIRE.md).
func sendRoomCmd(proj, text string) tea.Cmd {
	return func() tea.Msg {
		n := 0
		var err error
		for _, s := range roomAgents(proj) {
			if s.PpzHandle == "" {
				continue
			}
			if e := ppzSend(s.PpzHandle, text); e != nil {
				err = e
				continue
			}
			n++
		}
		if n == 0 && err == nil {
			err = errf("no agents on the mesh in this room")
		}
		return roomSentMsg{err: err}
	}
}

func (m roomModel) Init() tea.Cmd { return nil }

func (m roomModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.vp = viewport.New(msg.Width, msg.Height-2)
		m.vp.SetContent(renderRoom(m.proj, msg.Width))
		m.vp.GotoBottom()
		m.input = textinput.New()
		m.input.Prompt = "" // sRoomPrompt below renders our own "> "
		m.input.Placeholder = "message everyone in #" + m.proj + "…"
		m.input.CharLimit = 4000
		m.input.Width = msg.Width - 4
		m.input.Focus()
		m.ready, m.pinned = true, true
		return m, tea.Batch(roomTick(m.proj, msg.Width), textinput.Blink)
	case roomTickMsg:
		if !m.ready {
			return m, nil
		}
		m.vp.SetContent(msg.body)
		if m.pinned {
			m.vp.GotoBottom()
		}
		return m, roomTick(m.proj, m.vp.Width)
	case roomSentMsg:
		if msg.err != nil {
			m.sendErr = msg.err.Error()
			return m, nil
		}
		return m, refreshRoomCmd(m.proj, m.vp.Width)
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			if m.input.Value() != "" {
				m.input.SetValue("")
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyEnter:
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			m.input.SetValue("")
			m.sendErr = ""
			return m, sendRoomCmd(m.proj, text)
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			m.pinned = m.vp.AtBottom()
			return m, cmd
		default:
			m.sendErr = ""
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	m.pinned = m.vp.AtBottom()
	return m, cmd
}

func (m roomModel) View() string {
	if !m.ready {
		return "…"
	}
	line := sRoomPrompt.Render("> ") + m.input.View()
	if m.sendErr != "" {
		line += "  " + sRoomErr.Render("send failed: "+m.sendErr)
	}
	help := " enter send · ↑/↓ pgup/pgdn scroll · esc/ctrl-c close"
	return m.vp.View() + "\n" + line + "\n" + sHelp.Render(help)
}

func cmdRoom(args []string) int {
	watch := false
	proj := ""
	for _, a := range args {
		if a == "--watch" {
			watch = true
		} else {
			proj = a
		}
	}
	if proj == "" {
		return fail(errf("usage: muster room <project> [--watch]"))
	}
	if !watch {
		fmt.Println(renderRoom(proj, 100))
		return 0
	}
	if _, err := tea.NewProgram(roomModel{proj: proj}, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		return fail(err)
	}
	// keep the pane occupied until the sidebar retargets it (selecting an
	// agent respawns this pane); exiting would leave a dead shell instead
	fmt.Println("room closed — select an agent in the sidebar ←")
	for {
		time.Sleep(time.Hour)
	}
}
