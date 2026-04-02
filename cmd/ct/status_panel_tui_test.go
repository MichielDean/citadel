package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// ── initial state ─────────────────────────────────────────────────────────────

// TestStatusPanel_NewPanel_TitleIsStatus verifies the panel title.
//
// Given: a new statusPanel
// When:  Title() is called
// Then:  "Status" is returned
func TestStatusPanel_NewPanel_TitleIsStatus(t *testing.T) {
	p := newStatusPanel("", "")
	if p.Title() != "Status" {
		t.Errorf("Title() = %q, want %q", p.Title(), "Status")
	}
}

// TestStatusPanel_NewPanel_OverlayNotActive verifies no overlay is active by default.
//
// Given: a new statusPanel
// When:  OverlayActive() is called
// Then:  false is returned
func TestStatusPanel_NewPanel_OverlayNotActive(t *testing.T) {
	p := newStatusPanel("", "")
	if p.OverlayActive() {
		t.Error("OverlayActive() = true, want false")
	}
}

// TestStatusPanel_NewPanel_PaletteActionsNil verifies no palette actions for status panel.
//
// Given: a new statusPanel
// When:  PaletteActions() is called with a non-nil droplet
// Then:  nil is returned
func TestStatusPanel_NewPanel_PaletteActionsNil(t *testing.T) {
	p := newStatusPanel("", "")
	d := &cistern.Droplet{ID: "ci-test01"}
	if actions := p.PaletteActions(d); actions != nil {
		t.Errorf("PaletteActions() = %v, want nil", actions)
	}
}

// TestStatusPanel_NewPanel_KeyHelpNonEmpty verifies a non-empty key help string.
//
// Given: a new statusPanel
// When:  KeyHelp() is called
// Then:  a non-empty string is returned
func TestStatusPanel_NewPanel_KeyHelpNonEmpty(t *testing.T) {
	p := newStatusPanel("", "")
	if p.KeyHelp() == "" {
		t.Error("KeyHelp() = empty string, want non-empty")
	}
}

// ── View with no data ─────────────────────────────────────────────────────────

// TestStatusPanel_View_NoData_ShowsLoading verifies loading state when data is nil.
//
// Given: a statusPanel with no data loaded
// When:  View() is called
// Then:  output contains "Loading"
func TestStatusPanel_View_NoData_ShowsLoading(t *testing.T) {
	p := newStatusPanel("", "")
	v := p.View()
	if !strings.Contains(v, "Loading") {
		t.Errorf("View() = %q, want it to contain %q", v, "Loading")
	}
}

// ── View with data ────────────────────────────────────────────────────────────

// TestStatusPanel_View_WithData_ShowsCounts verifies that flowing/queued/delivered
// counts appear in the view when data is loaded.
//
// Given: a statusPanel with data (2 flowing, 3 queued, 10 delivered)
// When:  View() is called
// Then:  output contains "2", "3", and "10"
func TestStatusPanel_View_WithData_ShowsCounts(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{
		FlowingCount: 2,
		QueuedCount:  3,
		DoneCount:    10,
		FetchedAt:    time.Now(),
	}
	v := p.View()
	for _, want := range []string{"2", "3", "10"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// TestStatusPanel_View_FarmRunning_ShowsWatching verifies castellarius "watching"
// is shown when the farm is running.
//
// Given: a statusPanel with FarmRunning = true
// When:  View() is called
// Then:  output contains "watching"
func TestStatusPanel_View_FarmRunning_ShowsWatching(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{FarmRunning: true, FetchedAt: time.Now()}
	v := p.View()
	if !strings.Contains(v, "watching") {
		t.Errorf("View() does not contain %q; output:\n%s", "watching", v)
	}
}

// TestStatusPanel_View_FarmStopped_ShowsStopped verifies castellarius "stopped"
// is shown when the farm is not running.
//
// Given: a statusPanel with FarmRunning = false
// When:  View() is called
// Then:  output contains "stopped"
func TestStatusPanel_View_FarmStopped_ShowsStopped(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{FarmRunning: false, FetchedAt: time.Now()}
	v := p.View()
	if !strings.Contains(v, "stopped") {
		t.Errorf("View() does not contain %q; output:\n%s", "stopped", v)
	}
}

// TestStatusPanel_View_ActiveAqueduct_ShowsDropletAndStep verifies that an active
// aqueduct's droplet ID and step are rendered.
//
// Given: a statusPanel with one active aqueduct carrying "ci-test01" at "implement"
// When:  View() is called
// Then:  output contains "ci-test01" and "implement"
func TestStatusPanel_View_ActiveAqueduct_ShowsDropletAndStep(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{
		FlowingCount: 1,
		Cataractae: []CataractaeInfo{
			{
				Name:            "virgo",
				DropletID:       "ci-test01",
				Step:            "implement",
				CataractaeIndex: 2,
				TotalCataractae: 5,
			},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	for _, want := range []string{"ci-test01", "implement"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// TestStatusPanel_View_IdleAqueduct_ShowsIdleLabel verifies that an idle aqueduct
// is rendered with an "idle" label.
//
// Given: a statusPanel with one idle aqueduct (no DropletID)
// When:  View() is called
// Then:  output contains the aqueduct name and "idle"
func TestStatusPanel_View_IdleAqueduct_ShowsIdleLabel(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{Name: "virgo", DropletID: ""},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	for _, want := range []string{"virgo", "idle"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// TestStatusPanel_View_HumanGated_ShowsApprovalNotice verifies that pooled droplets
// awaiting human approval are surfaced in the view.
//
// Given: a statusPanel with one pooled droplet at CurrentCataractae="human"
// When:  View() is called
// Then:  output contains "human approval" and the droplet ID
func TestStatusPanel_View_HumanGated_ShowsApprovalNotice(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{
		PooledItems: []*cistern.Droplet{
			{ID: "ci-human01", CurrentCataractae: "human"},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	for _, want := range []string{"human approval", "ci-human01"} {
		if !strings.Contains(v, want) {
			t.Errorf("View() does not contain %q; output:\n%s", want, v)
		}
	}
}

// TestStatusPanel_View_ProgressFraction_Shown verifies that cataractae progress
// indices are shown when available.
//
// Given: a statusPanel with an aqueduct at step index 3 of 5
// When:  View() is called
// Then:  output contains "3/5"
func TestStatusPanel_View_ProgressFraction_Shown(t *testing.T) {
	p := newStatusPanel("", "")
	p.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{
				Name:            "virgo",
				DropletID:       "ci-test01",
				Step:            "review",
				CataractaeIndex: 3,
				TotalCataractae: 5,
			},
		},
		FetchedAt: time.Now(),
	}
	v := p.View()
	if !strings.Contains(v, "3/5") {
		t.Errorf("View() does not contain %q; output:\n%s", "3/5", v)
	}
}

// ── Update: data message ──────────────────────────────────────────────────────

// TestStatusPanel_Update_DataMsg_StoresData verifies that receiving a statusDataMsg
// stores the data and schedules a refresh tick.
//
// Given: a statusPanel with no data
// When:  a statusDataMsg with FlowingCount=1 is processed
// Then:  the model's data is updated and a tick command is returned
func TestStatusPanel_Update_DataMsg_StoresData(t *testing.T) {
	p := newStatusPanel("", "")
	data := &DashboardData{FlowingCount: 1, FetchedAt: time.Now()}

	updated, cmd := p.Update(statusDataMsg(data))
	up := updated.(statusPanel)

	if up.data == nil {
		t.Fatal("data = nil after statusDataMsg, want non-nil")
	}
	if up.data.FlowingCount != 1 {
		t.Errorf("data.FlowingCount = %d, want 1", up.data.FlowingCount)
	}
	if cmd == nil {
		t.Error("cmd = nil after statusDataMsg, want a refresh tick command")
	}
}

// TestStatusPanel_Update_DataMsg_IdleMode_WhenNoChange verifies idle mode activates
// when state hash is unchanged and no droplets are flowing.
//
// Given: a statusPanel with stateHash already set and data.FlowingCount=0
// When:  a statusDataMsg with the same hash value arrives
// Then:  idleMode = true
func TestStatusPanel_Update_DataMsg_IdleMode_WhenNoChange(t *testing.T) {
	p := newStatusPanel("", "")
	data := &DashboardData{
		FlowingCount: 0,
		QueuedCount:  1,
		FetchedAt:    time.Now(),
	}
	// Prime the model with the first fetch so stateHash is set.
	updated, _ := p.Update(statusDataMsg(data))
	p = updated.(statusPanel)

	// Send the same data again — hash unchanged, no flowing → idle.
	sameData := &DashboardData{
		FlowingCount: 0,
		QueuedCount:  1,
		FetchedAt:    time.Now(),
	}
	updated2, _ := p.Update(statusDataMsg(sameData))
	p2 := updated2.(statusPanel)

	if !p2.idleMode {
		t.Error("idleMode = false, want true when state is unchanged and no droplets flowing")
	}
}

// TestStatusPanel_Update_DataMsg_NotIdleMode_WhenFlowing verifies idle mode does NOT
// activate when droplets are flowing, even if the hash is unchanged.
//
// Given: a statusPanel that received the same data twice but FlowingCount > 0
// When:  the second identical statusDataMsg is processed
// Then:  idleMode = false
func TestStatusPanel_Update_DataMsg_NotIdleMode_WhenFlowing(t *testing.T) {
	p := newStatusPanel("", "")
	data := &DashboardData{
		FlowingCount: 1,
		FetchedAt:    time.Now(),
	}
	updated, _ := p.Update(statusDataMsg(data))
	p = updated.(statusPanel)

	sameData := &DashboardData{
		FlowingCount: 1,
		FetchedAt:    time.Now(),
	}
	updated2, _ := p.Update(statusDataMsg(sameData))
	p2 := updated2.(statusPanel)

	if p2.idleMode {
		t.Error("idleMode = true, want false when droplets are still flowing")
	}
}

// ── Update: tick message ──────────────────────────────────────────────────────

// TestStatusPanel_Update_TickMsg_ReturnsFetchCmd verifies that a statusTickMsg
// triggers a data fetch command.
//
// Given: a statusPanel in any state
// When:  a statusTickMsg is processed
// Then:  a non-nil command is returned (the fetch cmd)
func TestStatusPanel_Update_TickMsg_ReturnsFetchCmd(t *testing.T) {
	p := newStatusPanel("", "")

	_, cmd := p.Update(statusTickMsg(time.Now()))
	if cmd == nil {
		t.Error("cmd = nil after statusTickMsg, want a fetch command")
	}
}

// ── Update: r key force-refresh ───────────────────────────────────────────────

// TestStatusPanel_Update_RKey_ReturnsFetchCmd verifies that pressing 'r' triggers
// an immediate data fetch.
//
// Given: a statusPanel in any state
// When:  'r' is pressed
// Then:  a non-nil command is returned
func TestStatusPanel_Update_RKey_ReturnsFetchCmd(t *testing.T) {
	p := newStatusPanel("", "")

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Error("cmd = nil after 'r' key press, want a fetch command")
	}
}

// TestStatusPanel_Update_UpperRKey_ReturnsFetchCmd verifies that 'R' also
// triggers an immediate data fetch.
//
// Given: a statusPanel in any state
// When:  'R' is pressed
// Then:  a non-nil command is returned
func TestStatusPanel_Update_UpperRKey_ReturnsFetchCmd(t *testing.T) {
	p := newStatusPanel("", "")

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if cmd == nil {
		t.Error("cmd = nil after 'R' key press, want a fetch command")
	}
}

// ── Update: scroll ────────────────────────────────────────────────────────────

// TestStatusPanel_Update_DownKey_IncrementsScrollY verifies 'j' scrolls down.
//
// Given: scrollY=0
// When:  'j' is pressed
// Then:  scrollY = 1
func TestStatusPanel_Update_DownKey_IncrementsScrollY(t *testing.T) {
	p := newStatusPanel("", "")
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	up := updated.(statusPanel)

	if up.scrollY != 1 {
		t.Errorf("scrollY = %d, want 1", up.scrollY)
	}
}

// TestStatusPanel_Update_UpKey_DecrementsScrollY verifies 'k' scrolls up.
//
// Given: scrollY=3
// When:  'k' is pressed
// Then:  scrollY = 2
func TestStatusPanel_Update_UpKey_DecrementsScrollY(t *testing.T) {
	p := newStatusPanel("", "")
	p.scrollY = 3

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(statusPanel)

	if up.scrollY != 2 {
		t.Errorf("scrollY = %d, want 2", up.scrollY)
	}
}

// TestStatusPanel_Update_UpKey_AtTop_StaysAtZero verifies 'k' at the top does not
// set a negative scrollY.
//
// Given: scrollY=0
// When:  'k' is pressed
// Then:  scrollY = 0 (no underflow)
func TestStatusPanel_Update_UpKey_AtTop_StaysAtZero(t *testing.T) {
	p := newStatusPanel("", "")
	p.scrollY = 0

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	up := updated.(statusPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 (should not underflow)", up.scrollY)
	}
}

// TestStatusPanel_Update_HomeKey_ResetsScroll verifies 'g' jumps to the top.
//
// Given: scrollY=10
// When:  'g' is pressed
// Then:  scrollY = 0
func TestStatusPanel_Update_HomeKey_ResetsScroll(t *testing.T) {
	p := newStatusPanel("", "")
	p.scrollY = 10

	updated, _ := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	up := updated.(statusPanel)

	if up.scrollY != 0 {
		t.Errorf("scrollY = %d, want 0 after 'g'", up.scrollY)
	}
}

// ── Update: window resize ─────────────────────────────────────────────────────

// TestStatusPanel_Update_WindowSizeMsg_UpdatesDimensions verifies that
// tea.WindowSizeMsg updates the panel's width and height.
//
// Given: a statusPanel with default dimensions
// When:  a WindowSizeMsg{Width: 120, Height: 40} is processed
// Then:  width=120, height=40
func TestStatusPanel_Update_WindowSizeMsg_UpdatesDimensions(t *testing.T) {
	p := newStatusPanel("", "")

	updated, _ := p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	up := updated.(statusPanel)

	if up.width != 120 {
		t.Errorf("width = %d, want 120", up.width)
	}
	if up.height != 40 {
		t.Errorf("height = %d, want 40", up.height)
	}
}

// ── cockpit integration ───────────────────────────────────────────────────────

// TestCockpit_Panel3_IsStatusPanel verifies the cockpit panel at index 2 (key: 3)
// is a statusPanel with title "Status".
//
// Given: a new cockpitModel
// When:  panels[2] title is inspected
// Then:  title = "Status"
func TestCockpit_Panel3_IsStatusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	if len(m.panels) < 3 {
		t.Fatalf("len(panels) = %d, want at least 3", len(m.panels))
	}
	if m.panels[2].Title() != "Status" {
		t.Errorf("panels[2].Title() = %q, want %q", m.panels[2].Title(), "Status")
	}
}

// TestCockpit_StatusDataMsg_RoutesToStatusPanel_WhenCursorNotAtTwo verifies that
// statusDataMsg is always delivered to panels[2] regardless of which panel is active.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a statusDataMsg with FlowingCount=7 is processed
// Then:  panels[2] (statusPanel) has data.FlowingCount=7
func TestCockpit_StatusDataMsg_RoutesToStatusPanel_WhenCursorNotAtTwo(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0
	data := &DashboardData{FlowingCount: 7, FetchedAt: time.Now()}

	updated, _ := m.Update(statusDataMsg(data))
	um := updated.(cockpitModel)

	sp, ok := um.panels[2].(statusPanel)
	if !ok {
		t.Fatalf("panels[2] is not a statusPanel")
	}
	if sp.data == nil {
		t.Fatal("statusPanel.data = nil, want data delivered to panels[2]")
	}
	if sp.data.FlowingCount != 7 {
		t.Errorf("statusPanel.data.FlowingCount = %d, want 7", sp.data.FlowingCount)
	}
}

// TestCockpit_StatusTickMsg_RoutesToStatusPanel_WhenCursorNotAtTwo verifies that
// statusTickMsg is always delivered to panels[2] regardless of which panel is active.
//
// Given: a cockpitModel with cursor=0 (Droplets panel active)
// When:  a statusTickMsg is processed
// Then:  a non-nil command is returned (the fetch triggered by the status panel)
func TestCockpit_StatusTickMsg_RoutesToStatusPanel_WhenCursorNotAtTwo(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	_, cmd := m.Update(statusTickMsg(time.Now()))
	if cmd == nil {
		t.Error("cmd = nil after statusTickMsg delivered to cockpit with cursor=0, want fetch command from statusPanel")
	}
}

// TestCockpit_Key3_ActivatesStatusPanel verifies that pressing '3' in sidebar mode
// jumps to the status panel (index 2) and activates panel focus.
//
// Given: a cockpitModel in sidebar mode, cursor=0
// When:  '3' is pressed
// Then:  cursor=2, panelFocused=true
func TestCockpit_Key3_ActivatesStatusPanel(t *testing.T) {
	m := newCockpitModel("", "")
	m.cursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	um := updated.(cockpitModel)

	if um.cursor != 2 {
		t.Errorf("cursor = %d, want 2", um.cursor)
	}
	if !um.panelFocused {
		t.Error("panelFocused = false, want true after pressing '3'")
	}
}
