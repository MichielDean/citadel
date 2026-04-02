package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/MichielDean/cistern/internal/cistern"
)

// Tab constants for tabAppModel.
const (
	tabDroplets = 0
	tabDetail   = 1
	tabPeek     = 2
)

// Overlay mode constants for action dispatch in the Detail panel.
const (
	overlayNone = iota
	overlayConfirm
	overlayText
	overlayMulti // sequential multi-field form (one field at a time)
)

// Action constants identify the pending Detail-panel action.
const (
	actionCancel         = "cancel"
	actionPool           = "pool"
	actionRestart        = "restart"
	actionAddNote        = "addnote"
	actionSetStep        = "setstep"
	actionPass           = "pass"
	actionRecirculate    = "recirculate"
	actionClose          = "close"
	actionReopen         = "reopen"
	actionApprove        = "approve"
	actionEditMeta       = "editmeta"       // multi-field: title, priority, complexity, description
	actionCreateDroplet  = "create"         // multi-field: repo, title, description, complexity
	actionAddDep         = "adddep"         // text: depends-on droplet ID
	actionRemoveDep      = "removedep"      // text: dependency ID to remove
	actionFileIssue      = "fileissue"      // text: issue description
	actionResolveIssue   = "resolveissue"   // text: evidence (issue selected via cursor)
	actionRejectIssue    = "rejectissue"    // text: evidence (issue selected via cursor)
)

// isTerminalStatus reports whether a droplet status string is terminal
// (delivered or cancelled). Used to guard actions that must not run on
// terminal droplets.
func isTerminalStatus(status string) bool {
	return status == "delivered" || status == "cancelled"
}

// tuiDetailDataMsg carries notes and issues fetched for the Detail panel.
// The dropletID field lets the handler discard stale responses when the user
// navigates away before the fetch completes.
// err is non-nil when the notes fetch failed; the Detail view displays an error
// indicator so the user is not misled into thinking the droplet has no notes.
type tuiDetailDataMsg struct {
	dropletID string
	notes     []cistern.CataractaeNote
	issues    []cistern.DropletIssue
	err       error
}

// tuiActionResultMsg carries the outcome of an async action dispatched from
// the Detail panel. dropletID lets the handler discard results for a different
// droplet if the user navigated away before the action completed.
type tuiActionResultMsg struct {
	dropletID string
	err       error
}

// tuiPaletteActionMsg is emitted by a palette action's Run function when the
// user selects an action in the command palette. The tabAppModel handles it by
// opening the Detail view for the target droplet and activating the matching
// action overlay (confirm or text-entry).
type tuiPaletteActionMsg struct {
	dropletID string
	action    string
}

// tabAppModel is the root Bubble Tea model for `ct tui`.
// It manages three views: the Droplets list, the Detail panel, and the Peek panel.
type tabAppModel struct {
	cfgPath string
	dbPath  string

	// Dashboard data — refreshed periodically via the standard tick chain.
	data *DashboardData

	// Active view: tabDroplets, tabDetail, or tabPeek.
	tab               int
	cursor            int // cursor position in the Droplets list
	dropletsScrollTop int // viewport line offset for the Droplets list

	// Detail panel state — populated when the Detail view opens.
	selectedID    string
	detailDroplet *cistern.Droplet
	detailNotes   []cistern.CataractaeNote // chronological order (oldest first)
	detailSteps   []string                 // pipeline step names for the droplet
	detailScrollY int
	detailErr     error // non-nil when the notes fetch failed

	// Action overlay state — populated when an action keybinding is pressed.
	overlayMode   int    // overlayNone, overlayConfirm, overlayText, or overlayMulti
	overlayAction string // pending action (actionCancel, actionPool, …)
	overlayInput  string // text being typed in overlayText/overlayMulti mode
	overlayErr    string // error message from the most recent action (empty = none)

	// Multi-field overlay state (overlayMulti mode): one field is entered at a
	// time; overlayInput holds the current field, overlayMultiValues accumulates
	// completed fields.
	overlayMultiFields []string // prompts for each field in sequence
	overlayMultiIdx    int      // index of the field currently being entered
	overlayMultiValues []string // collected values for completed fields

	// Issues in the Detail panel.
	detailIssues      []cistern.DropletIssue
	detailIssueCursor int // cursor within the issue list; -1 means no selection

	// pendingIssueID holds the issue ID targeted by a resolve/reject action
	// initiated from the inline issue list (set by v/u keys when a cursor is active).
	pendingIssueID string

	// Peek tab state — populated when the Peek view opens from the Detail panel.
	peek peekModel

	width  int
	height int
}

func newTabAppModel(cfgPath, dbPath string) tabAppModel {
	return tabAppModel{
		cfgPath:           cfgPath,
		dbPath:            dbPath,
		width:             100,
		height:            24,
		detailIssueCursor: -1,
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

// fetchDetailCmd opens the DB and loads all notes and issues for dropletID.
// Notes are returned newest-first by the DB; the Update handler reverses them.
// Issue fetch errors are non-fatal: the panel renders an empty list rather than
// masking a valid notes fetch with a secondary failure.
func (m tabAppModel) fetchDetailCmd(dropletID string) tea.Cmd {
	dbPath := m.dbPath
	return func() tea.Msg {
		c, err := cistern.New(dbPath, "")
		if err != nil {
			return tuiDetailDataMsg{dropletID: dropletID, err: err}
		}
		defer c.Close()
		notes, err := c.GetNotes(dropletID)
		if err != nil {
			return tuiDetailDataMsg{dropletID: dropletID, err: err}
		}
		issues, _ := c.ListIssues(dropletID, false, "")
		return tuiDetailDataMsg{dropletID: dropletID, notes: notes, issues: issues}
	}
}

// execActionCmd opens the DB and executes the named action for dropletID.
// input is the user-supplied text for text-entry actions; it is ignored for
// confirm-only actions (cancel, pool).
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
		case actionPool:
			execErr = c.Pool(dropletID, "")
		case actionRestart:
			execErr = c.Assign(dropletID, "", input)
		case actionAddNote:
			execErr = c.AddNote(dropletID, "manual", input)
		case actionSetStep:
			execErr = c.SetCataractae(dropletID, input)
		case actionPass:
			item, err := c.Get(dropletID)
			if err != nil {
				execErr = err
				break
			}
			if isTerminalStatus(item.Status) {
				execErr = fmt.Errorf("cannot %s: droplet %s has terminal status %q", action, dropletID, item.Status)
				break
			}
			if err := c.SetOutcome(dropletID, "pass"); err != nil {
				execErr = err
				break
			}
			if item.Status != "in_progress" {
				execErr = c.CloseItem(dropletID)
			}
		case actionRecirculate:
			item, err := c.Get(dropletID)
			if err != nil {
				execErr = err
				break
			}
			if isTerminalStatus(item.Status) {
				execErr = fmt.Errorf("cannot %s: droplet %s has terminal status %q", action, dropletID, item.Status)
				break
			}
			if item.Status != "in_progress" {
				target := input
				if target == "" {
					target = item.CurrentCataractae
				}
				execErr = c.Assign(dropletID, "", target)
				break
			}
			outcome := "recirculate"
			if input != "" {
				outcome = "recirculate:" + input
			}
			execErr = c.SetOutcome(dropletID, outcome)
		case actionClose:
			execErr = c.CloseItem(dropletID)
		case actionReopen:
			execErr = c.UpdateStatus(dropletID, "open")
		case actionApprove:
			item, err := c.Get(dropletID)
			if err != nil {
				execErr = err
				break
			}
			if isTerminalStatus(item.Status) {
				execErr = fmt.Errorf("cannot %s: droplet %s has terminal status %q", action, dropletID, item.Status)
				break
			}
			if item.CurrentCataractae != "human" {
				execErr = fmt.Errorf("%s is not awaiting human approval (cataractae: %s)", dropletID, item.CurrentCataractae)
				break
			}
			execErr = c.Assign(dropletID, "", "delivery")
		case actionAddDep:
			execErr = c.AddDependency(dropletID, input)
		case actionRemoveDep:
			execErr = c.RemoveDependency(dropletID, input)
		case actionFileIssue:
			_, execErr = c.AddIssue(dropletID, "tui", input)
		}
		return tuiActionResultMsg{dropletID: dropletID, err: execErr}
	}
}

// execMultiActionCmd executes a multi-field action whose inputs were collected
// via the overlayMulti sequential form. values contains one entry per field in
// the order they were entered.
func (m tabAppModel) execMultiActionCmd(action string, values []string) tea.Cmd {
	dbPath := m.dbPath
	selectedID := m.selectedID
	return func() tea.Msg {
		c, err := cistern.New(dbPath, "")
		if err != nil {
			return tuiActionResultMsg{dropletID: selectedID, err: err}
		}
		defer c.Close()
		var execErr error
		switch action {
		case actionCreateDroplet:
			// values: [repo, title, description, complexity]
			repo := strings.TrimSpace(valAt(values, 0))
			title := strings.TrimSpace(valAt(values, 1))
			description := strings.TrimSpace(valAt(values, 2))
			complexity := 1
			if n, err := strconv.Atoi(strings.TrimSpace(valAt(values, 3))); err == nil && n >= 1 && n <= 3 {
				complexity = n
			}
			if repo == "" || title == "" {
				execErr = fmt.Errorf("repo and title are required to create a droplet")
				break
			}
			_, execErr = c.Add(repo, title, description, 1, complexity)
		case actionEditMeta:
			// values: [title, priority, complexity, description]
			// Each field is optional — skip empty/invalid values.
			title := strings.TrimSpace(valAt(values, 0))
			fields := cistern.EditDropletFields{}
			if p, err := strconv.Atoi(strings.TrimSpace(valAt(values, 1))); err == nil && p > 0 {
				fields.Priority = &p
			}
			if cx, err := strconv.Atoi(strings.TrimSpace(valAt(values, 2))); err == nil && cx >= 1 && cx <= 3 {
				fields.Complexity = &cx
			}
			if desc := strings.TrimSpace(valAt(values, 3)); desc != "" {
				fields.Description = &desc
			}
			hasEditFields := fields.Description != nil || fields.Priority != nil || fields.Complexity != nil
			// Guard: EditDroplet rejects in_progress/delivered droplets. Check
			// status before touching anything so no partial update occurs.
			if hasEditFields {
				item, err := c.Get(selectedID)
				if err != nil {
					execErr = err
					break
				}
				if item.Status != "open" && item.Status != "pooled" {
					execErr = fmt.Errorf("droplet %s is %s — cannot edit a droplet that has been picked up", selectedID, item.Status)
					break
				}
			}
			if title != "" {
				if err := c.UpdateTitle(selectedID, title); err != nil {
					execErr = err
					break
				}
			}
			if hasEditFields {
				execErr = c.EditDroplet(selectedID, fields)
			}
		case actionResolveIssue, actionRejectIssue:
			// values: [issue_id, evidence] (from palette multi-step path)
			issueID := strings.TrimSpace(valAt(values, 0))
			evidence := strings.TrimSpace(valAt(values, 1))
			if issueID == "" {
				execErr = fmt.Errorf("issue ID is required")
				break
			}
			if action == actionResolveIssue {
				execErr = c.ResolveIssue(issueID, evidence)
			} else {
				execErr = c.RejectIssue(issueID, evidence)
			}
		}
		return tuiActionResultMsg{dropletID: selectedID, err: execErr}
	}
}

// valAt returns values[i] if i is within bounds, otherwise "".
func valAt(values []string, i int) string {
	if i < len(values) {
		return values[i]
	}
	return ""
}

// openMultiOverlay activates overlayMulti mode with the given field prompts.
// overlayAction must be set by the caller before or after.
func openMultiOverlay(m tabAppModel, fields []string) tabAppModel {
	m.overlayMode = overlayMulti
	m.overlayMultiFields = fields
	m.overlayMultiIdx = 0
	m.overlayMultiValues = make([]string, len(fields))
	m.overlayInput = ""
	return m
}

// openCreateDropletOverlay puts m into overlayMulti mode for the new-droplet
// creation form. Callers set m.tab as needed before calling.
func openCreateDropletOverlay(m tabAppModel) tabAppModel {
	m.overlayAction = actionCreateDroplet
	return openMultiOverlay(m, []string{"repo", "title", "description", "complexity (1-3)"})
}

// overlayMultiFooter renders the progress footer shown during a multi-field form.
func (m tabAppModel) overlayMultiFooter() string {
	step := ""
	if m.overlayMultiIdx < len(m.overlayMultiFields) {
		step = m.overlayMultiFields[m.overlayMultiIdx]
	}
	return tuiStyleYellow.Render(fmt.Sprintf("  [%d/%d] %s: %s_  (esc cancel)",
		m.overlayMultiIdx+1, len(m.overlayMultiFields), step, m.overlayInput))
}

// closeOverlay resets all overlay state to inactive.
func closeOverlay(m tabAppModel) tabAppModel {
	m.overlayMode = overlayNone
	m.overlayAction = ""
	m.overlayInput = ""
	m.overlayMultiFields = nil
	m.overlayMultiIdx = 0
	m.overlayMultiValues = nil
	m.pendingIssueID = ""
	return m
}

// handleOverlayKey routes a key event to the active overlay (confirm, text-entry,
// or multi-field sequential form). It is only called when overlayMode != overlayNone.
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
			// Evidence is optional for resolve/reject; recirculate allows empty
			// to mean "use current step". All other actions require input.
			emptyAllowed := m.overlayAction == actionRecirculate ||
				m.overlayAction == actionResolveIssue ||
				m.overlayAction == actionRejectIssue
			if m.overlayInput == "" && !emptyAllowed {
				break // empty input is a no-op
			}
			action := m.overlayAction
			id := m.selectedID
			input := m.overlayInput
			issueID := m.pendingIssueID
			m = closeOverlay(m)
			if action == actionResolveIssue || action == actionRejectIssue {
				return m, m.execMultiActionCmd(action, []string{issueID, input})
			}
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
	case overlayMulti:
		switch s {
		case "esc":
			m = closeOverlay(m)
		case "enter":
			// Save the current field value.
			if m.overlayMultiIdx < len(m.overlayMultiValues) {
				m.overlayMultiValues[m.overlayMultiIdx] = m.overlayInput
			}
			if m.overlayMultiIdx < len(m.overlayMultiFields)-1 {
				// More fields remain — advance to the next one.
				m.overlayMultiIdx++
				m.overlayInput = ""
			} else {
				// Last field — collect values and execute.
				action := m.overlayAction
				values := make([]string, len(m.overlayMultiValues))
				copy(values, m.overlayMultiValues)
				m = closeOverlay(m)
				return m, m.execMultiActionCmd(action, values)
			}
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
	m.detailIssues = nil
	m.detailIssueCursor = -1
	m.detailScrollY = 0
	m.detailErr = nil
	m.detailSteps = m.findStepsForDroplet(dropletID)
	return m, m.fetchDetailCmd(dropletID)
}

// openPeek switches to the Peek tab for the currently selected droplet.
// If the droplet is flowing (has a matching CataractaeInfo), a live capture-pane
// session is started. If not flowing, a placeholder peek model is created with an
// empty session so the view shows "(session not active)" gracefully.
func (m tabAppModel) openPeek() (tabAppModel, tea.Cmd) {
	m.tab = tabPeek

	// Find the CataractaeInfo for the selected droplet.
	var ch *CataractaeInfo
	if m.data != nil && m.selectedID != "" {
		for i := range m.data.Cataractae {
			if m.data.Cataractae[i].DropletID == m.selectedID {
				ch = &m.data.Cataractae[i]
				break
			}
		}
	}

	var session, header string
	if ch == nil {
		if m.selectedID == "" {
			header = "(no droplet selected)"
		} else {
			header = fmt.Sprintf("[%s] — not flowing, no agent session", m.selectedID)
		}
	} else {
		session = ch.RepoName + "-" + ch.Name
		header = fmt.Sprintf("[%s] %s — flowing %s", ch.DropletID, ch.Step, formatElapsed(ch.Elapsed))
	}

	pk := newPeekModel(defaultCapturer, session, header, defaultPeekLines)
	pk.width = m.width
	pk.height = m.height - 1
	m.peek = pk

	if ch == nil {
		return m, nil
	}
	return m, m.peek.Init()
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
	n++ // separator before issues
	n++ // "ISSUES (count)" heading
	if len(m.detailIssues) == 0 {
		n++ // "(no issues)"
	} else {
		n += len(m.detailIssues) // one line per issue
	}
	n++ // separator before notes
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
// tuiActionResultMsg and tuiPaletteActionMsg are handled here before tab
// dispatch so they are never silently dropped when the user navigates away
// from the Detail panel while an async action is in-flight.
func (m tabAppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ar, ok := msg.(tuiActionResultMsg); ok {
		// dropletID=="" means a global action (e.g. create droplet) — always
		// handle. A non-empty ID that differs from selectedID is a stale result.
		if ar.dropletID != "" && ar.dropletID != m.selectedID {
			return m, nil
		}
		if ar.err != nil {
			m.overlayErr = ar.err.Error()
		} else {
			m.overlayErr = ""
		}
		if m.selectedID != "" {
			return m, tea.Batch(m.fetchDataCmd(), m.fetchDetailCmd(m.selectedID))
		}
		return m, m.fetchDataCmd()
	}
	if pm, ok := msg.(tuiPaletteActionMsg); ok {
		// actionCreateDroplet requires no selected droplet — handle before the
		// findDroplet guard.
		if pm.action == actionCreateDroplet {
			m.tab = tabDroplets
			m = openCreateDropletOverlay(m)
			return m, nil
		}
		if m.findDroplet(pm.dropletID) == nil {
			return m, nil // droplet not in data — no-op
		}
		updated, cmd := m.openDetail(pm.dropletID)
		if updated.detailDroplet != nil {
			updated.overlayAction = pm.action
			switch pm.action {
			case actionCancel, actionPool, actionPass, actionClose, actionReopen, actionApprove:
				updated.overlayMode = overlayConfirm
			case actionRestart, actionAddNote, actionSetStep, actionRecirculate,
				actionAddDep, actionRemoveDep, actionFileIssue:
				updated.overlayMode = overlayText
			case actionEditMeta:
				updated = openMultiOverlay(updated, []string{"title", "priority (1-5)", "complexity (1-3)", "description"})
			case actionResolveIssue, actionRejectIssue:
				updated = openMultiOverlay(updated, []string{"issue ID", "evidence"})
			}
		}
		return updated, cmd
	}
	switch m.tab {
	case tabDetail:
		return m.updateDetail(msg)
	case tabPeek:
		return m.updatePeek(msg)
	default:
		return m.updateDroplets(msg)
	}
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
		// When an overlay is active on the Droplets tab (e.g. create form), route
		// all key events to the overlay handler so the form captures input.
		if m.overlayMode != overlayNone {
			return m.handleOverlayKey(msg)
		}
		// Clear any prior action error on the next keypress.
		m.overlayErr = ""
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
		case "N":
			// Open multi-field creation form for a new droplet.
			m = openCreateDropletOverlay(m)
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
				m.detailIssues = msg.issues
				// Clamp issue cursor in case the issue list shrunk.
				if m.detailIssueCursor >= len(m.detailIssues) {
					m.detailIssueCursor = len(m.detailIssues) - 1
				}
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
		case "p":
			// Open Peek tab for the selected droplet.
			return m.openPeek()
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
				m.overlayAction = actionPool
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
		case "]":
			// Advance issue cursor forward (initialises to 0 if unset).
			if m.detailIssueCursor < len(m.detailIssues)-1 {
				m.detailIssueCursor++
			}
		case "[":
			// Move issue cursor backward.
			if m.detailIssueCursor > 0 {
				m.detailIssueCursor--
			}
		case "v", "u":
			// v resolves the selected issue; u rejects it — both prompt for evidence.
			if m.detailDroplet != nil && m.detailIssueCursor >= 0 && m.detailIssueCursor < len(m.detailIssues) {
				m.pendingIssueID = m.detailIssues[m.detailIssueCursor].ID
				m.overlayMode = overlayText
				if msg.String() == "v" {
					m.overlayAction = actionResolveIssue
				} else {
					m.overlayAction = actionRejectIssue
				}
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

// updatePeek handles messages while the Peek tab is active.
// Peek-specific messages (peekTickMsg, peekContentMsg) are forwarded to the
// embedded peekModel. Window resizes propagate size to the peek model,
// reserving one row for the Peek tab's own footer. Esc returns to the Detail tab.
func (m tabAppModel) updatePeek(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve one row for the footer; set dimensions directly to avoid
		// the peekModel overriding height with the raw terminal height.
		m.peek.width = m.width
		m.peek.height = m.height - 1
		return m, nil

	case tuiDataMsg:
		m.data = (*DashboardData)(msg)
		return m, tuiTickWithInterval(refreshInterval)

	case tuiTickMsg:
		return m, m.fetchDataCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.tab = tabDetail
			return m, nil
		}
	}

	// Forward all other messages (peek-specific tick/content and unhandled keys)
	// to the embedded model so the refresh loop and scroll controls keep working.
	updated, cmd := m.peek.Update(msg)
	m.peek = updated.(peekModel)
	return m, cmd
}

// View dispatches to the active tab's renderer.
func (m tabAppModel) View() string {
	if m.data == nil {
		return "  Loading…\n"
	}
	switch m.tab {
	case tabDetail:
		return m.viewDetail()
	case tabPeek:
		return m.viewPeek()
	default:
		return m.viewDroplets()
	}
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

	var footer string
	if m.overlayMode == overlayMulti {
		footer = m.overlayMultiFooter()
	} else if m.overlayErr != "" {
		footer = tuiStyleRed.Render("  error: " + m.overlayErr)
	} else {
		footer = tuiStyleFooter.Render("  ↑↓/jk navigate  enter/d detail  N new  q quit")
	}

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
		case actionPool:
			prompt = "pool this droplet?"
		case actionPass:
			prompt = "pass this droplet?"
		case actionClose:
			prompt = "close this droplet?"
		case actionReopen:
			prompt = "reopen this droplet?"
		case actionApprove:
			prompt = "approve this droplet?"
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
		case actionRecirculate:
			prompt = "recirculate to step"
		case actionAddDep:
			prompt = "add dependency (droplet ID)"
		case actionRemoveDep:
			prompt = "remove dependency (droplet ID)"
		case actionFileIssue:
			prompt = "file issue (description)"
		case actionResolveIssue:
			prompt = "resolve evidence"
		case actionRejectIssue:
			prompt = "reject evidence"
		default:
			prompt = m.overlayAction
		}
		footer = tuiStyleYellow.Render(fmt.Sprintf("  %s: %s_  (esc cancel)", prompt, m.overlayInput))
	case overlayMulti:
		footer = m.overlayMultiFooter()
	default:
		if m.overlayErr != "" {
			footer = tuiStyleRed.Render("  error: " + m.overlayErr)
		} else {
			footer = tuiStyleFooter.Render("  esc back  ↑↓/jk scroll  g/G top/bottom  p peek  r restart  x cancel  e pool  n note  s step  [/] issue  v resolve  u reject")
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
	case "pooled":
		statusStr = tuiStyleRed.Render("pooled")
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

	// Issues sub-section. Each issue is shown on one line with a cursor indicator.
	// Use ] / [ to navigate; v/u to resolve or reject the selected issue.
	issueCount := len(m.detailIssues)
	parts = append(parts, tuiStyleHeader.Render(fmt.Sprintf("  ISSUES  (%d)", issueCount)))
	if issueCount == 0 {
		parts = append(parts, tuiStyleDim.Render("  (no issues)"))
	} else {
		const issueDescMax = 50
		for i, iss := range m.detailIssues {
			ts := iss.FlaggedAt.Local().Format("2006-01-02")
			desc := iss.Description
			if len([]rune(desc)) > issueDescMax {
				desc = string([]rune(desc)[:issueDescMax-1]) + "…"
			}
			var statusRendered string
			switch iss.Status {
			case "open":
				statusRendered = tuiStyleYellow.Render(iss.Status)
			case "resolved":
				statusRendered = tuiStyleGreen.Render(iss.Status)
			default:
				statusRendered = tuiStyleDim.Render(iss.Status)
			}
			cursor := "  "
			if i == m.detailIssueCursor {
				cursor = tuiStyleGreen.Render("▶ ")
			}
			line := fmt.Sprintf("%s%-8s  %s  %s  %s",
				cursor, statusRendered, ts, iss.ID, desc)
			parts = append(parts, line)
		}
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

// viewPeek renders the Peek tab, delegating content to the embedded peekModel
// and appending a pinned footer with navigation hints.
func (m tabAppModel) viewPeek() string {
	// Ensure peek model dimensions are current before rendering.
	m.peek.width = m.width
	if m.height > 1 {
		m.peek.height = m.height - 1
	}
	footer := tuiStyleFooter.Render("  esc detail  space toggle-pin  ↑↓/jk scroll  q quit")
	return m.peek.View() + "\n" + footer
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive TUI — navigate droplets and inspect detail with notes timeline",
	Long: `Interactive TUI for the Cistern droplet queue.

Navigate the droplet list with ↑↓ (or j/k), press enter or d to open the
Detail panel for a selected droplet. The Detail panel shows the full notes
timeline and pipeline step indicator. Press esc to return to the list.

From the Detail panel, press p to peek at the live agent session output.
Press esc to return.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath := resolveConfigPath()
		dbPath := resolveDBPath()
		return RunCockpitTUI(cfgPath, dbPath)
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
