package main

import (
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Capturer abstracts tmux pane capture so it can be replaced in tests.
type Capturer interface {
	// Capture fetches the last `lines` lines of the named tmux pane.
	Capture(session string, lines int) (string, error)
	// HasSession reports whether the named tmux session exists.
	HasSession(session string) bool
}

// tmuxCapturer is the production Capturer that delegates to the real tmux binary.
type tmuxCapturer struct{}

func (tmuxCapturer) Capture(session string, lines int) (string, error) {
	return capturePane(session, lines)
}

func (tmuxCapturer) HasSession(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

// defaultCapturer is the singleton production capturer.
var defaultCapturer Capturer = tmuxCapturer{}

// --- Messages ---

type peekTickMsg    time.Time
type peekContentMsg string

// peekInterval is the polling rate for live pane updates.
const peekInterval = 500 * time.Millisecond

// defaultPeekLines is the number of pane lines shown when not configured.
const defaultPeekLines = 100

// --- Model ---

// peekModel is a Bubble Tea model for the read-only peek TUI panel.
// It polls the tmux pane every peekInterval and renders the output.
type peekModel struct {
	capturer    Capturer
	session     string
	lines       int    // number of pane lines to capture
	content string // current pane content
	scrollY int    // scroll offset (0 = top)
	pinned      bool   // when true scroll position is locked
	width       int
	height      int
	header      string // e.g. "[ci-0vm8f] My title — flowing 2m 14s"
}

// newPeekModel constructs a peekModel ready for use.
func newPeekModel(capturer Capturer, session, header string, lines int) peekModel {
	if lines <= 0 {
		lines = defaultPeekLines
	}
	return peekModel{
		capturer: capturer,
		session:  session,
		header:   header,
		lines:    lines,
		width:    100,
		height:   24,
	}
}

func (m peekModel) Init() tea.Cmd {
	return peekTickCmd()
}

// peekTickCmd schedules the next poll.
func peekTickCmd() tea.Cmd {
	return tea.Tick(peekInterval, func(t time.Time) tea.Msg {
		return peekTickMsg(t)
	})
}

// fetchCmd captures the current pane content and returns it as a peekContentMsg.
func (m peekModel) fetchCmd() tea.Cmd {
	capturer := m.capturer
	session := m.session
	lines := m.lines
	return func() tea.Msg {
		if !capturer.HasSession(session) {
			return peekContentMsg("")
		}
		content, err := capturer.Capture(session, lines)
		if err != nil {
			return peekContentMsg("")
		}
		return peekContentMsg(content)
	}
}

func (m peekModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case peekTickMsg:
		return m, m.fetchCmd()

	case peekContentMsg:
		newContent := string(msg)
		m.content = newContent
		if !m.pinned {
			// Auto-scroll to bottom.
			contentLines := strings.Count(newContent, "\n")
			visible := max(m.height-4, 1) // reserve rows for header + label + divider + status
			if contentLines > visible {
				m.scrollY = contentLines - visible
			} else {
				m.scrollY = 0
			}
		}
		return m, peekTickCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case " ", "p":
			m.pinned = !m.pinned
		case "up", "k":
			if m.scrollY > 0 {
				m.scrollY--
			}
			m.pinned = true // manual scroll implies pin
		case "down", "j":
			m.scrollY++
			m.pinned = true
		}
		return m, nil
	}

	return m, nil
}

func (m peekModel) View() string {
	var b strings.Builder

	// Header line.
	b.WriteString(m.header)
	b.WriteByte('\n')

	// Status label.
	label := "Observing — read only"
	if m.pinned {
		label += "  [scroll pinned — space to unpin]"
	} else {
		label += "  [auto-scroll — space to pin]"
	}
	b.WriteString(label)
	b.WriteByte('\n')

	// Horizontal divider.
	dividerW := m.width
	if dividerW <= 0 {
		dividerW = 80
	}
	b.WriteString(strings.Repeat("─", dividerW))
	b.WriteByte('\n')

	// Content area.
	if m.content == "" {
		b.WriteString("(session not active)")
	} else {
		lines := strings.Split(m.content, "\n")
		visible := max(m.height-4, 1)
		start := max(m.scrollY, 0)
		if start >= len(lines) {
			start = max(len(lines)-1, 0)
		}
		end := min(start+visible, len(lines))
		b.WriteString(strings.Join(lines[start:end], "\n"))
	}

	return b.String()
}

// computeDiff returns next if it differs from prev, or "" if unchanged.
// Used by the WebSocket handler to send only changed content.
func computeDiff(prev, next string) string {
	if prev == next {
		return ""
	}
	return next
}
