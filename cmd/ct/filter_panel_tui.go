package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// filterAgentMsg carries the async result of a callFilterAgent invocation.
type filterAgentMsg struct {
	result filterSessionResult
	err    error
}

// filterConvEntry is one message in the filter panel conversation history.
type filterConvEntry struct {
	role string // "user" or "assistant"
	text string
}

// Compile-time interface check.
var _ TUIPanel = filterPanel{}

// filterPanel is the Filter cockpit module (key: 8).
// It provides an interactive multi-turn filtration conversation with the LLM
// agent. Message history is displayed as alternating user/assistant blocks with
// a persistent text input at the bottom.
//
// First-use (no session yet): Enter appends a newline to the input buffer;
// ctrl+d submits. The first line is treated as the idea title; subsequent lines
// become the description.
//
// Resume (session active): Enter submits the input directly as a follow-up.
//
// n starts a new session when the input buffer is empty.
type filterPanel struct {
	history   []filterConvEntry
	sessionID string
	inputBuf  string
	running   bool
	errMsg    string
	width     int
	height    int
	scrollY   int
}

func newFilterPanel() filterPanel {
	return filterPanel{
		width:  100,
		height: 24,
	}
}

// isFirstUse returns true when no session has been started yet.
func (p filterPanel) isFirstUse() bool {
	return len(p.history) == 0 && p.sessionID == ""
}

func (p filterPanel) Init() tea.Cmd { return nil }

// submitCmd returns a tea.Cmd that calls the filter agent in the background.
// For a first-use session it parses the prompt (first line = title, rest =
// description) and invokes invokeFilterNew. For resume sessions it calls
// invokeFilterResume with the stored sessionID.
func (p filterPanel) submitCmd(prompt string) tea.Cmd {
	sessionID := p.sessionID
	firstUse := p.isFirstUse()
	return func() tea.Msg {
		preset := resolveFilterPreset("")
		var result filterSessionResult
		var err error
		if firstUse {
			lines := strings.SplitN(strings.TrimSpace(prompt), "\n", 2)
			title := strings.TrimSpace(lines[0])
			var desc string
			if len(lines) > 1 {
				desc = strings.TrimSpace(lines[1])
			}
			contextBlock := gatherFilterContext(filterContextConfig{
				DBPath: resolveDBPath(),
				Title:  title,
				Desc:   desc,
			})
			result, err = invokeFilterNew(preset, title, desc, contextBlock)
		} else {
			result, err = invokeFilterResume(preset, sessionID, prompt)
		}
		return filterAgentMsg{result: result, err: err}
	}
}

// doSubmit appends the current input as a user history entry, dispatches the
// agent call, and resets the input buffer. It is a no-op when inputBuf is empty.
func (p filterPanel) doSubmit() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(p.inputBuf)
	if prompt == "" {
		return p, nil
	}
	p.history = append(p.history, filterConvEntry{role: "user", text: prompt})
	cmd := p.submitCmd(prompt)
	p.inputBuf = ""
	p.running = true
	p.errMsg = ""
	p.scrollY = 999999
	return p, cmd
}

func (p filterPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height

	case filterAgentMsg:
		p.running = false
		if msg.err != nil {
			p.errMsg = msg.err.Error()
		} else {
			p.errMsg = ""
			if msg.result.SessionID != "" {
				p.sessionID = msg.result.SessionID
			}
			p.history = append(p.history, filterConvEntry{
				role: "assistant",
				text: msg.result.Text,
			})
			p.scrollY = 999999
		}

	case tea.KeyMsg:
		if p.running {
			return p, nil
		}
		return p.handleKey(msg)
	}
	return p, nil
}

// handleKey processes a key event when the panel is not running.
func (p filterPanel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+d":
		return p.doSubmit()

	case "enter":
		if p.isFirstUse() {
			// In first-use mode Enter adds a newline; ctrl+d submits.
			p.inputBuf += "\n"
		} else {
			return p.doSubmit()
		}

	case "backspace", "ctrl+h":
		runes := []rune(p.inputBuf)
		if len(runes) > 0 {
			p.inputBuf = string(runes[:len(runes)-1])
		}

	case "up":
		if p.scrollY > 0 {
			p.scrollY--
		}

	case "down":
		p.scrollY++

	case "k":
		if p.inputBuf == "" {
			if p.scrollY > 0 {
				p.scrollY--
			}
		} else {
			p.inputBuf += "k"
		}

	case "j":
		if p.inputBuf == "" {
			p.scrollY++
		} else {
			p.inputBuf += "j"
		}

	case "g":
		if p.inputBuf == "" {
			p.scrollY = 0
		} else {
			p.inputBuf += "g"
		}

	case "G":
		if p.inputBuf == "" {
			p.scrollY = 999999
		} else {
			p.inputBuf += "G"
		}

	case "n", "N":
		if p.inputBuf == "" {
			// Start a new session.
			p.history = nil
			p.sessionID = ""
			p.errMsg = ""
			p.scrollY = 0
		} else {
			p.inputBuf += msg.String()
		}

	default:
		// Append printable ASCII characters to the input buffer.
		s := msg.String()
		if len(s) == 1 && s[0] >= 32 && s[0] < 127 {
			p.inputBuf += s
		}
	}
	return p, nil
}

// buildHistoryLines returns all conversation history as display lines.
func (p filterPanel) buildHistoryLines() []string {
	var lines []string
	for _, entry := range p.history {
		if entry.role == "user" {
			lines = append(lines, tuiStyleYellow.Render("  You:"))
		} else {
			lines = append(lines, tuiStyleGreen.Render("  Assistant:"))
		}
		for _, l := range strings.Split(entry.text, "\n") {
			lines = append(lines, "    "+l)
		}
		lines = append(lines, "")
	}
	if p.errMsg != "" {
		lines = append(lines, tuiStyleRed.Render("  Error: "+p.errMsg))
		lines = append(lines, "")
	}
	return lines
}

// buildInputLines returns lines for the persistent input area at the bottom.
func (p filterPanel) buildInputLines() []string {
	sep := "  " + tuiStyleDim.Render(strings.Repeat("─", max(p.width-4, 10)))
	if p.running {
		return []string{
			sep,
			"  " + tuiStyleYellow.Render("  thinking…"),
		}
	}
	var lines []string
	lines = append(lines, sep)
	if p.isFirstUse() {
		hint := tuiStyleDim.Render("  first line = title  ·  ctrl+d to submit  ·  n new session")
		lines = append(lines, hint)
		inputLines := strings.Split(p.inputBuf, "\n")
		for i, l := range inputLines {
			if i == len(inputLines)-1 {
				lines = append(lines, "  > "+l+"_")
			} else {
				lines = append(lines, "  > "+l)
			}
		}
	} else {
		hint := tuiStyleDim.Render("  enter to submit  ·  n new session  ·  ↑↓ scroll")
		lines = append(lines, hint)
		lines = append(lines, "  > "+p.inputBuf+"_")
	}
	return lines
}

func (p filterPanel) View() string {
	inputLines := p.buildInputLines()

	// Build the header.
	var headerLine string
	if p.isFirstUse() {
		headerLine = tuiStyleHeader.Render("  FILTER — Start a filtration session")
	} else {
		sessionHint := ""
		if p.sessionID != "" {
			sessionHint = "  " + tuiStyleDim.Render("session: "+p.sessionID)
		}
		headerLine = tuiStyleHeader.Render("  FILTER CONVERSATION") + sessionHint
	}

	// Build history lines; show instructions when history is empty.
	historyLines := p.buildHistoryLines()
	if len(historyLines) == 0 && !p.running {
		historyLines = []string{
			"",
			"  Refine ideas before adding droplets. The agent asks",
			"  clarifying questions to sharpen your spec.",
			"",
		}
	}

	// Compute visible slice of history for the scrollable area.
	// Reserve 1 line for header, len(inputLines) for input area.
	inputH := len(inputLines)
	historyH := max(1, p.height-1-inputH)

	total := len(historyLines)
	top := min(p.scrollY, max(0, total-historyH))
	end := min(top+historyH, total)
	visible := historyLines[top:end]
	// Pad to fill the history area so the input region stays at a fixed position.
	for len(visible) < historyH {
		visible = append(visible, "")
	}

	var sb strings.Builder
	sb.WriteString(headerLine)
	sb.WriteByte('\n')
	for _, l := range visible {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	for i, l := range inputLines {
		sb.WriteString(l)
		if i < len(inputLines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func (p filterPanel) Title() string { return "Filter" }

func (p filterPanel) KeyHelp() string {
	return "enter submit  ctrl+d submit  n new session  ↑↓ scroll  esc sidebar"
}

func (p filterPanel) OverlayActive() bool { return false }

func (p filterPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }
