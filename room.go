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

// A room IS a project/space: a shared uncollared ppz pipe (room-<proj>)
// that every member subscribes to at launch — one send, everyone sees it
// AND each other's replies, sender-attributed, each on their own cursor.
// The view unions that channel with the members' inbox DM traffic (agent↔
// agent chatter, standup replies to mstrctl) so 1:1 messages still show.
// Clicking a project in the sidebar shows it in the right pane
// (`muster room <project> --watch`). Read receipts (✓✓) come from ppz
// ack:read envelopes (inbox DMs only — shared-pipe ack semantics are
// per-reader and unverified, so room messages carry no tick).

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
	// the shared room channel itself — everyone's room sends, one copy each
	for _, e := range ppzReread(roomPipe(proj), "24h") {
		if e.Sender == "" {
			continue
		}
		t, _ := time.Parse(time.RFC3339, e.CreatedAt)
		msgs = append(msgs, roomMsg{t: t, from: e.Sender, to: "#" + proj, text: e.Payload, id: e.ID})
	}
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

// roomRefreshedMsg is a one-shot re-render (a sent message shouldn't wait for
// the next 2s tick) — distinct from roomTickMsg so handling it doesn't also
// arm a second, redundant tick loop alongside the one already running.
type roomRefreshedMsg struct{ body string }

func refreshRoomCmd(proj string, w int) tea.Cmd {
	return func() tea.Msg { return roomRefreshedMsg{body: renderRoom(proj, w)} }
}

// sendRoomCmd publishes text ONCE to the project's shared room pipe —
// every subscribed member sees it (and each other's replies) there.
// Members subscribe at launch, so agents spawned before this feature
// need a relaunch to hear the room.
func sendRoomCmd(proj, text string) tea.Cmd {
	return func() tea.Msg {
		pipe, err := ensureRoomPipe(proj)
		if err == nil {
			err = ppzSend(pipe, text)
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
	case roomRefreshedMsg:
		if !m.ready {
			return m, nil
		}
		m.vp.SetContent(msg.body)
		if m.pinned {
			m.vp.GotoBottom()
		}
		return m, nil
	case roomSentMsg:
		if msg.err != nil {
			m.sendErr = strings.TrimSpace(msg.err.Error())
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

// truncateRunes bounds s to n runes so a long error can never make the
// compose line wider than the pane — a soft-wrapped line desyncs
// bubbletea's row bookkeeping and corrupts the whole alt-screen frame.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// status is its own line below input (not appended to it) — the input
// itself is padded to fill the pane width, leaving no safe budget to
// append anything after it without risking a soft-wrapped line, which
// desyncs bubbletea's row bookkeeping and corrupts the whole frame.
func (m roomModel) status() string {
	if m.sendErr == "" {
		return sHelp.Render(" enter send · ↑/↓ pgup/pgdn scroll · esc/ctrl-c close")
	}
	budget := m.vp.Width - len(" send failed: ")
	if budget < 10 {
		budget = 10
	}
	return " " + sRoomErr.Render("send failed: "+truncateRunes(m.sendErr, budget))
}

func (m roomModel) View() string {
	if !m.ready {
		return "…"
	}
	line := sRoomPrompt.Render("> ") + m.input.View()
	return m.vp.View() + "\n" + line + "\n" + m.status()
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
