package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MichielDean/cistern/internal/cistern"
)

// PaletteAction describes an action a panel can expose in the cockpit command
// palette when a droplet is in context. Panels return a slice of these from
// PaletteActions; the user searches by Name and executes with Enter.
type PaletteAction struct {
	Name        string         // action name shown in the palette list
	Description string         // short description shown alongside the name
	Run         func() tea.Cmd // called when the action is selected and executed
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
	// SelectedDroplet returns the droplet currently in context for this panel
	// (the list-cursor item in the Droplets view, or the open detail droplet).
	// Returns nil when no droplet is selected.
	SelectedDroplet() *cistern.Droplet
}

// Compile-time interface checks.
var (
	_ TUIPanel = dropletsPanel{}
	_ TUIPanel = placeholderPanel{}
	_ TUIPanel = dashboardPanel{}
	_ TUIPanel = logPanel{}
	_ TUIPanel = reposSkillsPanel{}
	_ TUIPanel = filterPanel{}
	_ TUIPanel = castellariusPanel{}
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
	// True when the Detail/Peek tab is open (not the Droplets list) OR when
	// any overlay (confirm, text-entry, or multi-field form) is active on any tab.
	return p.inner.tab != tabDroplets || p.inner.overlayMode != overlayNone
}

// SelectedDroplet returns the droplet currently in context for the panel.
// In the Detail or Peek tabs it returns the open detail droplet; in the
// Droplets list it returns the item at the current cursor position.
func (p dropletsPanel) SelectedDroplet() *cistern.Droplet {
	switch p.inner.tab {
	case tabDetail, tabPeek:
		return p.inner.detailDroplet
	default:
		items := p.inner.visibleItems()
		if p.inner.cursor < len(items) {
			return items[p.inner.cursor]
		}
		return nil
	}
}

func (p dropletsPanel) PaletteActions(droplet *cistern.Droplet) []PaletteAction {
	// "new droplet" is always available regardless of selection.
	alwaysActions := []PaletteAction{
		dropletPaletteAction("new droplet", "create a new droplet", "", actionCreateDroplet),
	}

	if droplet == nil {
		return alwaysActions
	}
	id := droplet.ID

	if isTerminalStatus(droplet.Status) {
		return append(alwaysActions,
			dropletPaletteAction("reopen", "return to cistern", id, actionReopen),
		)
	}
	actions := []PaletteAction{
		dropletPaletteAction("pass", "signal pass — advance to next step", id, actionPass),
		dropletPaletteAction("recirculate", "send back upstream for rework", id, actionRecirculate),
		dropletPaletteAction("close", "mark as delivered", id, actionClose),
		dropletPaletteAction("cancel", "cancel this droplet", id, actionCancel),
		dropletPaletteAction("pool", "move droplet to pool", id, actionPool),
		dropletPaletteAction("restart", "restart this droplet", id, actionRestart),
		dropletPaletteAction("add note", "add a note to this droplet", id, actionAddNote),
		dropletPaletteAction("edit metadata", "edit title, priority, complexity, description", id, actionEditMeta),
		dropletPaletteAction("add dependency", "add a droplet dependency", id, actionAddDep),
		dropletPaletteAction("remove dependency", "remove a droplet dependency", id, actionRemoveDep),
		dropletPaletteAction("file issue", "file an issue against this droplet", id, actionFileIssue),
		dropletPaletteAction("resolve issue", "resolve an issue by ID", id, actionResolveIssue),
		dropletPaletteAction("reject issue", "reject an issue by ID", id, actionRejectIssue),
	}
	if droplet.CurrentCataractae == "human" {
		actions = append(actions, dropletPaletteAction("approve", "approve for delivery", id, actionApprove))
	}
	return append(alwaysActions, actions...)
}

// dropletPaletteAction constructs a PaletteAction whose Run emits a
// tuiPaletteActionMsg for the given droplet ID and action string.
func dropletPaletteAction(name, desc, dropletID, action string) PaletteAction {
	return PaletteAction{
		Name:        name,
		Description: desc,
		Run: func() tea.Cmd {
			return func() tea.Msg { return tuiPaletteActionMsg{dropletID: dropletID, action: action} }
		},
	}
}

// ── dashboardPanel ───────────────────────────────────────────────────────────

// dashboardPanel adapts dashboardTUIModel to the TUIPanel interface.
// It is the Flow module — showing live aqueduct and flow state in the cockpit.
type dashboardPanel struct {
	inner dashboardTUIModel
}

func newDashboardPanel(cfgPath, dbPath string) dashboardPanel {
	return dashboardPanel{inner: newDashboardTUIModel(cfgPath, dbPath)}
}

func (p dashboardPanel) Init() tea.Cmd {
	return p.inner.Init()
}

func (p dashboardPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := p.inner.Update(msg)
	p.inner = updated.(dashboardTUIModel)
	return p, cmd
}

func (p dashboardPanel) View() string {
	return p.inner.View()
}

func (p dashboardPanel) Title() string { return "Flow" }

func (p dashboardPanel) KeyHelp() string {
	return "↑↓/jk scroll  p peek  r refresh"
}

func (p dashboardPanel) OverlayActive() bool {
	return p.inner.peekActive || p.inner.peekSelectMode
}

func (p dashboardPanel) SelectedDroplet() *cistern.Droplet { return nil }

func (p dashboardPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }

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

func (p placeholderPanel) OverlayActive() bool                              { return false }
func (p placeholderPanel) SelectedDroplet() *cistern.Droplet                { return nil }
func (p placeholderPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction { return nil }

// ── cockpitModel ─────────────────────────────────────────────────────────────

// cockpitSidebarWidth is the fixed column width of the nav sidebar.
const cockpitSidebarWidth = 20

// cockpitModel is the root Bubble Tea model for ct tui.
// It renders a persistent left-column nav sidebar (lazygit-style) and
// delegates content rendering and event handling to the active TUIPanel.
type cockpitModel struct {
	panels            []TUIPanel
	cursor            int  // sidebar highlight position (0-based)
	panelFocused      bool // true = active panel receives key events; false = sidebar mode
	width             int
	height            int
	initializedPanels []bool // tracks which panels have had Init() called

	// Command palette state.
	paletteActive   bool            // true when the command palette overlay is open
	paletteQuery    string          // current filter string typed by the user
	paletteCursor   int             // index of the highlighted action in paletteFiltered
	paletteAll      []PaletteAction // all actions from the active panel (unfiltered)
	paletteFiltered []PaletteAction // actions filtered by paletteQuery
}

// newCockpitModel builds the root cockpit model with all registered panels.
// The Droplets panel is the only fully-implemented module; the rest ship as
// placeholders ready for future implementation.
// The cockpit starts with panelFocused=true so that ct tui lands the user
// directly in the Droplets list — identical UX to the pre-cockpit tui.
func newCockpitModel(cfgPath, dbPath string) cockpitModel {
	m := cockpitModel{
		width:        100,
		height:       24,
		panelFocused: true,
	}
	inner := newTabAppModel(cfgPath, dbPath)
	inner.width = m.panelWidth()
	m.panels = []TUIPanel{
		dropletsPanel{inner: inner},
		newDashboardPanel(cfgPath, dbPath),
		newStatusPanel(cfgPath, dbPath),
		newCastellariusPanel(),
		newDoctorPanel(),
		newLogPanel(defaultLogReader, nil),
		newReposSkillsPanel(cfgPath, dbPath),
		newFilterPanel(),
		placeholderPanel{title: "Audit"},
	}
	// Only panel[0] is initialized in Init(). All others are lazily initialized
	// on first activation to prevent their tick chains from firing into the wrong
	// panel while the cockpit is showing a different module.
	m.initializedPanels = make([]bool, len(m.panels))
	m.initializedPanels[0] = true
	return m
}

// panelWidth returns the usable column width for the right-pane panel content.
func (m cockpitModel) panelWidth() int {
	return max(m.width-cockpitSidebarWidth-1, 20) // 1 col for the │ separator
}

func (m cockpitModel) Init() tea.Cmd {
	// Only initialize the active panel. Inactive panels are initialized lazily
	// on first activation (number key or tab/enter) so that their tick and
	// animation chains do not fire into the wrong panel model.
	return m.panels[0].Init()
}

// Update routes events to the cockpit or the active panel depending on focus mode.
//
// Global intercepts (handled regardless of focus mode):
//   - ctrl+c           → quit
//   - 1-9              → jump to panel[n-1] and activate it (skipped when overlay active)
//
// Sidebar mode (!panelFocused):
//   - tab / enter      → activate panel focus
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

	case tuiAnimMsg:
		// Animation ticks must be routed to all initialized panels, not just the
		// active one. dashboardPanel's tuiAnimTick() chain starts on first
		// activation; if the user navigates away within animInterval the tick
		// would land on the active panel, be silently dropped, and permanently
		// freeze the Flow panel at frame=0. Broadcasting to all initialized panels
		// ensures the chain survives any navigation-away race.
		var cmds []tea.Cmd
		for i, p := range m.panels {
			if m.initializedPanels[i] {
				updated, cmd := p.Update(msg)
				m.panels[i] = updated.(TUIPanel)
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		s := msg.String()
		// ctrl+c always quits, regardless of focus mode.
		if s == "ctrl+c" {
			return m, tea.Quit
		}
		if m.paletteActive {
			return m.updatePalette(msg)
		}
		// ':' opens the command palette from any focus mode, unless the panel has
		// an active overlay (e.g. note input in progress).
		if s == ":" && !(m.panelFocused && m.cursor < len(m.panels) && m.panels[m.cursor].OverlayActive()) {
			return m.openPalette(), nil
		}
		// Number keys switch panels from any mode — sidebar or panel — unless a
		// panel overlay is currently consuming keyboard input (e.g. typing a note).
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			overlayActive := m.cursor < len(m.panels) && m.panels[m.cursor].OverlayActive()
			if !overlayActive {
				idx := int(s[0] - '1')
				if idx < len(m.panels) {
					m.cursor = idx
					m.panelFocused = true
					// Lazily initialize the panel on first activation.
					if !m.initializedPanels[idx] {
						m.initializedPanels[idx] = true
						return m, m.panels[idx].Init()
					}
				}
				return m, nil
			}
			// overlay active: fall through to panel forwarding so the digit
			// reaches the text input field.
		}
		// Sidebar mode: tab/enter/q/Q/up/down/j/k.
		if !m.panelFocused {
			switch s {
			case "tab", "enter":
				m.panelFocused = true
				// Lazily initialize the panel on first activation.
				if m.cursor < len(m.panels) && !m.initializedPanels[m.cursor] {
					m.initializedPanels[m.cursor] = true
					return m, m.panels[m.cursor].Init()
				}
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

	// Background messages always route to their dedicated panels regardless of
	// which panel is focused, so each panel's refresh loop keeps running.
	switch msg.(type) {
	case statusDataMsg, statusTickMsg:
		if len(m.panels) > 2 {
			updated, cmd := m.panels[2].Update(msg)
			m.panels[2] = updated.(TUIPanel)
			return m, cmd
		}
		return m, nil
	case doctorOutputMsg:
		if len(m.panels) > 4 {
			updated, cmd := m.panels[4].Update(msg)
			m.panels[4] = updated.(TUIPanel)
			return m, cmd
		}
		return m, nil
	case castellariusStatusOutputMsg, castellariusTick, castellariusCmdMsg, castellariusCmdOutputMsg:
		if len(m.panels) > 3 {
			updated, cmd := m.panels[3].Update(msg)
			m.panels[3] = updated.(TUIPanel)
			return m, cmd
		}
		return m, nil
	case logTickMsg, logContentMsg:
		if len(m.panels) > 5 {
			updated, cmd := m.panels[5].Update(msg)
			m.panels[5] = updated.(TUIPanel)
			return m, cmd
		}
		return m, nil
	case reposSkillsDataMsg:
	case filterAgentMsg:
		if len(m.panels) > 7 {
			updated, cmd := m.panels[7].Update(msg)
			m.panels[7] = updated.(TUIPanel)
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
// separated by a vertical │ column. When the command palette is open the panel
// area is replaced by the palette overlay.
func (m cockpitModel) View() string {
	sidebar := m.viewSidebar()
	var panel string
	if m.paletteActive {
		panel = m.viewPalette()
	} else {
		panel = m.viewActivePanel()
	}
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

// trimTrailingEmpty removes trailing empty strings from a slice produced by
// strings.Split on a newline-terminated string.
func trimTrailingEmpty(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// joinSideBySide combines sidebar and panel content side by side, padding each
// sidebar line to sidebarW visual columns (using lipgloss.Width for ANSI-aware
// measurement) and inserting a │ separator between the two panes.
func joinSideBySide(sidebar, panel string, sidebarW int) string {
	sideLines := trimTrailingEmpty(strings.Split(sidebar, "\n"))
	panelLines := trimTrailingEmpty(strings.Split(panel, "\n"))

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

// filterPaletteActions returns the subset of actions whose Name contains query
// as a case-insensitive substring. Returns all actions when query is empty.
func filterPaletteActions(all []PaletteAction, query string) []PaletteAction {
	if query == "" {
		return all
	}
	q := strings.ToLower(query)
	out := make([]PaletteAction, 0, len(all))
	for _, a := range all {
		if strings.Contains(strings.ToLower(a.Name), q) {
			out = append(out, a)
		}
	}
	return out
}

// openPalette collects actions from the active panel and opens the command
// palette with a cleared query and cursor reset to 0.
func (m cockpitModel) openPalette() cockpitModel {
	m.paletteAll = nil
	if m.cursor < len(m.panels) {
		selected := m.panels[m.cursor].SelectedDroplet()
		m.paletteAll = m.panels[m.cursor].PaletteActions(selected)
	}
	m.paletteActive = true
	m.paletteQuery = ""
	m.paletteCursor = 0
	m.paletteFiltered = m.paletteAll
	return m
}

// updatePalette handles key events while the command palette is open.
//
//   - esc          → dismiss palette
//   - enter        → execute selected action, dismiss palette, focus panel
//   - up / k       → move cursor up
//   - down / j     → move cursor down
//   - backspace    → remove last character from filter query
//   - other runes  → append to filter query and re-filter; cursor resets to 0
func (m cockpitModel) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case "esc":
		m.paletteActive = false
	case "enter":
		if m.paletteCursor < len(m.paletteFiltered) {
			action := m.paletteFiltered[m.paletteCursor]
			m.paletteActive = false
			m.panelFocused = true
			if action.Run == nil {
				return m, nil
			}
			return m, action.Run()
		}
	case "up", "k":
		if m.paletteCursor > 0 {
			m.paletteCursor--
		}
	case "down", "j":
		if m.paletteCursor < len(m.paletteFiltered)-1 {
			m.paletteCursor++
		}
	case "backspace":
		runes := []rune(m.paletteQuery)
		if len(runes) > 0 {
			m.paletteQuery = string(runes[:len(runes)-1])
			m.paletteFiltered = filterPaletteActions(m.paletteAll, m.paletteQuery)
			m.paletteCursor = min(m.paletteCursor, max(0, len(m.paletteFiltered)-1))
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.paletteQuery += s
			m.paletteFiltered = filterPaletteActions(m.paletteAll, m.paletteQuery)
			m.paletteCursor = 0
		}
	}
	return m, nil
}

// viewPalette renders the command palette in the panel content area.
// It shows a filter input line, a divider, and the filtered action list with
// the cursor position highlighted.
func (m cockpitModel) viewPalette() string {
	var sb strings.Builder
	sb.WriteString(tuiStyleHeader.Render("> "+m.paletteQuery+"▌") + "\n")
	sb.WriteString(strings.Repeat("─", 30) + "\n")
	if len(m.paletteFiltered) == 0 {
		sb.WriteString(tuiStyleDim.Render("  (no matching actions)") + "\n")
		return sb.String()
	}
	for i, a := range m.paletteFiltered {
		desc := ""
		if a.Description != "" {
			desc = "  " + tuiStyleDim.Render(a.Description)
		}
		if i == m.paletteCursor {
			sb.WriteString(tuiStyleGreen.Render("▶ "+a.Name) + desc + "\n")
		} else {
			sb.WriteString("  " + a.Name + desc + "\n")
		}
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
