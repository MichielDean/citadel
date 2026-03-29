package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/MichielDean/cistern/internal/cistern"
)

// Tab constants for tabAppModel.
const (
	tabDroplets = 0
	tabDetail   = 1
)

// Overlay mode constants for action dispatch in the Detail panel.
const (
	overlayNone = iota
	overlayConfirm
	overlayText
)

// Action constants identify the pending Detail-panel action.
const (
	actionCancel   = "cancel"
	actionEscalate = "escalate"
	actionRestart  = "restart"
	actionAddNote  = "addnote"
	actionSetStep  = "setstep"
)

// tuiDetailDataMsg carries notes fetched for the Detail panel.
// The dropletID field lets the handler discard stale responses when the user
// navigates away before the fetch completes.
// err is non-nil when the notes fetch failed; the Detail view displays an error
// indicator so the user is not misled into thinking the droplet has no notes.
type tuiDetailDataMsg struct {
	dropletID string
	notes     []cistern.CataractaeNote
	err       error
}

// tuiActionResultMsg carries the outcome of an async action dispatched from
// the Detail panel. dropletID lets the handler discard results for a different
// droplet if the user navigated away before the action completed.
type tuiActionResultMsg struct {
	dropletID string
	err       error
}

// tabAppModel is the root Bubble Tea model for `ct tui`.
// It manages two views: the Droplets list and the Detail panel.
type tabAppModel struct {
	cfgPath string
	dbPath  string

	// Dashboard data — refreshed periodically via the standard tick chain.
	data *DashboardData

	// Active view: tabDroplets or tabDetail.
	tab              int
	cursor           int // cursor position in the Droplets list
	dropletsScrollTop int // viewport line offset for the Droplets list

	// Detail panel state — populated when the Detail view opens.
	selectedID    string
	detailDroplet *cistern.Droplet
	detailNotes   []cistern.CataractaeNote // chronological order (oldest first)
	detailSteps   []string                 // pipeline step names for the droplet
	detailScrollY int
	detailErr     error // non-nil when the notes fetch failed

	// Action overlay state — populated when an action keybinding is pressed.
	overlayMode   int    // overlayNone, overlayConfirm, or overlayText
	overlayAction string // pending action (actionCancel, actionEscalate, …)
	overlayInput  string // text being typed in overlayText mode
	overlayErr    string // error message from the most recent action (empty = none)

	width  int
	height int
}

func newTabAppModel(cfgPath, dbPath string) tabAppModel {
	return tabAppModel{
		cfgPath: cfgPath,
		dbPath:  dbPath,
		width:   100,
		height:  24,
	}
}

func (m tabAppModel) Init() tea.Cmd {
	return m.fetchDataCmd()
}

func (m tabAppModel) fetchDataCmd() tea.Cmd {
	cfgPath, dbPath := m.cfgPath, m.dbPath
	return func() tea.Msg {
		return tuiDataMsg(fetchDashboardData(cfgPath, dbPath))
	}
}

// fetchDetailCmd opens the DB and loads all notes for dropletID.
// Notes are returned newest-first by the DB; the Update handler reverses them.
func (m tabAppModel) fetchDetailCmd(dropletID string) tea.Cmd {
	dbPath := m.dbPath
	return func() tea.Msg {
		c, err := cistern.New(dbPath, "")
		if err != nil {
			return tuiDetailDataMsg{dropletID: dropletID, err: err}
		}
		defer c.Close()
		notes, err := c.GetNotes(dropletID)
		return tuiDetailDataMsg{dropletID: dropletID, notes: notes, err: err}
	}
}

// execActionCmd opens the DB and executes the named action for dropletID.
// input is the user-supplied text for text-entry actions; it is ignored for
// confirm-only actions (cancel, escalate).
func (m tabAppModel) execActionCmd(dropletID, action, input string) tea.Cmd {
	dbPath := m.dbPath
	return func() tea.Msg {
		c, err := cistern.New(dbPath, "")
		if err != nil {
			return tuiActionResultMsg{dropletID: dropletID, err: err}
		}
		defer c.Close()
		var execErr error
		switch action {
		case actionCancel:
			execErr = c.Cancel(dropletID, "")
		case actionEscalate:
			execErr = c.Escalate(dropletID, "")
		case actionRestart:
			execErr = c.Assign(dropletID, "", input)
		case actionAddNote:
			execErr = c.AddNote(dropletID, "manual", input)
		case actionSetStep:
			execErr = c.SetCataractae(dropletID, input)
		}
		return tuiActionResultMsg{dropletID: dropletID, err: execErr}
	}
}

// closeOverlay resets all overlay state to inactive.
func closeOverlay(m tabAppModel) tabAppModel {
	m.overlayMode = overlayNone
	m.overlayAction = ""
	m.overlayInput = ""
	return m
}

// handleOverlayKey routes a key event to the active overlay (confirm or text-entry).
// It is only called when overlayMode != overlayNone.
func (m tabAppModel) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.overlayMode {
	case overlayConfirm:
		switch s {
		case "y", "Y":
			action, id := m.overlayAction, m.selectedID
			m = closeOverlay(m)
			return m, m.execActionCmd(id, action, "")
		default:
			// Any other key (n, N, esc, q, …) dismisses without executing.
			m = closeOverlay(m)
		}
	case overlayText:
		switch s {
		case "esc":
			m = closeOverlay(m)
		case "enter":
			if m.overlayInput == "" {
				break // empty input is a no-op
			}
			action, id, input := m.overlayAction, m.selectedID, m.overlayInput
			m = closeOverlay(m)
			return m, m.execActionCmd(id, action, input)
		case "backspace":
			runes := []rune(m.overlayInput)
			if len(runes) > 0 {
				m.overlayInput = string(runes[:len(runes)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.overlayInput += s
			}
		}
	}
	return m, nil
}

// visibleItems returns the list shown in the Droplets tab (all CisternItems).
func (m tabAppModel) visibleItems() []*cistern.Droplet {
	if m.data == nil {
		return nil
	}
	return m.data.CisternItems
}

// openDetail switches to the Detail view for the given droplet ID.
func (m tabAppModel) openDetail(dropletID string) (tabAppModel, tea.Cmd) {
	m.selectedID = dropletID
	m.tab = tabDetail
	m.detailDroplet = m.findDroplet(dropletID)
	m.detailNotes = nil
	m.detailScrollY = 0
	m.detailErr = nil
	m.detailSteps = m.findStepsForDroplet(dropletID)
	return m, m.fetchDetailCmd(dropletID)
}

// findDroplet locates a droplet by ID in the current DashboardData.
func (m tabAppModel) findDroplet(id string) *cistern.Droplet {
	if m.data == nil {
		return nil
	}
	for _, item := range m.data.CisternItems {
		if item.ID == id {
			return item
		}
	}
	for _, item := range m.data.RecentItems {
		if item.ID == id {
			return item
		}
	}
	return nil
}

// findStepsForDroplet returns the workflow step names for the droplet's
// aqueduct by matching the dropletID against active CataractaeInfo entries.
func (m tabAppModel) findStepsForDroplet(dropletID string) []string {
	if m.data == nil {
		return nil
	}
	for _, ch := range m.data.Cataractae {
		if ch.DropletID == dropletID {
			return ch.Steps
		}
	}
	return nil
}

// detailLineCount returns the number of content lines that viewDetail produces
// for the current model state. Used by updateDetail to compute maxScroll and
// clamp detailScrollY after every scroll operation so the model never retains
// an inflated offset that makes scroll-up appear broken.
func (m tabAppModel) detailLineCount() int {
	if m.detailDroplet == nil {
		// "  Loading…\n\n" splits into 3 lines; no scrolling meaningful here.
		return 3
	}
	n := 2 // header line + repo·status·step line
	if len(m.detailSteps) > 0 {
		n++ // pipeline position indicator
	}
	n++ // separator
	n++ // "NOTES (count)" heading

	if m.detailErr != nil {
		n++ // error message line
	} else if len(m.detailNotes) == 0 {
		n++ // "(no notes yet)"
	} else {
		for _, note := range m.detailNotes {
			content := strings.TrimSpace(note.Content)
			noteLines := strings.Split(content, "\n")
			n++ // first content line
			for _, l := range noteLines[1:] {
				if strings.TrimSpace(l) != "" {
					n++ // non-empty continuation line
				}
			}
			n++ // blank spacer after each note
		}
		n-- // trailing blank is trimmed by viewDetail
	}
	return n
}

// Update routes messages to the active view's handler.
// tuiActionResultMsg is handled here before tab dispatch so it is never
// silently dropped when the user navigates away from the Detail panel while
// an async action is in-flight (e.g. during a SQLite busy-timeout delay).
func (m tabAppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ar, ok := msg.(tuiActionResultMsg); ok {
		if ar.dropletID != m.selectedID {
			return m, nil
		}
		if ar.err != nil {
			m.overlayErr = ar.err.Error()
		} else {
			m.overlayErr = ""
		}
		return m, tea.Batch(m.fetchDataCmd(), m.fetchDetailCmd(m.selectedID))
	}
	if m.tab == tabDetail {
		return m.updateDetail(msg)
	}
	return m.updateDroplets(msg)
}

func (m tabAppModel) updateDroplets(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tuiDataMsg:
		m.data = (*DashboardData)(msg)
		// Keep cursor in bounds after a data refresh.
		if items := m.visibleItems(); m.cursor >= len(items) && len(items) > 0 {
			m.cursor = len(items) - 1
		}
		m.dropletsScrollTop = m.clampedDropletsScrollTop()
		return m, tuiTickWithInterval(refreshInterval)

	case tuiTickMsg:
		return m, m.fetchDataCmd()

	case tea.KeyMsg:
		items := m.visibleItems()
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "down", "j":
			if m.cursor < len(items)-1 {
				m.cursor++
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter", "d":
			if len(items) > 0 && m.cursor < len(items) {
				return m.openDetail(items[m.cursor].ID)
			}
		}
		m.dropletsScrollTop = m.clampedDropletsScrollTop()
	}
	return m, nil
}

// clampedDropletsScrollTop returns a dropletsScrollTop value adjusted to keep
// the cursor row within the visible viewport. Content line index for item i is
// i+2 (header + separator occupy lines 0 and 1).
func (m tabAppModel) clampedDropletsScrollTop() int {
	viewH := m.height - 1 // 1 row reserved for the pinned footer
	if viewH < 1 {
		viewH = 1
	}
	cursorLine := m.cursor + 2 // header + sep = 2 lines before items
	top := m.dropletsScrollTop
	if cursorLine < top {
		top = cursorLine
	}
	if cursorLine >= top+viewH {
		top = cursorLine - viewH + 1
	}
	if top < 0 {
		top = 0
	}
	return top
}

func (m tabAppModel) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tuiDetailDataMsg:
		// Discard stale responses if the user navigated to a different droplet.
		if msg.dropletID == m.selectedID {
			if msg.err != nil {
				m.detailErr = msg.err
			} else {
				m.detailErr = nil
				notes := make([]cistern.CataractaeNote, len(msg.notes))
				copy(notes, msg.notes)
				// DB returns newest first; reverse to chronological order (oldest first).
				for i, j := 0, len(notes)-1; i < j; i, j = i+1, j-1 {
					notes[i], notes[j] = notes[j], notes[i]
				}
				m.detailNotes = notes
			}
		}

	case tuiDataMsg:
		m.data = (*DashboardData)(msg)
		// Keep detail droplet metadata fresh on each poll.
		if updated := m.findDroplet(m.selectedID); updated != nil {
			m.detailDroplet = updated
		}
		return m, tuiTickWithInterval(refreshInterval)

	case tuiTickMsg:
		return m, m.fetchDataCmd()

	case tea.KeyMsg:
		// When an overlay is active, route all key events to the overlay handler.
		if m.overlayMode != overlayNone {
			return m.handleOverlayKey(msg)
		}
		// Clear any prior action error on the next keypress.
		m.overlayErr = ""
		viewH := m.height - 1
		if viewH < 1 {
			viewH = 1
		}
		maxScroll := m.detailLineCount() - viewH
		if maxScroll < 0 {
			maxScroll = 0
		}
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			// Return to Droplets tab and clear selection.
			m.tab = tabDroplets
			m.selectedID = ""
		case "down", "j":
			m.detailScrollY++
		case "up", "k":
			m.detailScrollY--
		case "home", "g":
			m.detailScrollY = 0
		case "end", "G":
			m.detailScrollY = maxScroll
		case "pgdown", "ctrl+d":
			m.detailScrollY += m.height / 2
		case "pgup", "ctrl+u":
			m.detailScrollY -= m.height / 2
		case "r":
			if m.detailDroplet != nil {
				m.overlayMode = overlayText
				m.overlayAction = actionRestart
			}
		case "x":
			if m.detailDroplet != nil {
				m.overlayMode = overlayConfirm
				m.overlayAction = actionCancel
			}
		case "e":
			if m.detailDroplet != nil {
				m.overlayMode = overlayConfirm
				m.overlayAction = actionEscalate
			}
		case "n":
			if m.detailDroplet != nil {
				m.overlayMode = overlayText
				m.overlayAction = actionAddNote
			}
		case "s":
			if m.detailDroplet != nil {
				m.overlayMode = overlayText
				m.overlayAction = actionSetStep
			}
		}
		// Clamp to valid range after every scroll operation.
		if m.detailScrollY < 0 {
			m.detailScrollY = 0
		}
		if m.detailScrollY > maxScroll {
			m.detailScrollY = maxScroll
		}
	}
	return m, nil
}

// View dispatches to the active tab's renderer.
func (m tabAppModel) View() string {
	if m.data == nil {
		return "  Loading…\n"
	}
	if m.tab == tabDetail {
		return m.viewDetail()
	}
	return m.viewDroplets()
}

// scrollViewport slices lines to the viewH-line window starting at scrollTop,
// clamping scrollTop to a valid range. Used by both view functions.
func scrollViewport(lines []string, scrollTop, viewH int) []string {
	total := len(lines)
	max := total - viewH
	if max < 0 {
		max = 0
	}
	if scrollTop < 0 {
		scrollTop = 0
	}
	if scrollTop > max {
		scrollTop = max
	}
	end := scrollTop + viewH
	if end > total {
		end = total
	}
	return lines[scrollTop:end]
}

// viewDroplets renders the Droplets list with a cursor indicator.
func (m tabAppModel) viewDroplets() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}
	sep := strings.Repeat("─", w)

	var parts []string
	parts = append(parts, tuiStyleHeader.Render("  DROPLETS"))
	parts = append(parts, sep)

	items := m.visibleItems()
	if len(items) == 0 {
		parts = append(parts, "  (cistern is empty)")
	} else {
		const fixedW = 2 + 10 + 2 + 7 + 2 + 12 + 2 // matches the Sprintf format below
		titleW := w - fixedW
		if titleW < 8 {
			titleW = 8
		}
		for i, item := range items {
			stepName := item.CurrentCataractae
			if stepName == "" {
				stepName = "—"
			}
			step := padRight(stepName, 12)
			title := item.Title
			if len([]rune(title)) > titleW {
				title = string([]rune(title)[:titleW-1]) + "…"
			}

			line := fmt.Sprintf("  %-10s  %-7s  %-12s  %s",
				item.ID, item.Status, step, title)
			if i == m.cursor {
				parts = append(parts, tuiStyleGreen.Render("▶"+line[1:]))
			} else {
				parts = append(parts, line)
			}
		}
	}

	parts = append(parts, sep)
	footer := tuiStyleFooter.Render("  ↑↓/jk navigate  enter/d detail  q quit")

	// Apply viewport with scroll offset so the cursor is always visible.
	lines := strings.Split(strings.Join(parts, "\n"), "\n")
	viewH := h - 1
	if viewH < 1 {
		viewH = 1
	}
	return strings.Join(scrollViewport(lines, m.dropletsScrollTop, viewH), "\n") + "\n" + footer
}

// viewDetail renders the Detail panel for the selected droplet.
// Layout (top to bottom):
//
//	[scrollable]  header (ID + title)
//	              repo · status · current step
//	              pipeline step indicator
//	              separator
//	              NOTES (count)
//	              timeline entries (oldest first)
//	[pinned]      footer hint bar
func (m tabAppModel) viewDetail() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}
	sep := strings.Repeat("─", w)

	// Build footer — replaced by overlay prompt when an action is in progress.
	var footer string
	switch m.overlayMode {
	case overlayConfirm:
		var prompt string
		switch m.overlayAction {
		case actionCancel:
			prompt = "cancel this droplet?"
		case actionEscalate:
			prompt = "escalate this droplet?"
		default:
			prompt = m.overlayAction + "?"
		}
		footer = tuiStyleYellow.Render("  " + prompt + "  (y/n)")
	case overlayText:
		var prompt string
		switch m.overlayAction {
		case actionRestart:
			prompt = "restart at cataractae"
		case actionAddNote:
			prompt = "note"
		case actionSetStep:
			prompt = "set step"
		default:
			prompt = m.overlayAction
		}
		footer = tuiStyleYellow.Render(fmt.Sprintf("  %s: %s_  (esc cancel)", prompt, m.overlayInput))
	default:
		if m.overlayErr != "" {
			footer = tuiStyleRed.Render("  error: " + m.overlayErr)
		} else {
			footer = tuiStyleFooter.Render("  esc back  jk scroll  g/G top  r restart  x cancel  e escalate  n note  s step")
		}
	}

	if m.detailDroplet == nil {
		return "  Loading…\n\n" + footer
	}
	d := m.detailDroplet

	var parts []string

	// Header: [ID]  Title
	parts = append(parts, tuiStyleHeader.Render(fmt.Sprintf("  [%s]  %s", d.ID, d.Title)))

	// Second line: repo · status · current step
	var statusStr string
	switch d.Status {
	case "in_progress":
		statusStr = tuiStyleGreen.Render("in_progress")
	case "stagnant":
		statusStr = tuiStyleRed.Render("stagnant")
	case "open":
		statusStr = tuiStyleYellow.Render("open")
	default:
		statusStr = tuiStyleDim.Render(d.Status)
	}
	curStep := d.CurrentCataractae
	if curStep == "" {
		curStep = "—"
	}
	parts = append(parts, fmt.Sprintf("  %s  ·  %s  ·  %s", d.Repo, statusStr, curStep))

	// Pipeline position indicator: implement → review → test (active step bold).
	if len(m.detailSteps) > 0 {
		ch := CataractaeInfo{
			DropletID: d.ID, // required by pipelineLabel to highlight the active step
			Step:      d.CurrentCataractae,
			Steps:     m.detailSteps,
		}
		avail := w - 4
		if avail < 40 {
			avail = 40
		}
		parts = append(parts, "  "+pipelineLabel(ch, avail, tuiStyleGreen, tuiStyleDim))
	}
	parts = append(parts, sep)

	// Notes timeline.
	noteCount := len(m.detailNotes)
	parts = append(parts, tuiStyleHeader.Render(fmt.Sprintf("  NOTES  (%d)", noteCount)))
	if m.detailErr != nil {
		parts = append(parts, tuiStyleRed.Render("  [error loading notes: "+m.detailErr.Error()+"]"))
	} else if noteCount == 0 {
		parts = append(parts, "  (no notes yet)")
	} else {
		for _, note := range m.detailNotes {
			ts := note.CreatedAt.Local().Format("2006-01-02 15:04")
			who := padRight("["+note.CataractaeName+"]", 18)
			content := strings.TrimSpace(note.Content)
			noteLines := strings.Split(content, "\n")
			parts = append(parts, fmt.Sprintf("  %s  %s  %s", ts, who, noteLines[0]))
			// Indent continuation lines of multi-line notes.
			const contIndent = "                                      "
			for _, l := range noteLines[1:] {
				if l = strings.TrimSpace(l); l != "" {
					parts = append(parts, contIndent+l)
				}
			}
			parts = append(parts, "") // blank spacer between notes
		}
		// Trim trailing blank line.
		for len(parts) > 0 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
	}

	// Apply viewport scroll; footer is pinned outside the scrolled region.
	lines := strings.Split(strings.Join(parts, "\n"), "\n")
	viewH := h - 1
	if viewH < 1 {
		viewH = 1
	}
	return strings.Join(scrollViewport(lines, m.detailScrollY, viewH), "\n") + "\n" + footer
}

// RunTabbedTUI launches the ct tui interactive panel using the alternate screen.
func RunTabbedTUI(cfgPath, dbPath string) error {
	m := newTabAppModel(cfgPath, dbPath)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive TUI — navigate droplets and inspect detail with notes timeline",
	Long: `Interactive TUI for the Cistern droplet queue.

Navigate the droplet list with ↑↓ (or j/k), press enter or d to open the
Detail panel for a selected droplet. The Detail panel shows the full notes
timeline and pipeline step indicator. Press esc to return to the list.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		dbPath := resolveDBPath()
		return RunTabbedTUI(cfgPath, dbPath)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
