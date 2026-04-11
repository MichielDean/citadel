package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

const castellariusPanelRefreshInterval = 5 * time.Second

// castellariusStatusOutputMsg carries fresh ct castellarius status output.
type castellariusStatusOutputMsg struct {
	output string
	runAt  time.Time
}

// castellariusTick fires the castellarius panel's periodic refresh timer.
type castellariusTick time.Time

// castellariusCmdMsg is emitted by a palette action to request a confirm overlay
// for the given subcommand ("start", "stop", "restart").
type castellariusCmdMsg struct {
	action string
}

// castellariusCmdOutputMsg carries the result of a start/stop/restart execution.
type castellariusCmdOutputMsg struct {
	action string
	output string
	err    error
}

// Compile-time interface check.
var _ TUIPanel = castellariusPanel{}

// castellariusPanel is the Castellarius cockpit module (key: 4).
// It shows ct castellarius status output with live-refresh on a 5-second ticker.
// Exposes start, stop, and restart actions via the command palette with confirm
// overlays before execution.
type castellariusPanel struct {
	output        string
	runAt         time.Time
	loading       bool
	width         int
	height        int
	scrollY       int
	confirmActive bool
	confirmAction string // "start", "stop", "restart"
	actionOutput  string
	actionErr     bool
}

func newCastellariusPanel() castellariusPanel {
	return castellariusPanel{
		width:   100,
		height:  24,
		loading: true,
	}
}

func (p castellariusPanel) Init() tea.Cmd {
	return p.fetchStatusCmd()
}

// fetchStatusCmd returns a tea.Cmd that runs ct castellarius status and wraps
// the output in a castellariusStatusOutputMsg.
func (p castellariusPanel) fetchStatusCmd() tea.Cmd {
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "ct"
		}
		cmd := exec.Command(exe, "castellarius", "status")
		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = cmdErr.Error()
			}
			return castellariusStatusOutputMsg{output: "Error fetching status: " + msg, runAt: time.Now()}
		}
		return castellariusStatusOutputMsg{output: string(out), runAt: time.Now()}
	}
}

// castellariusTickCmd returns a tea.Cmd that fires a castellariusTick after the
// panel's refresh interval.
func castellariusTickCmd() tea.Cmd {
	return tea.Tick(castellariusPanelRefreshInterval, func(t time.Time) tea.Msg {
		return castellariusTick(t)
	})
}

// execActionCmd returns a tea.Cmd that executes the confirmed action (start, stop,
// or restart) and wraps the result in a castellariusCmdOutputMsg.
//
// For "start", systemctl --user is used because ct castellarius start is a blocking
// foreground process that cannot be run from within the TUI without freezing it.
// For "stop" and "restart", the ct castellarius subcommand is invoked directly.
func (p castellariusPanel) execActionCmd() tea.Cmd {
	action := p.confirmAction
	return func() tea.Msg {
		exe, err := os.Executable()
		if err != nil {
			exe = "ct"
		}
		var out []byte
		switch action {
		case "start":
			// Try systemctl first (standard deployment via cistern-castellarius.service).
			if err = exec.Command("systemctl", "--user", "start", "cistern-castellarius").Run(); err == nil {
				out = []byte("Castellarius started.")
			} else {
				// Fall back: spawn ct castellarius start as a detached process so the TUI
				// is not blocked (ct castellarius start is a blocking foreground process).
				c := exec.Command(exe, "castellarius", "start")
				c.SysProcAttr = detachSysProcAttr()
				if err = c.Start(); err != nil {
					out = []byte(err.Error())
				} else {
					out = []byte("Castellarius started (detached process).")
				}
			}
		default:
			cmd := exec.Command(exe, "castellarius", action)
			out, err = cmd.CombinedOutput()
		}
		return castellariusCmdOutputMsg{action: action, output: string(out), err: err}
	}
}

func (p castellariusPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = msg.Width
		p.height = msg.Height

	case castellariusStatusOutputMsg:
		p.output = msg.output
		p.runAt = msg.runAt
		p.loading = false
		return p, castellariusTickCmd()

	case castellariusTick:
		return p, p.fetchStatusCmd()

	case castellariusCmdMsg:
		p.confirmActive = true
		p.confirmAction = msg.action
		p.actionOutput = ""
		p.actionErr = false

	case castellariusCmdOutputMsg:
		p.actionOutput = strings.TrimSpace(msg.output)
		p.actionErr = msg.err != nil
		// Immediately refresh status to reflect the change.
		return p, p.fetchStatusCmd()

	case tea.KeyMsg:
		// When the confirm overlay is active, handle confirm/dismiss keys only.
		if p.confirmActive {
			switch msg.String() {
			case "y", "Y":
				p.confirmActive = false
				return p, p.execActionCmd()
			case "n", "N", "esc":
				p.confirmActive = false
			}
			return p, nil
		}
		switch msg.String() {
		case "r", "R":
			p.loading = true
			p.scrollY = 0
			return p, p.fetchStatusCmd()
		case "up", "k":
			if p.scrollY > 0 {
				p.scrollY--
			}
		case "down", "j":
			p.scrollY++
		case "home", "g":
			p.scrollY = 0
		case "end", "G":
			p.scrollY = 999999
		}
	}
	return p, nil
}

func (p castellariusPanel) View() string {
	if p.confirmActive {
		return p.viewConfirm()
	}

	if p.loading && p.output == "" {
		return "\n  Loading castellarius status…\n"
	}

	var lines []string
	lines = append(lines, "")

	// Show result of last palette action (start/stop/restart) if any.
	if p.actionOutput != "" {
		if p.actionErr {
			lines = append(lines, tuiStyleRed.Render("  Error: "+p.actionOutput), "")
		} else {
			lines = append(lines, tuiStyleGreen.Render("  "+p.actionOutput), "")
		}
	}

	for _, line := range strings.Split(p.output, "\n") {
		lines = append(lines, "  "+line)
	}
	lines = append(lines, "")

	if !p.runAt.IsZero() {
		age := time.Since(p.runAt).Round(time.Second)
		lines = append(lines, tuiStyleDim.Render(fmt.Sprintf(
			"  refreshed %s ago  ·  r to force-refresh", formatElapsed(age))))
	}

	total := len(lines)
	viewH := max(1, p.height-1)
	top := min(p.scrollY, max(0, total-viewH))
	end := min(top+viewH, total)
	return strings.Join(lines[top:end], "\n")
}

// viewConfirm renders the confirm overlay asking the user to confirm the pending action.
func (p castellariusPanel) viewConfirm() string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(tuiStyleHeader.Render("  Confirm action") + "\n\n")
	a := p.confirmAction
	if len(a) == 0 {
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf("  %s the Castellarius?\n\n", strings.ToUpper(a[:1])+a[1:]))
	sb.WriteString(tuiStyleGreen.Render("  y") + " yes    " + tuiStyleDim.Render("n / esc") + " cancel\n")
	return sb.String()
}

func (p castellariusPanel) Title() string { return "Castellarius" }

func (p castellariusPanel) KeyHelp() string {
	return "r refresh  ↑↓/jk scroll  g/G top/bottom  : palette"
}

func (p castellariusPanel) OverlayActive() bool { return p.confirmActive }

func (p castellariusPanel) SelectedDroplet() *cistern.Droplet { return nil }

// PaletteActions returns the three Castellarius control actions.
// The droplet argument is ignored — these actions operate on the Castellarius
// service, not on any individual droplet.
func (p castellariusPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction {
	castAction := func(action string) PaletteAction {
		return PaletteAction{
			Name:        action,
			Description: action + " the Castellarius",
			Run: func() tea.Cmd {
				return func() tea.Msg { return castellariusCmdMsg{action: action} }
			},
		}
	}
	return []PaletteAction{
		castAction("start"),
		castAction("stop"),
		castAction("restart"),
	}
}
