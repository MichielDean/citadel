package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MichielDean/cistern/internal/cistern"
)

// PaletteAction describes a keyboard-triggered action a panel can offer in the
// cockpit command palette when a droplet is in context. Panels return a slice
// of these from PaletteActions so the cockpit can render a unified action bar.
type PaletteAction struct {
	Key   string // keyboard shortcut, e.g. "x"
	Label string // human-readable description, e.g. "cancel droplet"
}

// TUIPanel is the interface every cockpit module must implement.
// It extends tea.Model with panel metadata and command-palette support.
type TUIPanel interface {
	tea.Model
	// Title returns the short display name shown in the nav sidebar.
	Title() string
	// KeyHelp returns a one-line hint string shown in the cockpit footer.
	KeyHelp() string
	// PaletteActions returns the actions available for the given droplet.
	// droplet may be nil when no droplet is selected.
	PaletteActions(droplet *cistern.Droplet) []PaletteAction
	// OverlayActive reports whether the panel currently has an overlay open
	// (e.g. a confirmation dialog or text-entry prompt). The cockpit uses this
	// to decide whether to intercept Esc for return-to-sidebar navigation or
	// forward it to the panel so the overlay can be dismissed first.
	OverlayActive() bool
}

// Compile-time interface checks.
var (
	_ TUIPanel = dropletsPanel{}
	_ TUIPanel = placeholderPanel{}
)

// ── dropletsPanel ────────────────────────────────────────────────────────────

// dropletsPanel adapts tabAppModel to the TUIPanel interface.
// It is the Droplets module — the only currently-functional cockpit panel.
type dropletsPanel struct {
	inner tabAppModel
}

func newDropletsPanel(cfgPath, dbPath string) dropletsPanel {
	return dropletsPanel{inner: newTabAppModel(cfgPath, dbPath)}
}

func (p dropletsPanel) Init() tea.Cmd {
	return p.inner.Init()
}

func (p dropletsPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := p.inner.Update(msg)
	p.inner = updated.(tabAppModel)
	return p, cmd
}

func (p dropletsPanel) View() string {
	return p.inner.View()
}

func (p dropletsPanel) Title() string { return "Droplets" }

func (p dropletsPanel) KeyHelp() string {
	return "↑↓/jk navigate  enter/d detail  p peek"
}

func (p dropletsPanel) OverlayActive() bool {
	return p.inner.overlayMode != overlayNone
}

func (p dropletsPanel) PaletteActions(droplet *cistern.Droplet) []PaletteAction {
	if droplet == nil {
		return nil
	}
	return []PaletteAction{
		{Key: "x", Label: "cancel"},
		{Key: "e", Label: "pool"},
		{Key: "r", Label: "restart"},
		{Key: "n", Label: "add note"},
	}
}

// ── placeholderPanel ─────────────────────────────────────────────────────────

// placeholderPanel is a TUIPanel stub for cockpit modules not yet implemented.
type placeholderPanel struct {
	title string
}

func (p placeholderPanel) Init() tea.Cmd { return nil }

func (p placeholderPanel) Update(_ tea.Msg) (tea.Model, tea.Cmd) { return p, nil }

func (p placeholderPanel) View() string {
	return "\n\n  (not yet implemented)\n"
}

func (p placeholderPanel) Title() string { return p.title }

func (p placeholderPanel) KeyHelp() string { return "" }

func (p placeholderPanel) OverlayActive() bool { return false }

func (p placeholderPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }

// ── cockpitModel ─────────────────────────────────────────────────────────────

// cockpitSidebarWidth is the fixed column width of the nav sidebar.
const cockpitSidebarWidth = 20

// cockpitModel is the root Bubble Tea model for ct tui.
// It renders a persistent left-column nav sidebar (lazygit-style) and
// delegates content rendering and event handling to the active TUIPanel.
type cockpitModel struct {
	panels       []TUIPanel
	cursor       int  // sidebar highlight position (0-based)
	panelFocused bool // true = active panel receives key events; false = sidebar mode
	width        int
	height       int
}

// newCockpitModel builds the root cockpit model with all registered panels.
// The Droplets panel is the only fully-implemented module; the rest ship as
// placeholders ready for future implementation.
func newCockpitModel(cfgPath, dbPath string) cockpitModel {
	m := cockpitModel{
		width:  100,
		height: 24,
	}
	inner := newTabAppModel(cfgPath, dbPath)
	inner.width = m.panelWidth()
	m.panels = []TUIPanel{
		dropletsPanel{inner: inner},
		placeholderPanel{title: "Dashboard"},
		newStatusPanel(cfgPath, dbPath),
		placeholderPanel{title: "Inspect"},
		placeholderPanel{title: "Audit"},
	}
	return m
}

// panelWidth returns the usable column width for the right-pane panel content.
func (m cockpitModel) panelWidth() int {
	return max(m.width-cockpitSidebarWidth-1, 20) // 1 col for the │ separator
}

func (m cockpitModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range m.panels {
		cmds = append(cmds, p.Init())
	}
	return tea.Batch(cmds...)
}

// Update routes events to the cockpit or the active panel depending on focus mode.
//
// Global intercepts (handled regardless of focus mode):
//   - ctrl+c           → quit
//
// Sidebar mode (!panelFocused):
//   - tab / enter      → activate panel focus
//   - 1-9              → jump to panel[n-1] and activate it
//   - q / Q            → quit
//   - up / k           → move cursor up
//   - down / j         → move cursor down
//
// Panel mode (panelFocused):
//   - all other messages (including q/Q, tab) are forwarded to the active panel.
func (m cockpitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		panelMsg := tea.WindowSizeMsg{Width: m.panelWidth(), Height: m.height}
		var cmds []tea.Cmd
		for i, p := range m.panels {
			updated, cmd := p.Update(panelMsg)
			m.panels[i] = updated.(TUIPanel)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		s := msg.String()
		// ctrl+c always quits, regardless of focus mode.
		if s == "ctrl+c" {
			return m, tea.Quit
		}
		// Sidebar mode: all sidebar key handling consolidated here.
		if !m.panelFocused {
			if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
				idx := int(s[0] - '1')
				if idx < len(m.panels) {
					m.cursor = idx
					m.panelFocused = true
				}
				return m, nil
			}
			switch s {
			case "tab", "enter":
				m.panelFocused = true
			case "q", "Q":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.panels)-1 {
					m.cursor++
				}
			}
			return m, nil
		}
		// panelFocused=true: esc returns to sidebar unless the panel has an active
		// overlay (in that case forward esc so the panel can dismiss it first).
		if s == "esc" && m.cursor < len(m.panels) && !m.panels[m.cursor].OverlayActive() {
			m.panelFocused = false
			return m, nil
		}
		// All other panel-focused keys fall through to forwarding below.
	}

	// statusDataMsg and statusTickMsg always route to panels[2] (statusPanel)
	// regardless of which panel is currently focused, so the background refresh
	// loop continues running when the user is on a different panel.
	switch msg.(type) {
	case statusDataMsg, statusTickMsg:
		if len(m.panels) > 2 {
			updated, cmd := m.panels[2].Update(msg)
			m.panels[2] = updated.(TUIPanel)
			return m, cmd
		}
		return m, nil
	}

	// Forward all other non-key messages and panel-focused key messages to the active panel.
	if m.cursor < len(m.panels) {
		updated, cmd := m.panels[m.cursor].Update(msg)
		m.panels[m.cursor] = updated.(TUIPanel)
		return m, cmd
	}
	return m, nil
}

// View renders the full cockpit: sidebar on the left, active panel on the right,
// separated by a vertical │ column.
func (m cockpitModel) View() string {
	sidebar := m.viewSidebar()
	panel := m.viewActivePanel()
	return joinSideBySide(sidebar, panel, cockpitSidebarWidth)
}

// viewSidebar renders the left-column navigation sidebar listing all panels.
// The cursor position is highlighted; colour indicates whether sidebar or panel
// is currently focused.
func (m cockpitModel) viewSidebar() string {
	divider := strings.Repeat("─", cockpitSidebarWidth) + "\n"
	var sb strings.Builder
	sb.WriteString(tuiStyleHeader.Render("  CISTERN") + "\n")
	sb.WriteString(divider)
	for i, p := range m.panels {
		label := fmt.Sprintf("%d  %s", i+1, p.Title())
		switch {
		case i == m.cursor && m.panelFocused:
			sb.WriteString(tuiStyleGreen.Render("▶ "+label) + "\n")
		case i == m.cursor:
			sb.WriteString(tuiStyleYellow.Render("▷ "+label) + "\n")
		default:
			sb.WriteString("  " + label + "\n")
		}
	}
	sb.WriteString(divider)
	hint := "  tab→panel"
	if m.panelFocused {
		hint = "  esc→sidebar"
	}
	sb.WriteString(tuiStyleDim.Render(hint) + "\n")
	return sb.String()
}

// viewActivePanel returns the View of the currently selected panel.
func (m cockpitModel) viewActivePanel() string {
	if m.cursor >= len(m.panels) {
		return ""
	}
	return m.panels[m.cursor].View()
}

// joinSideBySide combines sidebar and panel content side by side, padding each
// sidebar line to sidebarW visual columns (using lipgloss.Width for ANSI-aware
// measurement) and inserting a │ separator between the two panes.
func joinSideBySide(sidebar, panel string, sidebarW int) string {
	sideLines := strings.Split(sidebar, "\n")
	panelLines := strings.Split(panel, "\n")

	n := max(len(sideLines), len(panelLines))

	var sb strings.Builder
	for i := 0; i < n; i++ {
		var sl, pl string
		if i < len(sideLines) {
			sl = sideLines[i]
		}
		if i < len(panelLines) {
			pl = panelLines[i]
		}
		// Pad sidebar line to exact visual width so the separator column is stable.
		vw := lipgloss.Width(sl)
		if vw < sidebarW {
			sl += strings.Repeat(" ", sidebarW-vw)
		}
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(sl)
		sb.WriteString("│")
		sb.WriteString(pl)
	}
	return sb.String()
}

// RunCockpitTUI launches the ct tui cockpit using the alternate screen.
func RunCockpitTUI(cfgPath, dbPath string) error {
	m := newCockpitModel(cfgPath, dbPath)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
