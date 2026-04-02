package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// ── cockpitModel initial state ───────────────────────────────────────────────

// TestCockpit_NewModel_CursorStartsAtZero verifies that a freshly constructed
// cockpitModel has the sidebar cursor on the first panel.
//
// Given: a new cockpitModel
// When:  no messages have been processed
// Then:  cursor = 0
func TestCockpit_NewModel_CursorStartsAtZero(t *testing.T) {
	m := newCockpitModel("", "")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

// TestCockpit_NewModel_DropletsPreSelected verifies that the cockpit starts with
// the Droplets panel already focused, so ct tui lands the user in the droplets
// list without requiring an extra keypress (identical UX to the pre-cockpit tui).
//
// Given: a new cockpitModel
// When:  no messages have been processed
// Then:  panelFocused = true (Droplets panel is pre-selected)
func TestCockpit_NewModel_DropletsPreSelected(t *testing.T) {
	m := newCockpitModel("", "")
	if !m.panelFocused {
		t.Error("panelFocused = false, want true (Droplets panel should be pre-selected)")
	}
}

// TestCockpit_NewModel_HasSixPanels verifies the cockpit ships with the expected
// set of panels.
//
// Given: a new cockpitModel
// When:  panels are inspected
// Then:  eight panels are registered
func TestCockpit_NewModel_HasEightPanels(t *testing.T) {
	m := newCockpitModel("", "")
	if len(m.panels) != 8 {
		t.Errorf("len(panels) = %d, want 8", len(m.panels))
	}
}

// ── sidebar navigation ───────────────────────────────────────────────────────

// TestCockpit_Sidebar_Down_MovesToNextPanel verifies that pressing 'j' in sidebar
// mode advances the cursor to the next panel.
//
// Given: cursor=0, panelFocused=false
// When:  'j' is pressed
// Then:  cursor = 1
func TestCockpit_Sidebar_Down_MovesToNextPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(cockpitModel)

	if um.cursor != 1 {
		t.Errorf("cursor = %d, want 1", um.cursor)
	}
}

// TestCockpit_Sidebar_DownArrow_MovesToNextPanel verifies that the down arrow key
// also advances the cursor.
//
// Given: cursor=0, panelFocused=false
// When:  down arrow is pressed
// Then:  cursor = 1
func TestCockpit_Sidebar_DownArrow_MovesToNextPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	um := updated.(cockpitModel)

	if um.cursor != 1 {
		t.Errorf("cursor = %d, want 1", um.cursor)
	}
}

// TestCockpit_Sidebar_Down_AtLastPanel_Stays verifies that pressing 'j' at the
// last panel does not advance the cursor past the end.
//
// Given: cursor = last panel index, panelFocused=false
// When:  'j' is pressed
// Then:  cursor stays at last panel index
func TestCockpit_Sidebar_Down_AtLastPanel_Stays(t *testing.T) {
	m := newCockpitModel("", "")
	last := len(m.panels) - 1
	m.cursor = last
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(cockpitModel)

	if um.cursor != last {
		t.Errorf("cursor = %d, want %d (should not advance past last panel)", um.cursor, last)
	}
}

// TestCockpit_Sidebar_Up_MovesToPreviousPanel verifies that pressing 'k' in
// sidebar mode moves the cursor to the previous panel.
//
// Given: cursor=1, panelFocused=false
// When:  'k' is pressed
// Then:  cursor = 0
func TestCockpit_Sidebar_Up_MovesToPreviousPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 1
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0", um.cursor)
	}
}

// TestCockpit_Sidebar_Up_AtFirstPanel_Stays verifies that pressing 'k' at the
// first panel does not move the cursor before 0.
//
// Given: cursor=0, panelFocused=false
// When:  'k' is pressed
// Then:  cursor stays at 0
func TestCockpit_Sidebar_Up_AtFirstPanel_Stays(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (should not go below first panel)", um.cursor)
	}
}

// TestCockpit_Sidebar_Enter_ActivatesPanelFocus verifies that pressing Enter in
// sidebar mode sets panelFocused=true.
//
// Given: panelFocused=false
// When:  Enter is pressed
// Then:  panelFocused = true
func TestCockpit_Sidebar_Enter_ActivatesPanelFocus(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(cockpitModel)

	if !um.panelFocused {
		t.Error("panelFocused = false, want true after Enter in sidebar mode")
	}
}

// TestCockpit_Sidebar_Enter_CursorUnchanged verifies that pressing Enter does not
// change the cursor position.
//
// Given: cursor=2, panelFocused=false
// When:  Enter is pressed
// Then:  cursor stays at 2
func TestCockpit_Sidebar_Enter_CursorUnchanged(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 2
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(cockpitModel)

	if um.cursor != 2 {
		t.Errorf("cursor = %d, want 2", um.cursor)
	}
}

// ── number key jumps ─────────────────────────────────────────────────────────

// TestCockpit_NumberKey_JumpsToPanel verifies that pressing '2' activates
// the second panel (index 1) and enables panel focus.
//
// Given: cursor=0, panelFocused=false
// When:  '2' is pressed
// Then:  cursor=1, panelFocused=true
func TestCockpit_NumberKey_JumpsToPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	um := updated.(cockpitModel)

	if um.cursor != 1 {
		t.Errorf("cursor = %d, want 1", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after number key jump")
	}
}

// TestCockpit_NumberKey_1_ActivatesFirstPanel verifies that '1' activates panel 0.
//
// Given: cursor=3
// When:  '1' is pressed
// Then:  cursor=0, panelFocused=true
func TestCockpit_NumberKey_1_ActivatesFirstPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 3

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0", um.cursor)
	}
}

// TestCockpit_NumberKey_OutOfRange_NoChange verifies that pressing '9' when fewer
// than 9 panels exist does not change cursor or focus.
//
// Given: cockpit with 5 panels, cursor=0, panelFocused=false (sidebar mode)
// When:  '9' is pressed
// Then:  cursor=0, panelFocused=false (unchanged)
func TestCockpit_NumberKey_OutOfRange_NoChange(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'9'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (out-of-range key should not change cursor)", um.cursor)
	}
	if um.panelFocused {
		t.Error("panelFocused = true, want false (out-of-range key should not activate panel)")
	}
}

// TestCockpit_NumberKey_FromPanelMode_JumpsToPanel verifies that pressing a digit
// key while a panel is focused switches to the corresponding panel, enabling
// "press 1 from any module to return to Droplets" navigation.
//
// Given: panelFocused=true, cursor=0, no overlay active
// When:  '2' is pressed
// Then:  cursor=1, panelFocused=true
func TestCockpit_NumberKey_FromPanelMode_JumpsToPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = true
	m.panels[0] = placeholderPanel{title: "Test"} // OverlayActive() == false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	um := updated.(cockpitModel)

	if um.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (digit key should jump to panel from panel mode)", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after panel-mode number key jump")
	}
}

// TestCockpit_Key1_FromPanelMode_ReturnsToDroplets verifies the acceptance criterion:
// pressing '1' from any module returns the user to the Droplets panel.
//
// Given: panelFocused=true, cursor=2 (some other panel), no overlay active
// When:  '1' is pressed
// Then:  cursor=0 (Droplets), panelFocused=true
func TestCockpit_Key1_FromPanelMode_ReturnsToDroplets(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 2
	m.panelFocused = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (pressing 1 from any module should return to Droplets)", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true")
	}
}

// TestCockpit_NumberKey_ForwardedToPanel_WhenOverlayActive verifies that digit keys
// are forwarded to the active panel when an overlay is open, so text overlays can
// receive digit input without triggering panel switching.
//
// Given: panelFocused=true, cursor=0, overlay is active
// When:  '2' is pressed
// Then:  cursor remains 0 (digit was forwarded to panel, not intercepted)
func TestCockpit_NumberKey_ForwardedToPanel_WhenOverlayActive(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = true
	m.panels[0] = overlayActivePanel{placeholderPanel{title: "Test"}} // OverlayActive() == true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (digit must not switch panels when overlay is active)", um.cursor)
	}
}

// ── tab focus toggle ─────────────────────────────────────────────────────────

// TestCockpit_Tab_EnablesPanelFocus verifies that Tab from sidebar mode enables
// panel focus.
//
// Given: panelFocused=false
// When:  Tab is pressed
// Then:  panelFocused=true
func TestCockpit_Tab_EnablesPanelFocus(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	um := updated.(cockpitModel)

	if !um.panelFocused {
		t.Error("panelFocused = false, want true after Tab from sidebar mode")
	}
}

// TestCockpit_Tab_ForwardedToPanel_WhenPanelFocused verifies that Tab is forwarded
// to the active panel when panelFocused=true, leaving cockpit state unchanged, so
// overlays in tabAppModel are not left in a stuck/unreachable state.
//
// Given: panelFocused=true, active panel is a placeholderPanel
// When:  Tab is pressed
// Then:  panelFocused remains true (tab was not intercepted by cockpit)
func TestCockpit_Tab_ForwardedToPanel_WhenPanelFocused(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true
	m.panels[0] = placeholderPanel{title: "Test"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	um := updated.(cockpitModel)

	if !um.panelFocused {
		t.Error("panelFocused = false, want true (tab should not un-focus panel when panel is focused)")
	}
}

// ── quit ─────────────────────────────────────────────────────────────────────

// TestCockpit_Q_Quits verifies that pressing 'q' returns tea.Quit in sidebar mode.
//
// Given: panelFocused=false
// When:  'q' is pressed
// Then:  returned command is tea.Quit
func TestCockpit_Q_Quits(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = false

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("cmd() = %v, want tea.Quit()", msg)
	}
}

// TestCockpit_Q_ForwardedToPanel_WhenPanelFocused verifies that 'q' is forwarded
// to the active panel when panelFocused=true, so overlayText/overlayConfirm modes
// in tabAppModel can receive 'q' as character input or dismiss dialogs.
//
// Given: panelFocused=true, active panel is a placeholderPanel
// When:  'q' is pressed
// Then:  cockpit does NOT quit; cmd is nil (placeholder ignores the key)
func TestCockpit_Q_ForwardedToPanel_WhenPanelFocused(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true
	m.panels[0] = placeholderPanel{title: "Test"}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})

	if cmd != nil {
		msg := cmd()
		if msg == tea.Quit() {
			t.Error("cmd() = tea.Quit, want nil (q must not quit when panel is focused)")
		}
	}
}

// TestCockpit_CtrlC_Quits_WhenPanelFocused verifies that ctrl+c always quits
// even when a panel has focus.
//
// Given: panelFocused=true
// When:  ctrl+c is pressed
// Then:  returned command is tea.Quit
func TestCockpit_CtrlC_Quits_WhenPanelFocused(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("cmd() = %v, want tea.Quit()", msg)
	}
}

// ── panel focus forwarding ────────────────────────────────────────────────────

// TestCockpit_PanelFocused_DownKey_DoesNotMoveSidebarCursor verifies that when
// a panel has focus, 'j' is forwarded to the panel and does not move the cockpit
// sidebar cursor.
//
// Given: cursor=0, panelFocused=true (active panel is a placeholderPanel)
// When:  'j' is pressed
// Then:  cockpit cursor stays at 0 (placeholder ignores the key; sidebar unaffected)
func TestCockpit_PanelFocused_DownKey_DoesNotMoveSidebarCursor(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = true
	// Override to a known placeholder so the inner panel is deterministic.
	m.panels[0] = placeholderPanel{title: "Test"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (sidebar must not move when panel is focused)", um.cursor)
	}
}

// ── window size ───────────────────────────────────────────────────────────────

// TestCockpit_WindowSizeMsg_UpdatesDimensions verifies that a WindowSizeMsg
// updates the cockpit dimensions.
//
// Given: a cockpit with default dimensions
// When:  a WindowSizeMsg{Width:120, Height:40} is received
// Then:  width=120, height=40
func TestCockpit_WindowSizeMsg_UpdatesDimensions(t *testing.T) {
	m := newCockpitModel("", "")

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(cockpitModel)

	if um.width != 120 {
		t.Errorf("width = %d, want 120", um.width)
	}
	if um.height != 40 {
		t.Errorf("height = %d, want 40", um.height)
	}
}

// ── View rendering ────────────────────────────────────────────────────────────

// TestCockpit_View_ContainsSidebarTitle verifies that the cockpit View includes
// the "CISTERN" sidebar heading.
//
// Given: a new cockpitModel
// When:  View is called
// Then:  output contains "CISTERN"
func TestCockpit_View_ContainsSidebarTitle(t *testing.T) {
	m := newCockpitModel("", "")
	v := m.View()
	if !strings.Contains(v, "CISTERN") {
		t.Error("View() does not contain 'CISTERN'")
	}
}

// TestCockpit_View_ContainsPanelTitles verifies that all panel titles appear in
// the sidebar.
//
// Given: a new cockpitModel
// When:  View is called
// Then:  output contains each panel's title
func TestCockpit_View_ContainsPanelTitles(t *testing.T) {
	m := newCockpitModel("", "")
	v := m.View()
	for _, p := range m.panels {
		if !strings.Contains(v, p.Title()) {
			t.Errorf("View() does not contain panel title %q", p.Title())
		}
	}
}

// TestCockpit_View_ContainsSeparator verifies that the View includes the │
// separator between sidebar and panel.
//
// Given: a new cockpitModel
// When:  View is called
// Then:  output contains the │ column separator
func TestCockpit_View_ContainsSeparator(t *testing.T) {
	m := newCockpitModel("", "")
	v := m.View()
	if !strings.Contains(v, "│") {
		t.Error("View() does not contain '│' column separator")
	}
}

// ── placeholderPanel ─────────────────────────────────────────────────────────

// TestPlaceholderPanel_Title_ReturnsConfiguredTitle verifies that Title returns
// the string passed at construction.
//
// Given: placeholderPanel{title: "Widgets"}
// When:  Title() is called
// Then:  "Widgets" is returned
func TestPlaceholderPanel_Title_ReturnsConfiguredTitle(t *testing.T) {
	p := placeholderPanel{title: "Widgets"}
	if p.Title() != "Widgets" {
		t.Errorf("Title() = %q, want %q", p.Title(), "Widgets")
	}
}

// TestPlaceholderPanel_View_ContainsNotYetImplemented verifies that the
// placeholder view communicates that the module is not yet available.
//
// Given: any placeholderPanel
// When:  View() is called
// Then:  output contains "not yet implemented"
func TestPlaceholderPanel_View_ContainsNotYetImplemented(t *testing.T) {
	p := placeholderPanel{title: "Future"}
	v := p.View()
	if !strings.Contains(v, "not yet implemented") {
		t.Errorf("View() = %q, want it to contain 'not yet implemented'", v)
	}
}

// TestPlaceholderPanel_KeyHelp_ReturnsEmpty verifies that placeholders have no
// key hints to contribute to the footer.
//
// Given: any placeholderPanel
// When:  KeyHelp() is called
// Then:  "" is returned
func TestPlaceholderPanel_KeyHelp_ReturnsEmpty(t *testing.T) {
	p := placeholderPanel{title: "Future"}
	if got := p.KeyHelp(); got != "" {
		t.Errorf("KeyHelp() = %q, want %q", got, "")
	}
}

// TestPlaceholderPanel_PaletteActions_ReturnsNil verifies that placeholders
// have no palette actions.
//
// Given: any placeholderPanel, any droplet
// When:  PaletteActions(droplet) is called
// Then:  nil is returned
func TestPlaceholderPanel_PaletteActions_ReturnsNil(t *testing.T) {
	p := placeholderPanel{title: "Future"}
	got := p.PaletteActions(&cistern.Droplet{ID: "ci-aaa"})
	if got != nil {
		t.Errorf("PaletteActions() = %v, want nil", got)
	}
}

// TestPlaceholderPanel_Update_ReturnsUnchangedModel verifies that Update is a
// no-op — the same model is returned and no command is issued.
//
// Given: any placeholderPanel
// When:  Update(any key msg) is called
// Then:  returned model equals original, cmd is nil
func TestPlaceholderPanel_Update_ReturnsUnchangedModel(t *testing.T) {
	p := placeholderPanel{title: "Future"}
	updated, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if updated.(placeholderPanel).title != p.title {
		t.Errorf("updated title = %q, want %q", updated.(placeholderPanel).title, p.title)
	}
	if cmd != nil {
		t.Error("cmd is non-nil, want nil")
	}
}

// ── dropletsPanel ────────────────────────────────────────────────────────────

// TestDropletsPanel_Title_ReturnsDroplets verifies the panel's display name.
//
// Given: a dropletsPanel
// When:  Title() is called
// Then:  "Droplets" is returned
func TestDropletsPanel_Title_ReturnsDroplets(t *testing.T) {
	p := newDropletsPanel("", "")
	if got := p.Title(); got != "Droplets" {
		t.Errorf("Title() = %q, want %q", got, "Droplets")
	}
}

// TestDropletsPanel_PaletteActions_WithDroplet_ReturnsActions verifies that
// actions are returned when a droplet is provided.
//
// Given: a dropletsPanel with a non-nil droplet
// When:  PaletteActions(droplet) is called
// Then:  a non-empty slice of PaletteActions is returned
func TestDropletsPanel_PaletteActions_WithDroplet_ReturnsActions(t *testing.T) {
	p := newDropletsPanel("", "")
	actions := p.PaletteActions(&cistern.Droplet{ID: "ci-aaa"})
	if len(actions) == 0 {
		t.Error("PaletteActions(droplet) returned empty slice, want at least one action")
	}
}

// TestDropletsPanel_PaletteActions_WithNilDroplet_ReturnsNil verifies that no
// actions are returned when no droplet is in context.
//
// Given: a dropletsPanel
// When:  PaletteActions(nil) is called
// Then:  nil is returned
func TestDropletsPanel_PaletteActions_WithNilDroplet_ReturnsNil(t *testing.T) {
	p := newDropletsPanel("", "")
	if got := p.PaletteActions(nil); got != nil {
		t.Errorf("PaletteActions(nil) = %v, want nil", got)
	}
}

// ── joinSideBySide ────────────────────────────────────────────────────────────

// TestJoinSideBySide_PadsSidebarToWidth verifies that each line of the sidebar
// column is padded to exactly sidebarW visual columns before the separator.
//
// Given: sidebar = "ab\n", panel = "XY\n", sidebarW = 5
// When:  joinSideBySide is called
// Then:  the first line starts with "ab   │XY" (sidebar padded to 5 with spaces)
func TestJoinSideBySide_PadsSidebarToWidth(t *testing.T) {
	result := joinSideBySide("ab\n", "XY\n", 5)
	lines := strings.Split(result, "\n")
	if len(lines) == 0 {
		t.Fatal("joinSideBySide returned empty string")
	}
	// First line: "ab" (2 chars) padded to 5 → "ab   " + "│" + "XY"
	want := "ab   │XY"
	if lines[0] != want {
		t.Errorf("line[0] = %q, want %q", lines[0], want)
	}
}

// TestJoinSideBySide_JoinsWithSeparator verifies that each combined line contains
// the │ column separator.
//
// Given: sidebar = "A\nB\n", panel = "1\n2\n", sidebarW = 3
// When:  joinSideBySide is called
// Then:  each output line contains exactly one │
func TestJoinSideBySide_JoinsWithSeparator(t *testing.T) {
	result := joinSideBySide("A\nB\n", "1\n2\n", 3)
	for i, line := range strings.Split(result, "\n") {
		count := strings.Count(line, "│")
		if count != 1 {
			t.Errorf("line[%d] = %q: contains %d '│' separators, want 1", i, line, count)
		}
	}
}

// TestJoinSideBySide_NoSpuriousTrailingLine verifies that joinSideBySide does not
// emit a spurious extra separator line when both inputs are newline-terminated.
//
// Given: sidebar = "A\n", panel = "1\n", sidebarW = 3
// When:  joinSideBySide is called
// Then:  the output contains exactly 1 line (no spurious trailing "   │" row)
func TestJoinSideBySide_NoSpuriousTrailingLine(t *testing.T) {
	result := joinSideBySide("A\n", "1\n", 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 1 {
		t.Errorf("got %d lines, want 1: %q", len(lines), lines)
	}
}

// ── OverlayActive ─────────────────────────────────────────────────────────────

// overlayActivePanel is a test-only TUIPanel stub that always reports an overlay
// as active, allowing cockpit tests to exercise overlay-gated key handling.
type overlayActivePanel struct {
	placeholderPanel
}

func (p overlayActivePanel) OverlayActive() bool { return true }

// Update overrides placeholderPanel.Update to preserve the overlayActivePanel
// type after forwarding — without this, any Update call demotes the receiver
// back to placeholderPanel, silently breaking OverlayActive().
func (p overlayActivePanel) Update(_ tea.Msg) (tea.Model, tea.Cmd) { return p, nil }

// TestOverlayActivePanel_Update_PreservesType verifies that Update returns an
// overlayActivePanel, not a placeholderPanel — preventing type demotion when
// cockpit forwards messages to a panel with an active overlay.
//
// Given: an overlayActivePanel installed as the active cockpit panel
// When:  a key message is forwarded to it via cockpit.Update
// Then:  the panel at cursor is still an overlayActivePanel
func TestOverlayActivePanel_Update_PreservesType(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	m.panelFocused = true
	m.panels[0] = overlayActivePanel{placeholderPanel{title: "Test"}} // OverlayActive() == true

	// Send a key that falls through to panel forwarding (overlay active blocks number-key intercept).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	um := updated.(cockpitModel)

	if _, ok := um.panels[0].(overlayActivePanel); !ok {
		t.Errorf("panels[0] type = %T, want overlayActivePanel (type was demoted after Update)", um.panels[0])
	}
}

// TestPlaceholderPanel_OverlayActive_ReturnsFalse verifies that the placeholder
// panel always reports no active overlay.
//
// Given: a placeholderPanel
// When:  OverlayActive() is called
// Then:  false is returned
func TestPlaceholderPanel_OverlayActive_ReturnsFalse(t *testing.T) {
	p := placeholderPanel{title: "Test"}
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false for placeholderPanel")
	}
}

// TestDropletsPanel_OverlayActive_ReturnsFalse_WhenNoOverlay verifies that
// dropletsPanel reports no overlay when tabAppModel is in its default state.
//
// Given: a freshly constructed dropletsPanel (no overlay active)
// When:  OverlayActive() is called
// Then:  false is returned
func TestDropletsPanel_OverlayActive_ReturnsFalse_WhenNoOverlay(t *testing.T) {
	p := newDropletsPanel("", "")
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false when no overlay is active")
	}
}

// TestDropletsPanel_OverlayActive_ReturnsTrue_WhenOverlayConfirm verifies that
// dropletsPanel reports an active overlay when the inner model is in overlayConfirm.
//
// Given: a dropletsPanel whose inner overlayMode is overlayConfirm
// When:  OverlayActive() is called
// Then:  true is returned
func TestDropletsPanel_OverlayActive_ReturnsTrue_WhenOverlayConfirm(t *testing.T) {
	p := newDropletsPanel("", "")
	p.inner.overlayMode = overlayConfirm
	if !p.OverlayActive() {
		t.Error("OverlayActive() = false, want true when overlayMode is overlayConfirm")
	}
}

// TestDropletsPanel_OverlayActive_ReturnsTrue_WhenOverlayText verifies that
// dropletsPanel reports an active overlay when the inner model is in overlayText.
//
// Given: a dropletsPanel whose inner overlayMode is overlayText
// When:  OverlayActive() is called
// Then:  true is returned
func TestDropletsPanel_OverlayActive_ReturnsTrue_WhenOverlayText(t *testing.T) {
	p := newDropletsPanel("", "")
	p.inner.overlayMode = overlayText
	if !p.OverlayActive() {
		t.Error("OverlayActive() = false, want true when overlayMode is overlayText")
	}
}

// ── esc return-to-sidebar ─────────────────────────────────────────────────────

// TestCockpit_Esc_ReturnsToCockpit_WhenNoOverlayActive verifies that pressing Esc
// when panelFocused=true and no panel overlay is active returns the cockpit to
// sidebar mode.
//
// Given: panelFocused=true, active panel overlay is not active
// When:  Esc is pressed
// Then:  panelFocused=false
func TestCockpit_Esc_ReturnsToCockpit_WhenNoOverlayActive(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true
	m.panels[0] = placeholderPanel{title: "Test"} // OverlayActive() == false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(cockpitModel)

	if um.panelFocused {
		t.Error("panelFocused = true, want false (esc should return to sidebar when no overlay is active)")
	}
}

// TestCockpit_Esc_ForwardedToPanel_WhenOverlayActive verifies that pressing Esc
// when panelFocused=true and an overlay is active forwards the key to the panel
// instead of returning to sidebar.
//
// Given: panelFocused=true, active panel overlay is active
// When:  Esc is pressed
// Then:  panelFocused remains true (esc was forwarded to panel, not cockpit-handled)
func TestCockpit_Esc_ForwardedToPanel_WhenOverlayActive(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true
	m.panels[0] = overlayActivePanel{placeholderPanel{title: "Test"}} // OverlayActive() == true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(cockpitModel)

	if !um.panelFocused {
		t.Error("panelFocused = false, want true (esc must not return to sidebar when overlay is active)")
	}
}

// ── sidebar hint ──────────────────────────────────────────────────────────────

// TestCockpit_View_Hint_ContainsTabToPanel_WhenSidebarFocused verifies that the
// sidebar footer hint shows "tab→panel" when in sidebar navigation mode.
//
// Given: panelFocused=false
// When:  View() is called
// Then:  output contains "tab→panel"
func TestCockpit_View_Hint_ContainsTabToPanel_WhenSidebarFocused(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = false

	if !strings.Contains(m.View(), "tab→panel") {
		t.Error("View() does not contain 'tab→panel' hint when sidebar is focused")
	}
}

// TestCockpit_View_Hint_ContainsEscToSidebar_WhenPanelFocused verifies that the
// sidebar footer hint shows "esc→sidebar" when a panel has focus.
//
// Given: panelFocused=true
// When:  View() is called
// Then:  output contains "esc→sidebar"
func TestCockpit_View_Hint_ContainsEscToSidebar_WhenPanelFocused(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true

	if !strings.Contains(m.View(), "esc→sidebar") {
		t.Error("View() does not contain 'esc→sidebar' hint when panel is focused")
	}
}

// ── sidebar up-arrow navigation ───────────────────────────────────────────────

// TestCockpit_Sidebar_UpArrow_MovesToPreviousPanel verifies that the up arrow key
// moves the cursor to the previous panel, matching the behaviour of 'k'.
//
// Given: cursor=1, panelFocused=false
// When:  up arrow is pressed
// Then:  cursor = 0
func TestCockpit_Sidebar_UpArrow_MovesToPreviousPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 1
	m.panelFocused = false

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	um := updated.(cockpitModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0", um.cursor)
	}
}

// recordingPanel is a test-only TUIPanel stub that captures the last
// tea.WindowSizeMsg width it receives, allowing tests to assert that the
// cockpit forwards the adjusted panel width rather than the raw terminal width.
type recordingPanel struct {
	placeholderPanel
	receivedWidth int
}

func (p recordingPanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		p.receivedWidth = wsm.Width
	}
	return p, nil
}

// TestCockpit_WindowSizeMsg_ForwardsPanelWidth_ToPanel verifies that a
// WindowSizeMsg is forwarded to panels with the cockpit-adjusted panel width
// (terminal width minus sidebar and separator), not the raw terminal width.
//
// Given: a cockpit with a recording stub at panels[0]
// When:  WindowSizeMsg{Width:120, Height:40} is received
// Then:  panels[0] received Width == m.panelWidth() (99), not 120
func TestCockpit_WindowSizeMsg_ForwardsPanelWidth_ToPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.panels[0] = recordingPanel{placeholderPanel: placeholderPanel{title: "Test"}}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(cockpitModel)

	want := um.panelWidth() // max(120-20-1, 20) = 99
	got := um.panels[0].(recordingPanel).receivedWidth
	if got != want {
		t.Errorf("panel received Width=%d, want %d (panelWidth, not raw terminal width)", got, want)
	}
	// Sanity: panel width must differ from terminal width to prove the test is meaningful.
	if want == 120 {
		t.Errorf("panelWidth == terminal width (%d); test would not catch a regression", want)
	}
}

// ── dropletsPanel KeyHelp ────────────────────────────────────────────────────

// TestDropletsPanel_KeyHelp_ReturnsNavigationHint verifies that dropletsPanel
// returns the expected non-empty navigation hint string.
//
// Given: a dropletsPanel
// When:  KeyHelp() is called
// Then:  the returned string contains navigation hint text
func TestDropletsPanel_KeyHelp_ReturnsNavigationHint(t *testing.T) {
	p := newDropletsPanel("", "")
	got := p.KeyHelp()
	if got == "" {
		t.Error("KeyHelp() returned empty string, want navigation hint")
	}
	want := "↑↓/jk navigate  enter/d detail  p peek"
	if got != want {
		t.Errorf("KeyHelp() = %q, want %q", got, want)
	}
}

// ── joinSideBySide unequal heights ───────────────────────────────────────────

// TestJoinSideBySide_UnequalHeights_PadsShorterSide verifies that when the
// sidebar has fewer lines than the panel, the extra panel lines are still
// rendered with the sidebar column padded to sidebarW spaces.
//
// Given: sidebar = "X" (1 line), panel = "1\n2\n3" (3 lines), sidebarW = 3
// When:  joinSideBySide is called
// Then:  line[0] = "X  │1", line[1] = "   │2", line[2] = "   │3"
func TestJoinSideBySide_UnequalHeights_PadsShorterSide(t *testing.T) {
	result := joinSideBySide("X", "1\n2\n3", 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3; result=%q", len(lines), result)
	}
	want := []string{"X  │1", "   │2", "   │3"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

// TestJoinSideBySide_UnequalHeights_PadsLongerSidebar verifies that when the
// sidebar has more lines than the panel, the extra sidebar lines are still
// rendered with the panel column empty.
//
// Given: sidebar = "A\nB\nC" (3 lines), panel = "1" (1 line), sidebarW = 3
// When:  joinSideBySide is called
// Then:  line[0] = "A  │1", line[1] = "B  │", line[2] = "C  │"
func TestJoinSideBySide_UnequalHeights_PadsLongerSidebar(t *testing.T) {
	result := joinSideBySide("A\nB\nC", "1", 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3; result=%q", len(lines), result)
	}
	want := []string{"A  │1", "B  │", "C  │"}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line[%d] = %q, want %q", i, lines[i], w)
		}
	}
}

// ── uppercase Q quit ──────────────────────────────────────────────────────────

// TestCockpit_UppercaseQ_Quits verifies that pressing 'Q' returns tea.Quit in
// sidebar mode, matching the documented 'q / Q → quit' behaviour.
//
// Given: panelFocused=false
// When:  'Q' is pressed
// Then:  returned command is tea.Quit
func TestCockpit_UppercaseQ_Quits(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = false

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})

	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("cmd() = %v, want tea.Quit()", msg)
	}
}

// ── panelWidth floor ──────────────────────────────────────────────────────────

// TestCockpit_PanelWidth_Floor_ClampedToMinimum verifies that panelWidth() never
// returns less than 20 even when the terminal is too narrow to fit the sidebar.
//
// Given: cockpit width = 5 (narrower than the sidebar alone)
// When:  panelWidth() is called
// Then:  20 is returned (the clamped minimum)
func TestCockpit_PanelWidth_Floor_ClampedToMinimum(t *testing.T) {
	m := newCockpitModel("", "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 5, Height: 40})
	um := updated.(cockpitModel)

	got := um.panelWidth()
	if got != 20 {
		t.Errorf("panelWidth() = %d, want 20 (floor) for terminal width 5", got)
	}
}
