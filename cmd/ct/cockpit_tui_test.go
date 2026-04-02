package main

import (
	"strings"
	"testing"
	"time"

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

// TestCockpit_NewModel_HasNinePanels verifies the cockpit ships with the expected
// set of panels.
//
// Given: a new cockpitModel
// When:  panels are inspected
// Then:  nine panels are registered
func TestCockpit_NewModel_HasNinePanels(t *testing.T) {
	m := newCockpitModel("", "")
	if len(m.panels) != 9 {
		t.Errorf("len(panels) = %d, want 9", len(m.panels))
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

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
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

// TestDropletsPanel_PaletteActions_WithNilDroplet_ReturnsNewDropletAction verifies
// that PaletteActions(nil) returns the always-available "new droplet" action even
// when no droplet is selected.
//
// Given: a dropletsPanel
// When:  PaletteActions(nil) is called
// Then:  a non-nil slice containing "new droplet" is returned
func TestDropletsPanel_PaletteActions_WithNilDroplet_ReturnsNewDropletAction(t *testing.T) {
	p := newDropletsPanel("", "")
	got := p.PaletteActions(nil)
	if got == nil {
		t.Fatal("PaletteActions(nil) = nil, want non-nil (should include 'new droplet' action)")
	}
	found := false
	for _, a := range got {
		if a.Name == "new droplet" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(got))
		for i, a := range got {
			names[i] = a.Name
		}
		t.Errorf("PaletteActions(nil) does not include 'new droplet'; got %v", names)
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

// ── animation tick routing (navigation-away race) ────────────────────────────

// TestCockpit_AnimMsg_RoutedToInitializedFlowPanel_WhenFlowPanelNotActive
// verifies that a tuiAnimMsg is broadcast to all initialized panels, not only
// the currently active one. This prevents the navigation-away race where:
//  1. User activates panel 2 (dashboardPanel) — lazy init fires, starts tuiAnimTick chain.
//  2. User navigates back to panel 1 within animInterval (150ms).
//  3. The in-flight tuiAnimMsg arrives while cursor=0.
//  4. Without the fix it lands on dropletsPanel which drops it, permanently
//     freezing the Flow panel animation at frame=0.
//
// Given: cursor=0, panels[1] (dashboardPanel) initializedPanels[1]=true
// When:  tuiAnimMsg is received
// Then:  panels[1] (dashboardPanel) inner.frame advances (tick was routed to it)
func TestCockpit_AnimMsg_RoutedToInitializedFlowPanel_WhenFlowPanelNotActive(t *testing.T) {
	m := newCockpitModel("", "")
	// Simulate: user activated panel 2 (dashboardPanel initialized), then navigated back.
	m.initializedPanels[1] = true
	m.cursor = 0 // dropletsPanel is active

	frameBefore := m.panels[1].(dashboardPanel).inner.frame

	updated, _ := m.Update(tuiAnimMsg(time.Time{}))
	um := updated.(cockpitModel)

	frameAfter := um.panels[1].(dashboardPanel).inner.frame
	if frameAfter == frameBefore {
		t.Error("dashboardPanel.inner.frame did not advance — animation tick was not routed to initialized inactive panel")
	}
}

// ── command palette ───────────────────────────────────────────────────────────

// paletteActionPanel is a test-only TUIPanel stub that returns a fixed set of
// PaletteActions and a fixed SelectedDroplet for exercising palette behaviour.
type paletteActionPanel struct {
	placeholderPanel
	actions  []PaletteAction
	selected *cistern.Droplet
}

func (p paletteActionPanel) PaletteActions(_ *cistern.Droplet) []PaletteAction {
	return p.actions
}

func (p paletteActionPanel) SelectedDroplet() *cistern.Droplet { return p.selected }

// newPaletteTestCockpit builds a cockpitModel whose first panel returns the
// given actions and selected droplet, for use in palette tests.
func newPaletteTestCockpit(actions []PaletteAction, selected *cistern.Droplet) cockpitModel {
	m := newCockpitModel("", "")
	m.panels[0] = paletteActionPanel{
		placeholderPanel: placeholderPanel{title: "Test"},
		actions:          actions,
		selected:         selected,
	}
	return m
}

// TestCockpit_Colon_OpensPalette_WhenSidebarFocused verifies that pressing ':'
// in sidebar mode opens the command palette.
//
// Given: panelFocused=false
// When:  ':' is pressed
// Then:  paletteActive=true
func TestCockpit_Colon_OpensPalette_WhenSidebarFocused(t *testing.T) {
	m := newCockpitModel("", "")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	um := updated.(cockpitModel)
	if !um.paletteActive {
		t.Error("paletteActive = false, want true after ':' in sidebar mode")
	}
}

// TestCockpit_Colon_OpensPalette_WhenPanelFocused verifies that pressing ':'
// in panel-focused mode also opens the command palette.
//
// Given: panelFocused=true
// When:  ':' is pressed
// Then:  paletteActive=true
func TestCockpit_Colon_OpensPalette_WhenPanelFocused(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	um := updated.(cockpitModel)
	if !um.paletteActive {
		t.Error("paletteActive = false, want true after ':' in panel-focused mode")
	}
}

// TestCockpit_Colon_AppendsToFilter_WhenPaletteAlreadyOpen verifies that typing
// ':' while the palette is already open appends to the filter query rather than
// resetting the palette (regression: paletteActive check must precede ':' check).
//
// Given: paletteActive=true, paletteQuery="fo"
// When:  ':' is pressed
// Then:  paletteActive=true and paletteQuery contains ':'
func TestCockpit_Colon_AppendsToFilter_WhenPaletteAlreadyOpen(t *testing.T) {
	m := newPaletteTestCockpit([]PaletteAction{{Name: "fo:ward"}}, nil).openPalette()
	m.paletteQuery = "fo"
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	um := updated.(cockpitModel)
	if !um.paletteActive {
		t.Error("paletteActive = false, want true — ':' while palette open should filter, not close")
	}
	if um.paletteQuery != "fo:" {
		t.Errorf("paletteQuery = %q, want %q — ':' should append to filter", um.paletteQuery, "fo:")
	}
}

// TestCockpit_Colon_DoesNotOpenPalette_WhenOverlayActive verifies that ':' is
// ignored when the focused panel has an active overlay (e.g. note input).
// Regression: ':' must carry the same OverlayActive guard as 'esc'.
//
// Given: panelFocused=true, panels[0].OverlayActive()=true
// When:  ':' is pressed
// Then:  paletteActive remains false
func TestCockpit_Colon_DoesNotOpenPalette_WhenOverlayActive(t *testing.T) {
	m := newCockpitModel("", "")
	m.panelFocused = true
	m.panels[0] = overlayActivePanel{placeholderPanel{title: "Test"}}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	um := updated.(cockpitModel)
	if um.paletteActive {
		t.Error("paletteActive = true, want false — ':' should not open palette while overlay is active")
	}
}

// TestCockpit_Palette_StartsWithEmptyQuery verifies that opening the palette
// resets the filter query to empty.
//
// Given: a new cockpit
// When:  ':' is pressed
// Then:  paletteQuery = ""
func TestCockpit_Palette_StartsWithEmptyQuery(t *testing.T) {
	m := newCockpitModel("", "")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	um := updated.(cockpitModel)
	if um.paletteQuery != "" {
		t.Errorf("paletteQuery = %q, want empty string", um.paletteQuery)
	}
}

// TestCockpit_Palette_Open_LoadsActionsFromPanel verifies that opening the
// palette populates paletteFiltered with the active panel's actions.
//
// Given: a cockpit whose first panel returns 2 PaletteActions
// When:  ':' is pressed
// Then:  len(paletteFiltered) = 2
func TestCockpit_Palette_Open_LoadsActionsFromPanel(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha", Description: "first"},
		{Name: "beta", Description: "second"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	um := updated.(cockpitModel)
	if len(um.paletteFiltered) != 2 {
		t.Errorf("len(paletteFiltered) = %d, want 2", len(um.paletteFiltered))
	}
}

// TestCockpit_Palette_Esc_ClosesPalette verifies that pressing Esc when the
// palette is open closes it.
//
// Given: paletteActive=true
// When:  Esc is pressed
// Then:  paletteActive=false
func TestCockpit_Palette_Esc_ClosesPalette(t *testing.T) {
	m := newCockpitModel("", "")
	m.paletteActive = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(cockpitModel)
	if um.paletteActive {
		t.Error("paletteActive = true, want false after Esc dismisses palette")
	}
}

// TestCockpit_Palette_Down_MovesSelectionDown verifies that pressing 'j' in
// the palette advances the paletteCursor.
//
// Given: palette open with 2 actions, paletteCursor=0
// When:  'j' is pressed
// Then:  paletteCursor=1
func TestCockpit_Palette_Down_MovesSelectionDown(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha"},
		{Name: "beta"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(cockpitModel)
	if um.paletteCursor != 1 {
		t.Errorf("paletteCursor = %d, want 1", um.paletteCursor)
	}
}

// TestCockpit_Palette_Down_AtLastAction_Stays verifies that the palette cursor
// does not advance past the last action.
//
// Given: palette open with 2 actions, paletteCursor=1 (last)
// When:  'j' is pressed
// Then:  paletteCursor remains 1
func TestCockpit_Palette_Down_AtLastAction_Stays(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha"},
		{Name: "beta"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()
	m.paletteCursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(cockpitModel)
	if um.paletteCursor != 1 {
		t.Errorf("paletteCursor = %d, want 1 (should not advance past last action)", um.paletteCursor)
	}
}

// TestCockpit_Palette_Up_MovesSelectionUp verifies that pressing 'k' in
// the palette moves the cursor up.
//
// Given: palette open with 2 actions, paletteCursor=1
// When:  'k' is pressed
// Then:  paletteCursor=0
func TestCockpit_Palette_Up_MovesSelectionUp(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha"},
		{Name: "beta"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()
	m.paletteCursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(cockpitModel)
	if um.paletteCursor != 0 {
		t.Errorf("paletteCursor = %d, want 0", um.paletteCursor)
	}
}

// TestCockpit_Palette_Up_AtFirstAction_Stays verifies that the palette cursor
// does not go below 0.
//
// Given: palette open with 2 actions, paletteCursor=0
// When:  'k' is pressed
// Then:  paletteCursor remains 0
func TestCockpit_Palette_Up_AtFirstAction_Stays(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha"},
		{Name: "beta"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(cockpitModel)
	if um.paletteCursor != 0 {
		t.Errorf("paletteCursor = %d, want 0 (should not go below 0)", um.paletteCursor)
	}
}

// TestCockpit_Palette_Type_FiltersActions verifies that typing into the palette
// filters actions by case-insensitive substring match on Name.
//
// Given: palette open with actions ["cancel", "pool", "restart"]
// When:  'a' is typed
// Then:  paletteFiltered contains only actions whose names contain "a"
//
//	(cancel and restart match; pool does not)
func TestCockpit_Palette_Type_FiltersActions(t *testing.T) {
	actions := []PaletteAction{
		{Name: "cancel"},
		{Name: "pool"},
		{Name: "restart"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	um := updated.(cockpitModel)

	// "cancel" contains "a", "pool" does not, "restart" contains "a"
	if len(um.paletteFiltered) != 2 {
		t.Errorf("len(paletteFiltered) = %d, want 2 (cancel and restart match 'a')", len(um.paletteFiltered))
	}
}

// TestCockpit_Palette_Filter_ResetsCursorToZero verifies that typing a filter
// character resets the palette cursor to 0.
//
// Given: palette open with 2 actions, paletteCursor=1
// When:  a character is typed
// Then:  paletteCursor=0
func TestCockpit_Palette_Filter_ResetsCursorToZero(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha"},
		{Name: "aleph"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()
	m.paletteCursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	um := updated.(cockpitModel)
	if um.paletteCursor != 0 {
		t.Errorf("paletteCursor = %d, want 0 after typing a filter character", um.paletteCursor)
	}
}

// TestCockpit_Palette_Backspace_RemovesLastChar verifies that pressing Backspace
// removes the last character from the filter query.
//
// Given: palette open with query "ca"
// When:  Backspace is pressed
// Then:  paletteQuery = "c"
func TestCockpit_Palette_Backspace_RemovesLastChar(t *testing.T) {
	actions := []PaletteAction{{Name: "cancel"}}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()
	m.paletteQuery = "ca"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(cockpitModel)
	if um.paletteQuery != "c" {
		t.Errorf("paletteQuery = %q, want %q", um.paletteQuery, "c")
	}
}

// TestCockpit_Palette_Enter_ClosesPalette verifies that pressing Enter when
// an action is selected closes the palette and calls the action's Run function.
//
// Given: palette open with 1 action at cursor=0
// When:  Enter is pressed
// Then:  paletteActive=false and action.Run() was called
func TestCockpit_Palette_Enter_ClosesPalette(t *testing.T) {
	ran := false
	actions := []PaletteAction{
		{
			Name: "test",
			Run:  func() tea.Cmd { ran = true; return nil },
		},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(cockpitModel)
	if um.paletteActive {
		t.Error("paletteActive = true, want false after Enter executes action")
	}
	if !ran {
		t.Error("action Run() was not called")
	}
}

// TestCockpit_Palette_Enter_ReturnsCmdFromAction verifies that pressing Enter
// returns the tea.Cmd produced by the selected action's Run function.
//
// Given: palette open with 1 action whose Run() returns a non-nil cmd
// When:  Enter is pressed
// Then:  the returned cmd is the action's cmd
func TestCockpit_Palette_Enter_ReturnsCmdFromAction(t *testing.T) {
	type sentinelMsg struct{}
	actions := []PaletteAction{
		{
			Name: "test",
			Run: func() tea.Cmd {
				return func() tea.Msg { return sentinelMsg{} }
			},
		},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("cmd = nil, want action's cmd")
	}
	if _, ok := cmd().(sentinelMsg); !ok {
		t.Errorf("cmd() did not return sentinelMsg")
	}
}

// TestCockpit_Palette_Enter_EmptyFiltered_IsNoOp verifies that pressing Enter
// when no actions match the filter is a no-op.
//
// Given: palette open with empty paletteFiltered
// When:  Enter is pressed
// Then:  paletteActive remains true, cmd is nil
func TestCockpit_Palette_Enter_EmptyFiltered_IsNoOp(t *testing.T) {
	m := newCockpitModel("", "")
	m.paletteActive = true
	m.paletteFiltered = nil

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(cockpitModel)
	if !um.paletteActive {
		t.Error("paletteActive = false, want true (Enter on empty palette should be no-op)")
	}
	if cmd != nil {
		t.Error("cmd is non-nil, want nil for no-op enter")
	}
}

// TestCockpit_Palette_Enter_SetsPanelFocused verifies that executing a palette
// action activates panel focus so the panel receives subsequent input.
//
// Given: palette open, panelFocused=false
// When:  Enter is pressed on a valid action
// Then:  panelFocused=true
func TestCockpit_Palette_Enter_SetsPanelFocused(t *testing.T) {
	actions := []PaletteAction{
		{Name: "test", Run: func() tea.Cmd { return nil }},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(cockpitModel)
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after executing palette action")
	}
}

// TestCockpit_View_Palette_ContainsSearchPrompt verifies that when the palette
// is active the View includes the '>' search prompt indicator.
//
// Given: paletteActive=true
// When:  View() is called
// Then:  output contains ">"
func TestCockpit_View_Palette_ContainsSearchPrompt(t *testing.T) {
	m := newCockpitModel("", "")
	m.paletteActive = true
	m.paletteFiltered = nil
	if !strings.Contains(m.View(), ">") {
		t.Error("View() with palette active does not contain '>' search prompt")
	}
}

// TestCockpit_View_Palette_ContainsActionNames verifies that when the palette
// is active, action names appear in the rendered view.
//
// Given: paletteActive=true with actions ["alpha", "beta"]
// When:  View() is called
// Then:  output contains both action names
func TestCockpit_View_Palette_ContainsActionNames(t *testing.T) {
	actions := []PaletteAction{
		{Name: "alpha"},
		{Name: "beta"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	v := m.View()
	if !strings.Contains(v, "alpha") {
		t.Error("View() with palette does not contain action name 'alpha'")
	}
	if !strings.Contains(v, "beta") {
		t.Error("View() with palette does not contain action name 'beta'")
	}
}

// ── dropletsPanel.SelectedDroplet ────────────────────────────────────────────

// TestDropletsPanel_SelectedDroplet_NoData_ReturnsNil verifies that
// SelectedDroplet returns nil when no dashboard data is loaded.
//
// Given: a dropletsPanel with no data
// When:  SelectedDroplet() is called
// Then:  nil is returned
func TestDropletsPanel_SelectedDroplet_NoData_ReturnsNil(t *testing.T) {
	p := newDropletsPanel("", "")
	if got := p.SelectedDroplet(); got != nil {
		t.Errorf("SelectedDroplet() = %v, want nil when no data loaded", got)
	}
}

// TestDropletsPanel_SelectedDroplet_WithData_ReturnsCursorItem verifies that
// SelectedDroplet returns the droplet at the current cursor position.
//
// Given: a dropletsPanel with 2 items and cursor=1
// When:  SelectedDroplet() is called
// Then:  the second droplet (ci-bbb) is returned
func TestDropletsPanel_SelectedDroplet_WithData_ReturnsCursorItem(t *testing.T) {
	p := newDropletsPanel("", "")
	p.inner.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa"},
			{ID: "ci-bbb"},
		},
	}
	p.inner.cursor = 1

	got := p.SelectedDroplet()
	if got == nil {
		t.Fatal("SelectedDroplet() = nil, want droplet ci-bbb")
	}
	if got.ID != "ci-bbb" {
		t.Errorf("SelectedDroplet().ID = %q, want %q", got.ID, "ci-bbb")
	}
}

// TestDropletsPanel_SelectedDroplet_DetailAndPeekView_ReturnDetailDroplet verifies
// that both tabDetail and tabPeek return the open detail droplet.
//
// Given: dropletsPanel in tabDetail or tabPeek with detailDroplet set
// When:  SelectedDroplet() is called
// Then:  the detail droplet is returned
func TestDropletsPanel_SelectedDroplet_DetailAndPeekView_ReturnDetailDroplet(t *testing.T) {
	tests := []struct {
		name string
		tab  int
		id   string
	}{
		{"tabDetail", tabDetail, "ci-detail"},
		{"tabPeek", tabPeek, "ci-peek"},
	}
	for _, tt := range tests {
		p := newDropletsPanel("", "")
		p.inner.tab = tt.tab
		p.inner.detailDroplet = &cistern.Droplet{ID: tt.id}

		got := p.SelectedDroplet()
		if got == nil {
			t.Fatalf("%s: SelectedDroplet() = nil, want detail droplet", tt.name)
		}
		if got.ID != tt.id {
			t.Errorf("%s: SelectedDroplet().ID = %q, want %q", tt.name, got.ID, tt.id)
		}
	}
}

// TestPlaceholderPanel_SelectedDroplet_ReturnsNil verifies that
// placeholderPanel always returns nil from SelectedDroplet.
//
// Given: any placeholderPanel
// When:  SelectedDroplet() is called
// Then:  nil is returned
func TestPlaceholderPanel_SelectedDroplet_ReturnsNil(t *testing.T) {
	p := placeholderPanel{title: "Test"}
	if got := p.SelectedDroplet(); got != nil {
		t.Errorf("SelectedDroplet() = %v, want nil", got)
	}
}

// TestDropletsPanel_PaletteActions_AllActionsHaveRequiredFields verifies that
// all actions returned by PaletteActions have non-empty Name, Description, and
// a non-nil Run function.
//
// Given: a dropletsPanel with a non-nil droplet
// When:  PaletteActions(droplet) is called
// Then:  every action has non-empty Name, Description, and non-nil Run
func TestDropletsPanel_PaletteActions_AllActionsHaveRequiredFields(t *testing.T) {
	p := newDropletsPanel("", "")
	d := &cistern.Droplet{ID: "ci-aaa"}
	actions := p.PaletteActions(d)
	for i, a := range actions {
		if a.Name == "" {
			t.Errorf("action[%d] has empty Name", i)
		}
		if a.Description == "" {
			t.Errorf("action[%d] %q has empty Description", i, a.Name)
		}
		if a.Run == nil {
			t.Errorf("action[%d] %q has nil Run function", i, a.Name)
		}
	}
}

// TestCockpit_Palette_Enter_NilRun_DoesNotPanic verifies that pressing Enter on
// a PaletteAction whose Run field is nil does not panic and closes the palette.
//
// Given: palette open with 1 action that has Run=nil
// When:  Enter is pressed
// Then:  no panic, paletteActive=false, cmd=nil
func TestCockpit_Palette_Enter_NilRun_DoesNotPanic(t *testing.T) {
	actions := []PaletteAction{
		{Name: "noop", Description: "no run func"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(cockpitModel)
	if um.paletteActive {
		t.Error("paletteActive = true, want false after Enter on nil-Run action")
	}
	if cmd != nil {
		t.Error("cmd is non-nil, want nil for nil-Run action")
	}
}

// ── filterPaletteActions case-insensitivity (ci-gg7gp-lg2pu) ─────────────────

// TestFilterPaletteActions_CaseInsensitive verifies that filterPaletteActions
// matches action names case-insensitively in both directions.
func TestFilterPaletteActions_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name     string
		actions  []PaletteAction
		query    string
		wantName string
	}{
		{
			"uppercase query matches lowercase name",
			[]PaletteAction{{Name: "cancel"}, {Name: "pool"}},
			"CAN", "cancel",
		},
		{
			"lowercase query matches uppercase name",
			[]PaletteAction{{Name: "CANCEL"}, {Name: "POOL"}},
			"can", "CANCEL",
		},
	}
	for _, tt := range tests {
		got := filterPaletteActions(tt.actions, tt.query)
		if len(got) != 1 {
			t.Fatalf("%s: len(got) = %d, want 1", tt.name, len(got))
		}
		if got[0].Name != tt.wantName {
			t.Errorf("%s: got[0].Name = %q, want %q", tt.name, got[0].Name, tt.wantName)
		}
	}
}

// ── dropletPaletteAction Run() message content (ci-gg7gp-166ya) ──────────────

// TestDropletsPanel_PaletteActions_Run_EmitsCorrectMsg verifies that calling
// action.Run()() on each action returned by dropletsPanel.PaletteActions emits
// a tuiPaletteActionMsg with the correct dropletID and action fields.
// Non-terminal (open) droplets include outcome actions plus structural actions.
// "new droplet" always uses dropletID="" since it is not tied to a selection.
//
// Given: a dropletsPanel with open droplet "ci-xyz"
// When:  action.Run()() is called on each PaletteAction
// Then:  each msg carries the expected dropletID and action constant
func TestDropletsPanel_PaletteActions_Run_EmitsCorrectMsg(t *testing.T) {
	p := newDropletsPanel("", "")
	dropletID := "ci-xyz"
	d := &cistern.Droplet{ID: dropletID, Status: "open"}
	actions := p.PaletteActions(d)

	// Expected: name → {action constant, expected dropletID in msg}.
	// "new droplet" emits "" as dropletID (no droplet required).
	type wantMsg struct {
		action     string
		wantDropID string
	}
	want := map[string]wantMsg{
		"new droplet":      {actionCreateDroplet, ""},
		"pass":             {actionPass, dropletID},
		"recirculate":      {actionRecirculate, dropletID},
		"close":            {actionClose, dropletID},
		"cancel":           {actionCancel, dropletID},
		"pool":             {actionPool, dropletID},
		"restart":          {actionRestart, dropletID},
		"add note":         {actionAddNote, dropletID},
		"edit metadata":    {actionEditMeta, dropletID},
		"add dependency":   {actionAddDep, dropletID},
		"remove dependency": {actionRemoveDep, dropletID},
		"file issue":       {actionFileIssue, dropletID},
		"resolve issue":    {actionResolveIssue, dropletID},
		"reject issue":     {actionRejectIssue, dropletID},
	}

	if len(actions) != len(want) {
		t.Fatalf("len(actions) = %d, want %d", len(actions), len(want))
	}

	for _, a := range actions {
		w, ok := want[a.Name]
		if !ok {
			t.Errorf("unexpected action name %q", a.Name)
			continue
		}
		if a.Run == nil {
			t.Fatalf("action %q: Run = nil", a.Name)
		}
		cmd := a.Run()
		if cmd == nil {
			t.Fatalf("action %q: Run() returned nil cmd", a.Name)
		}
		msg := cmd()
		pm, ok := msg.(tuiPaletteActionMsg)
		if !ok {
			t.Fatalf("action %q: Run()() type = %T, want tuiPaletteActionMsg", a.Name, msg)
		}
		if pm.dropletID != w.wantDropID {
			t.Errorf("action %q: msg.dropletID = %q, want %q", a.Name, pm.dropletID, w.wantDropID)
		}
		if pm.action != w.action {
			t.Errorf("action %q: msg.action = %q, want %q", a.Name, pm.action, w.action)
		}
	}
}

// ── Backspace on empty palette query (ci-gg7gp-d6ki7) ────────────────────────

// TestCockpit_Palette_Backspace_EmptyQuery_IsNoOp verifies that pressing
// Backspace when paletteQuery is already empty leaves the query unchanged and
// does not corrupt paletteFiltered.
//
// Given: palette open with paletteQuery="" and paletteFiltered=["cancel"]
// When:  Backspace is pressed
// Then:  paletteQuery="" and len(paletteFiltered)=1
func TestCockpit_Palette_Backspace_EmptyQuery_IsNoOp(t *testing.T) {
	actions := []PaletteAction{{Name: "cancel"}}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(cockpitModel)
	if um.paletteQuery != "" {
		t.Errorf("paletteQuery = %q, want %q (backspace on empty query must be no-op)", um.paletteQuery, "")
	}
	if len(um.paletteFiltered) != 1 {
		t.Errorf("len(paletteFiltered) = %d, want 1 (must be unchanged)", len(um.paletteFiltered))
	}
}

// ── Palette arrow-key navigation (ci-gg7gp-q3k6g) ────────────────────────────

// TestCockpit_Palette_ArrowKeys_Navigate verifies that the up and down arrow
// keys move the palette cursor, consistent with the j/k bindings.
//
// Given: palette open with 2 actions
// When:  down arrow is pressed from cursor=0 (or up arrow from cursor=1)
// Then:  cursor advances or retreats by 1
func TestCockpit_Palette_ArrowKeys_Navigate(t *testing.T) {
	actions := []PaletteAction{{Name: "alpha"}, {Name: "beta"}}
	tests := []struct {
		name       string
		key        tea.KeyMsg
		initCursor int
		wantCursor int
	}{
		{"down arrow moves down", tea.KeyMsg{Type: tea.KeyDown}, 0, 1},
		{"up arrow moves up", tea.KeyMsg{Type: tea.KeyUp}, 1, 0},
	}
	for _, tt := range tests {
		m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()
		m.paletteCursor = tt.initCursor

		updated, _ := m.Update(tt.key)
		um := updated.(cockpitModel)
		if um.paletteCursor != tt.wantCursor {
			t.Errorf("%s: paletteCursor = %d, want %d", tt.name, um.paletteCursor, tt.wantCursor)
		}
	}
}

// ── Backspace filter re-expansion (ci-gg7gp-6jo0w) ───────────────────────────

// TestCockpit_Palette_Backspace_ReExpandsFilter verifies that pressing Backspace
// after typing a filter character re-runs filterPaletteActions and restores all
// matching actions to paletteFiltered.
//
// Given: palette open with actions=[cancel, pool], paletteQuery="c" (typed via KeyRunes)
// When:  Backspace is pressed
// Then:  paletteQuery="" and len(paletteFiltered)=2
func TestCockpit_Palette_Backspace_ReExpandsFilter(t *testing.T) {
	actions := []PaletteAction{
		{Name: "cancel"},
		{Name: "pool"},
	}
	m := newPaletteTestCockpit(actions, &cistern.Droplet{ID: "ci-aaa"}).openPalette()

	// Type 'c' — only "cancel" matches, paletteFiltered shrinks to 1.
	typed, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = typed.(cockpitModel)
	if len(m.paletteFiltered) != 1 {
		t.Fatalf("precondition: len(paletteFiltered) = %d after typing 'c', want 1", len(m.paletteFiltered))
	}

	// Backspace clears the query and must re-expand to all actions.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(cockpitModel)
	if um.paletteQuery != "" {
		t.Errorf("paletteQuery = %q, want %q", um.paletteQuery, "")
	}
	if len(um.paletteFiltered) != 2 {
		t.Errorf("len(paletteFiltered) = %d, want 2 (backspace must re-expand filter)", len(um.paletteFiltered))
	}
}

// ── viewPalette empty-filtered branch (ci-gg7gp-ltsxt) ───────────────────────

// TestCockpit_View_Palette_EmptyFiltered_ShowsNoMatchMessage verifies that when
// the palette is active and paletteFiltered is empty, the view renders the
// "(no matching actions)" message.
//
// Given: paletteActive=true and paletteFiltered=[]
// When:  View() is called
// Then:  output contains "(no matching actions)"
func TestCockpit_View_Palette_EmptyFiltered_ShowsNoMatchMessage(t *testing.T) {
	m := newCockpitModel("", "")
	m.paletteActive = true
	m.paletteFiltered = []PaletteAction{}

	if !strings.Contains(m.View(), "(no matching actions)") {
		t.Error("View() with empty paletteFiltered does not contain '(no matching actions)'")
	}
}

// ── PaletteActions: conditional new outcome/state actions ────────────────────

func paletteActionNames(actions []PaletteAction) []string {
	names := make([]string, len(actions))
	for i, a := range actions {
		names[i] = a.Name
	}
	return names
}

func containsAction(actions []PaletteAction, name string) bool {
	for _, a := range actions {
		if a.Name == name {
			return true
		}
	}
	return false
}

// TestDropletsPanel_PaletteActions verifies that PaletteActions includes and
// excludes the correct actions based on droplet status and current cataractae.
//
// Non-terminal droplets get pass/recirculate/close/cancel/pool/restart/add note,
// plus approve only when CurrentCataractae=="human". Terminal droplets get only
// reopen. Approve must not appear for terminal droplets even when
// CurrentCataractae=="human" (cancel does not clear current_cataractae).
func TestDropletsPanel_PaletteActions(t *testing.T) {
	tests := []struct {
		name        string
		droplet     *cistern.Droplet
		action      string
		wantPresent bool
	}{
		{"pass in open", &cistern.Droplet{ID: "ci-aaa", Status: "open"}, "pass", true},
		{"recirculate in in_progress", &cistern.Droplet{ID: "ci-aaa", Status: "in_progress"}, "recirculate", true},
		{"close in open", &cistern.Droplet{ID: "ci-aaa", Status: "open"}, "close", true},
		{"reopen in delivered", &cistern.Droplet{ID: "ci-aaa", Status: "delivered"}, "reopen", true},
		{"reopen absent in open", &cistern.Droplet{ID: "ci-aaa", Status: "open"}, "reopen", false},
		{"approve when human-gated", &cistern.Droplet{ID: "ci-aaa", Status: "in_progress", CurrentCataractae: "human"}, "approve", true},
		{"approve absent when not human-gated", &cistern.Droplet{ID: "ci-aaa", Status: "in_progress", CurrentCataractae: "implement"}, "approve", false},
		{"approve absent for terminal human-gated", &cistern.Droplet{ID: "ci-aaa", Status: "cancelled", CurrentCataractae: "human"}, "approve", false},
		{"pass absent in delivered", &cistern.Droplet{ID: "ci-aaa", Status: "delivered"}, "pass", false},
	}
	p := newDropletsPanel("", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := p.PaletteActions(tt.droplet)
			got := containsAction(actions, tt.action)
			if got != tt.wantPresent {
				if tt.wantPresent {
					t.Errorf("PaletteActions missing %q; got %v", tt.action, paletteActionNames(actions))
				} else {
					t.Errorf("PaletteActions should not contain %q; got %v", tt.action, paletteActionNames(actions))
				}
			}
		})
	}
}

// ── Structural action palette actions ────────────────────────────────────────

// TestDropletsPanel_PaletteActions_StructuralActions_Present verifies that the
// structural actions (edit metadata, add/remove dependency, file issue,
// resolve/reject issue) are present in the palette for a non-terminal droplet.
//
// Given: a dropletsPanel
// When:  PaletteActions is called with an open (non-terminal) droplet
// Then:  each structural action name is present in the returned slice
func TestDropletsPanel_PaletteActions_StructuralActions_Present(t *testing.T) {
	p := newDropletsPanel("", "")
	d := &cistern.Droplet{ID: "ci-aaa", Status: "open"}
	actions := p.PaletteActions(d)

	wantActions := []string{
		"edit metadata",
		"add dependency",
		"remove dependency",
		"file issue",
		"resolve issue",
		"reject issue",
		"new droplet",
	}
	for _, want := range wantActions {
		if !containsAction(actions, want) {
			t.Errorf("PaletteActions missing %q; got %v", want, paletteActionNames(actions))
		}
	}
}

// TestDropletsPanel_PaletteActions_NewDroplet_AlwaysPresentWithDroplet verifies
// that "new droplet" action is also present when a droplet IS selected.
//
// Given: a dropletsPanel
// When:  PaletteActions is called with an open droplet
// Then:  "new droplet" is present
func TestDropletsPanel_PaletteActions_NewDroplet_AlwaysPresentWithDroplet(t *testing.T) {
	p := newDropletsPanel("", "")
	d := &cistern.Droplet{ID: "ci-aaa", Status: "open"}
	actions := p.PaletteActions(d)

	if !containsAction(actions, "new droplet") {
		t.Errorf("PaletteActions missing 'new droplet'; got %v", paletteActionNames(actions))
	}
}

// TestDropletsPanel_PaletteActions_NewDroplet_PresentForTerminalDroplet verifies
// that "new droplet" is still present even for a terminal (cancelled/delivered) droplet.
//
// Given: a dropletsPanel
// When:  PaletteActions is called with a cancelled droplet
// Then:  "new droplet" is present
func TestDropletsPanel_PaletteActions_NewDroplet_PresentForTerminalDroplet(t *testing.T) {
	p := newDropletsPanel("", "")
	d := &cistern.Droplet{ID: "ci-aaa", Status: "cancelled"}
	actions := p.PaletteActions(d)

	if !containsAction(actions, "new droplet") {
		t.Errorf("PaletteActions missing 'new droplet' for terminal droplet; got %v", paletteActionNames(actions))
	}
}
