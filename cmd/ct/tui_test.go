package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// ── Droplets tab cursor navigation ──────────────────────────────────────────

// TestTabApp_Droplets_CursorDown_MovesToNextItem verifies that pressing 'j'
// moves the cursor from the first to the second item.
//
// Given: a model with two cistern items and cursor=0
// When:  'j' is pressed
// Then:  cursor becomes 1
func TestTabApp_Droplets_CursorDown_MovesToNextItem(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Title: "First item", Status: "open"},
			{ID: "ci-bbb", Title: "Second item", Status: "open"},
		},
	}
	m.cursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(tabAppModel)

	if um.cursor != 1 {
		t.Errorf("cursor = %d, want 1", um.cursor)
	}
}

// TestTabApp_Droplets_CursorDown_AtLastItem_Stays verifies that pressing 'j'
// at the last item does not advance the cursor past the end.
//
// Given: a model with two items and cursor=1 (last item)
// When:  'j' is pressed
// Then:  cursor stays at 1
func TestTabApp_Droplets_CursorDown_AtLastItem_Stays(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Status: "open"},
			{ID: "ci-bbb", Status: "open"},
		},
	}
	m.cursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(tabAppModel)

	if um.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (should not advance past last item)", um.cursor)
	}
}

// TestTabApp_Droplets_CursorUp_MovesToPreviousItem verifies that pressing 'k'
// moves the cursor from the second to the first item.
//
// Given: a model with two items and cursor=1
// When:  'k' is pressed
// Then:  cursor becomes 0
func TestTabApp_Droplets_CursorUp_MovesToPreviousItem(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Status: "open"},
			{ID: "ci-bbb", Status: "open"},
		},
	}
	m.cursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(tabAppModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0", um.cursor)
	}
}

// TestTabApp_Droplets_CursorUp_AtZero_Stays verifies that pressing 'k' at
// the first item does not move the cursor to a negative index.
//
// Given: a model with cursor=0
// When:  'k' is pressed
// Then:  cursor stays at 0
func TestTabApp_Droplets_CursorUp_AtZero_Stays(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Status: "open"},
		},
	}
	m.cursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(tabAppModel)

	if um.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (should not go below 0)", um.cursor)
	}
}

// ── Droplets → Detail navigation ────────────────────────────────────────────

// TestTabApp_Droplets_Enter_SwitchesToDetailTab verifies that pressing Enter
// on a selected item switches to the Detail tab, sets selectedID, and returns
// a fetch command for the detail notes.
//
// Given: a model with one cistern item and cursor=0
// When:  enter is pressed
// Then:  tab becomes tabDetail, selectedID is set, a cmd is returned
func TestTabApp_Droplets_Enter_SwitchesToDetailTab(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Title: "Some task", Status: "open"},
		},
	}
	m.cursor = 0
	m.tab = tabDroplets

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.tab != tabDetail {
		t.Errorf("tab = %d, want tabDetail (%d)", um.tab, tabDetail)
	}
	if um.selectedID != "ci-aaa" {
		t.Errorf("selectedID = %q, want %q", um.selectedID, "ci-aaa")
	}
	if cmd == nil {
		t.Error("expected a fetch cmd, got nil")
	}
}

// TestTabApp_Droplets_Enter_EmptyList_NoOp verifies that pressing Enter with
// an empty item list is a no-op: the tab stays on Droplets and no cmd is issued.
//
// Given: a model with no cistern items
// When:  enter is pressed
// Then:  tab remains tabDroplets and cmd is nil
func TestTabApp_Droplets_Enter_EmptyList_NoOp(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{CisternItems: nil}
	m.tab = tabDroplets

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.tab != tabDroplets {
		t.Errorf("tab = %d, want tabDroplets (%d) for empty list", um.tab, tabDroplets)
	}
	if cmd != nil {
		t.Error("expected nil cmd for empty list, got non-nil")
	}
}

// TestTabApp_Droplets_D_Key_AlsoOpensDetail verifies that pressing 'd' is an
// alias for enter and also opens the detail tab.
//
// Given: a model with one item
// When:  'd' is pressed
// Then:  tab becomes tabDetail
func TestTabApp_Droplets_D_Key_AlsoOpensDetail(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Status: "open"},
		},
	}
	m.cursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	um := updated.(tabAppModel)

	if um.tab != tabDetail {
		t.Errorf("tab = %d, want tabDetail (%d) on 'd' key", um.tab, tabDetail)
	}
}

// ── Detail tab navigation ────────────────────────────────────────────────────

// TestTabApp_Detail_Escape_ReturnsToDropletsAndClearsSelection verifies that
// pressing Escape in the Detail tab returns to the Droplets tab and clears
// the selectedID field.
//
// Given: a model in the Detail tab with a selectedID
// When:  esc is pressed
// Then:  tab becomes tabDroplets and selectedID is empty
func TestTabApp_Detail_Escape_ReturnsToDropletsAndClearsSelection(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Title: "Some task"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(tabAppModel)

	if um.tab != tabDroplets {
		t.Errorf("tab = %d, want tabDroplets (%d) after esc", um.tab, tabDroplets)
	}
	if um.selectedID != "" {
		t.Errorf("selectedID = %q, want empty after esc", um.selectedID)
	}
}

// TestTabApp_Detail_ScrollDown_IncreasesScrollY verifies that pressing 'j'
// in the Detail tab increments the scroll offset when content exceeds the viewport.
//
// Given: a model in the Detail tab with detailScrollY=0 and height=4 (content > viewport)
// When:  'j' is pressed
// Then:  detailScrollY becomes 1
func TestTabApp_Detail_ScrollDown_IncreasesScrollY(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Status: "open"}
	m.detailScrollY = 0
	// height=4: viewH=3, content=5 lines (header+status+sep+NOTES+no-notes-yet),
	// maxScroll=2 — so 'j' should increment to 1.
	m.height = 4

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(tabAppModel)

	if um.detailScrollY != 1 {
		t.Errorf("detailScrollY = %d, want 1", um.detailScrollY)
	}
}

// TestTabApp_Detail_ScrollUp_AtZero_StaysAtZero verifies that pressing 'k'
// when already at the top does not produce a negative scroll offset.
//
// Given: a model in the Detail tab with detailScrollY=0
// When:  'k' is pressed
// Then:  detailScrollY remains 0
func TestTabApp_Detail_ScrollUp_AtZero_StaysAtZero(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa"}
	m.detailScrollY = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(tabAppModel)

	if um.detailScrollY != 0 {
		t.Errorf("detailScrollY = %d, want 0 (should not go below 0)", um.detailScrollY)
	}
}

// TestTabApp_Detail_HomeKey_ResetsScrollY verifies that pressing 'g' jumps
// the detail panel back to the top.
//
// Given: a model in the Detail tab with detailScrollY=10
// When:  'g' is pressed
// Then:  detailScrollY becomes 0
func TestTabApp_Detail_HomeKey_ResetsScrollY(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa"}
	m.detailScrollY = 10

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	um := updated.(tabAppModel)

	if um.detailScrollY != 0 {
		t.Errorf("detailScrollY = %d, want 0 after 'g'", um.detailScrollY)
	}
}

// ── Detail data message ──────────────────────────────────────────────────────

// TestTabApp_Detail_NotesFetched_StoredInChronologicalOrder verifies that
// when tuiDetailDataMsg arrives with notes newest-first (as returned by the DB),
// the model stores them oldest-first so the timeline reads chronologically.
//
// Given: a model in Detail tab with selectedID="ci-aaa"
// When:  tuiDetailDataMsg arrives with 2 notes, newest first
// Then:  detailNotes[0] is the older note, detailNotes[1] is the newer note
func TestTabApp_Detail_NotesFetched_StoredInChronologicalOrder(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa"}

	older := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	// DB returns newest first.
	notes := []cistern.CataractaeNote{
		{ID: 2, DropletID: "ci-aaa", CataractaeName: "reviewer", Content: "LGTM", CreatedAt: newer},
		{ID: 1, DropletID: "ci-aaa", CataractaeName: "implementer", Content: "Done", CreatedAt: older},
	}

	updated, _ := m.Update(tuiDetailDataMsg{dropletID: "ci-aaa", notes: notes})
	um := updated.(tabAppModel)

	if len(um.detailNotes) != 2 {
		t.Fatalf("detailNotes length = %d, want 2", len(um.detailNotes))
	}
	if um.detailNotes[0].CataractaeName != "implementer" {
		t.Errorf("detailNotes[0].CataractaeName = %q, want %q (oldest first)", um.detailNotes[0].CataractaeName, "implementer")
	}
	if um.detailNotes[1].CataractaeName != "reviewer" {
		t.Errorf("detailNotes[1].CataractaeName = %q, want %q (newest last)", um.detailNotes[1].CataractaeName, "reviewer")
	}
}

// TestTabApp_Detail_NotesFetched_StaleDropletID_Ignored verifies that notes
// fetched for a different droplet ID are discarded.
//
// Given: a model in Detail tab with selectedID="ci-aaa"
// When:  tuiDetailDataMsg arrives for "ci-bbb"
// Then:  detailNotes remains nil (stale response discarded)
func TestTabApp_Detail_NotesFetched_StaleDropletID_Ignored(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa"}
	m.detailNotes = nil

	notes := []cistern.CataractaeNote{
		{ID: 1, DropletID: "ci-bbb", CataractaeName: "implementer", Content: "Done"},
	}

	updated, _ := m.Update(tuiDetailDataMsg{dropletID: "ci-bbb", notes: notes})
	um := updated.(tabAppModel)

	if um.detailNotes != nil {
		t.Errorf("detailNotes should be nil for stale droplet ID, got %v", um.detailNotes)
	}
}

// ── View rendering ───────────────────────────────────────────────────────────

// TestTabApp_Detail_View_ShowsTitleAndID verifies that the Detail panel
// renders the droplet ID and title in its header.
//
// Given: a model in Detail tab with a droplet loaded
// When:  View() is called
// Then:  the output contains the droplet ID and title
func TestTabApp_Detail_View_ShowsTitleAndID(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{
		ID:                "ci-abc12",
		Title:             "Add retry logic to export pipeline",
		Repo:              "myrepo",
		Status:            "in_progress",
		CurrentCataractae: "implement",
	}
	m.width = 120
	m.height = 30

	view := m.View()

	if !strings.Contains(view, "ci-abc12") {
		t.Errorf("view should contain droplet ID 'ci-abc12', got:\n%s", view)
	}
	if !strings.Contains(view, "Add retry logic to export pipeline") {
		t.Errorf("view should contain title, got:\n%s", view)
	}
}

// TestTabApp_Detail_View_ShowsPipelineSteps verifies that the Detail panel
// renders all pipeline steps in the step position indicator.
//
// Given: a model in Detail tab with detailSteps set
// When:  View() is called
// Then:  each step name appears in the output
func TestTabApp_Detail_View_ShowsPipelineSteps(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{
		ID:                "ci-abc12",
		Title:             "Test task",
		Status:            "in_progress",
		CurrentCataractae: "review",
	}
	m.detailSteps = []string{"implement", "review", "test"}
	m.width = 120
	m.height = 30

	view := m.View()

	for _, step := range []string{"implement", "review", "test"} {
		if !strings.Contains(view, step) {
			t.Errorf("view should contain pipeline step %q, got:\n%s", step, view)
		}
	}
}

// TestTabApp_Detail_View_ShowsNotesWithAuthors verifies that the Detail panel
// renders note content and author names from the detailNotes timeline.
//
// Given: a model in Detail tab with two notes loaded
// When:  View() is called
// Then:  note content and author names are present in the output
func TestTabApp_Detail_View_ShowsNotesWithAuthors(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{
		ID:     "ci-abc12",
		Title:  "Test task",
		Status: "in_progress",
	}
	m.detailNotes = []cistern.CataractaeNote{
		{
			ID:             1,
			CataractaeName: "implementer",
			Content:        "Initial implementation done",
			CreatedAt:      time.Now().Add(-2 * time.Hour),
		},
		{
			ID:             2,
			CataractaeName: "reviewer",
			Content:        "LGTM with minor comments",
			CreatedAt:      time.Now().Add(-1 * time.Hour),
		},
	}
	m.width = 120
	m.height = 30

	view := m.View()

	for _, want := range []string{"implementer", "Initial implementation done", "reviewer", "LGTM with minor comments"} {
		if !strings.Contains(view, want) {
			t.Errorf("view should contain %q, got:\n%s", want, view)
		}
	}
}

// TestTabApp_Detail_View_ShowsEscHint verifies that the Detail panel's footer
// includes an "esc" keybinding hint to navigate back.
//
// Given: a model in Detail tab with a droplet loaded
// When:  View() is called
// Then:  "esc" appears in the output
func TestTabApp_Detail_View_ShowsEscHint(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{ID: "ci-abc12", Title: "Test"}
	m.width = 120
	m.height = 30

	view := m.View()

	if !strings.Contains(view, "esc") {
		t.Errorf("view should contain 'esc' keybinding hint, got:\n%s", view)
	}
}

// TestTabApp_Detail_View_ShowsRepoAndStatus verifies that the Detail panel
// header row contains the repo name and current status.
//
// Given: a model in Detail tab with repo="myrepo" and status="in_progress"
// When:  View() is called
// Then:  "myrepo" and "in_progress" are present in the output
func TestTabApp_Detail_View_ShowsRepoAndStatus(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{
		ID:     "ci-abc12",
		Title:  "Test task",
		Repo:   "myrepo",
		Status: "in_progress",
	}
	m.width = 120
	m.height = 30

	view := m.View()

	if !strings.Contains(view, "myrepo") {
		t.Errorf("view should contain repo 'myrepo', got:\n%s", view)
	}
	if !strings.Contains(view, "in_progress") {
		t.Errorf("view should contain status 'in_progress', got:\n%s", view)
	}
}

// TestTabApp_Droplets_View_ShowsItemIDs verifies that the Droplets tab lists
// all cistern item IDs.
//
// Given: a model in Droplets tab with two items
// When:  View() is called
// Then:  both item IDs appear in the output
func TestTabApp_Droplets_View_ShowsItemIDs(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-abc12", Title: "First droplet", Status: "open"},
			{ID: "ci-def34", Title: "Second droplet", Status: "in_progress", CurrentCataractae: "implement"},
		},
	}
	m.tab = tabDroplets
	m.cursor = 0
	m.width = 120
	m.height = 30

	view := m.View()

	for _, id := range []string{"ci-abc12", "ci-def34"} {
		if !strings.Contains(view, id) {
			t.Errorf("view should contain item ID %q, got:\n%s", id, view)
		}
	}
}

// ── Window resize ────────────────────────────────────────────────────────────

// TestTabApp_WindowResize_UpdatesDimensions verifies that a WindowSizeMsg
// updates both width and height on the model.
//
// Given: a model with default dimensions
// When:  a WindowSizeMsg{Width:140, Height:40} arrives
// Then:  m.width=140 and m.height=40
func TestTabApp_WindowResize_UpdatesDimensions(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	um := updated.(tabAppModel)

	if um.width != 140 {
		t.Errorf("width = %d, want 140", um.width)
	}
	if um.height != 40 {
		t.Errorf("height = %d, want 40", um.height)
	}
}

// TestTabApp_Detail_WindowResize_UpdatesDimensions verifies that window resize
// works correctly when the Detail tab is active.
//
// Given: a model in Detail tab
// When:  a WindowSizeMsg arrives
// Then:  dimensions are updated
func TestTabApp_Detail_WindowResize_UpdatesDimensions(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa"}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	um := updated.(tabAppModel)

	if um.width != 160 {
		t.Errorf("width = %d, want 160", um.width)
	}
	if um.height != 50 {
		t.Errorf("height = %d, want 50", um.height)
	}
}

// ── Issue 1: detailScrollY clamping ─────────────────────────────────────────

// helper: build a model with N single-line notes so content overflows the viewport.
// height=10, viewH=9. Content (no steps): 1+1+1+1 + (2N-1) = 4+2N-1 = 2N+3 lines.
// With N=5: 13 lines, maxScroll=4.
func detailModelWithNotes(n int) tabAppModel {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Title: "T", Status: "open"}
	m.height = 10
	m.width = 80
	notes := make([]cistern.CataractaeNote, n)
	for i := range notes {
		notes[i] = cistern.CataractaeNote{
			ID:             i + 1,
			CataractaeName: fmt.Sprintf("author%d", i),
			Content:        fmt.Sprintf("note%d", i),
		}
	}
	m.detailNotes = notes
	return m
}

// TestTabApp_Detail_ScrollDown_ClampsAtMaxScroll verifies that pressing 'j'
// many times does not push detailScrollY past the maximum scrollable offset.
//
// Given: detail tab with 5 notes, height=10 (maxScroll=4)
// When:  'j' is pressed 20 times
// Then:  detailScrollY <= 4
func TestTabApp_Detail_ScrollDown_ClampsAtMaxScroll(t *testing.T) {
	m := detailModelWithNotes(5)
	// maxScroll = 2*5+3 - 9 = 13-9 = 4

	for i := 0; i < 20; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = updated.(tabAppModel)
	}

	const wantMax = 4
	if m.detailScrollY > wantMax {
		t.Errorf("detailScrollY = %d, want <= %d (should be clamped at maxScroll)", m.detailScrollY, wantMax)
	}
}

// TestTabApp_Detail_EndKey_ClampsToMaxScroll verifies that pressing 'G' sets
// detailScrollY to the maximum scrollable offset, not an arbitrary large value.
//
// Given: detail tab with 5 notes, height=10 (maxScroll=4)
// When:  'G' is pressed
// Then:  detailScrollY == 4
func TestTabApp_Detail_EndKey_ClampsToMaxScroll(t *testing.T) {
	m := detailModelWithNotes(5)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	um := updated.(tabAppModel)

	const wantMax = 4
	if um.detailScrollY != wantMax {
		t.Errorf("detailScrollY = %d after 'G', want %d (maxScroll)", um.detailScrollY, wantMax)
	}
}

// TestTabApp_Detail_ScrollUp_AfterEndKey_Decrements verifies that pressing 'k'
// after pressing 'G' actually decrements the scroll position. This was broken
// when 'G' set detailScrollY=999999 — the user would need thousands of 'k'
// presses before movement became visible.
//
// Given: detail tab at maximum scroll (after G)
// When:  'k' is pressed
// Then:  detailScrollY decreases by 1
func TestTabApp_Detail_ScrollUp_AfterEndKey_Decrements(t *testing.T) {
	m := detailModelWithNotes(5)

	// Go to bottom.
	raw, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = raw.(tabAppModel)
	atBottom := m.detailScrollY

	// Press k once.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := updated.(tabAppModel)

	if um.detailScrollY != atBottom-1 {
		t.Errorf("detailScrollY = %d after k from bottom (%d), want %d", um.detailScrollY, atBottom, atBottom-1)
	}
}

// TestTabApp_Detail_PgDown_ClampsAtMaxScroll verifies that pgdown does not
// push detailScrollY past the maximum scrollable offset.
//
// Given: detail tab with 5 notes, height=10 (maxScroll=4)
// When:  pgdown is pressed
// Then:  detailScrollY <= 4
func TestTabApp_Detail_PgDown_ClampsAtMaxScroll(t *testing.T) {
	m := detailModelWithNotes(5)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	um := updated.(tabAppModel)

	const wantMax = 4
	if um.detailScrollY > wantMax {
		t.Errorf("detailScrollY = %d after pgdown, want <= %d", um.detailScrollY, wantMax)
	}
}

// ── Issue 2: Droplets list scroll offset ─────────────────────────────────────

// TestTabApp_Droplets_ScrollTop_FollowsCursorDown verifies that dropletsScrollTop
// adjusts when the cursor moves below the visible viewport.
//
// Given: 10 items, height=7 (viewH=6, header+sep consume 2 lines → 4 item rows)
// When:  cursor moves down to item 4 (content line 6, just past viewH=6)
// Then:  dropletsScrollTop > 0 (viewport scrolled to follow cursor)
func TestTabApp_Droplets_ScrollTop_FollowsCursorDown(t *testing.T) {
	m := newTabAppModel("", "")
	items := make([]*cistern.Droplet, 10)
	for i := range items {
		items[i] = &cistern.Droplet{ID: fmt.Sprintf("ci-%04d", i), Status: "open"}
	}
	m.data = &DashboardData{CisternItems: items}
	m.tab = tabDroplets
	m.cursor = 0
	m.height = 7 // viewH=6; item cursorLine=cursor+2; item4→cursorLine6≥0+6 → scroll
	m.width = 80

	// Move cursor to item 4 (one past visible area).
	for i := 0; i < 4; i++ {
		raw, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = raw.(tabAppModel)
	}

	if m.cursor != 4 {
		t.Fatalf("cursor = %d, want 4", m.cursor)
	}
	if m.dropletsScrollTop <= 0 {
		t.Errorf("dropletsScrollTop = %d, want > 0 after cursor escaped viewport", m.dropletsScrollTop)
	}
}

// TestTabApp_Droplets_ScrollTop_FollowsCursorUp verifies that dropletsScrollTop
// decreases when the cursor moves above the visible viewport.
//
// Given: dropletsScrollTop=5, cursor=3 (above scrollTop after moving up)
// When:  'k' is pressed so cursor moves to 2 (cursorLine=4, below scrollTop=5)
// Then:  dropletsScrollTop adjusts so cursor is visible (≤ cursorLine)
func TestTabApp_Droplets_ScrollTop_FollowsCursorUp(t *testing.T) {
	m := newTabAppModel("", "")
	items := make([]*cistern.Droplet, 10)
	for i := range items {
		items[i] = &cistern.Droplet{ID: fmt.Sprintf("ci-%04d", i), Status: "open"}
	}
	m.data = &DashboardData{CisternItems: items}
	m.tab = tabDroplets
	m.cursor = 3
	m.dropletsScrollTop = 6 // cursor line 5 < scrollTop 6 → should adjust on next k
	m.height = 10
	m.width = 80

	raw, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	um := raw.(tabAppModel)

	// cursor=2, cursorLine=4. dropletsScrollTop must be ≤ 4.
	cursorLine := um.cursor + 2
	if um.dropletsScrollTop > cursorLine {
		t.Errorf("dropletsScrollTop = %d > cursorLine %d — cursor above visible viewport", um.dropletsScrollTop, cursorLine)
	}
}

// TestTabApp_Droplets_View_CursorAlwaysVisible verifies that the cursor row
// appears in the rendered output even when the list is longer than the viewport.
//
// Given: 20 items, height=8, cursor at item 15
// When:  View() is called
// Then:  the cursor's item ID appears in the output
func TestTabApp_Droplets_View_CursorAlwaysVisible(t *testing.T) {
	m := newTabAppModel("", "")
	items := make([]*cistern.Droplet, 20)
	for i := range items {
		items[i] = &cistern.Droplet{ID: fmt.Sprintf("ci-%04d", i), Status: "open"}
	}
	m.data = &DashboardData{CisternItems: items}
	m.tab = tabDroplets
	m.cursor = 15
	m.height = 8
	m.width = 80

	// Move cursor to 15 so dropletsScrollTop is set correctly.
	for i := 0; i < 15; i++ {
		raw, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = raw.(tabAppModel)
	}

	view := m.View()
	wantID := fmt.Sprintf("ci-%04d", 15)
	if !strings.Contains(view, wantID) {
		t.Errorf("view does not contain cursor item %q — cursor is off screen:\n%s", wantID, view)
	}
}

// ── Issue 6: cistern.New error propagated in fetchDetailCmd ──────────────────

// TestFetchDetailCmd_CisternNewFails_ReturnsError verifies that when cistern.New
// fails (e.g. DB path is a directory), the returned message carries the error
// rather than silently returning a zero-value message.
//
// Given: a model whose dbPath points to a directory (not a valid SQLite file)
// When:  fetchDetailCmd is invoked and the returned tea.Cmd is executed
// Then:  the resulting tuiDetailDataMsg has err != nil and the correct dropletID
func TestFetchDetailCmd_CisternNewFails_ReturnsError(t *testing.T) {
	dir := t.TempDir()

	m := newTabAppModel("", dir) // dir is not a valid SQLite file
	cmd := m.fetchDetailCmd("ci-aaa")
	msg := cmd()

	dm, ok := msg.(tuiDetailDataMsg)
	if !ok {
		t.Fatalf("expected tuiDetailDataMsg, got %T", msg)
	}
	if dm.dropletID != "ci-aaa" {
		t.Errorf("dropletID = %q, want %q", dm.dropletID, "ci-aaa")
	}
	if dm.err == nil {
		t.Error("err should be non-nil when cistern.New fails, got nil")
	}
}

// ── Issue 3: Error handling in fetchDetailCmd ─────────────────────────────────

// TestTabApp_Detail_NotesFetchError_ShowsErrorIndicator verifies that when
// the notes fetch fails, the detail view shows an error indication rather than
// silently displaying "(no notes yet)" as if there were simply no notes.
//
// Given: detail tab with selectedID="ci-aaa"
// When:  tuiDetailDataMsg arrives with err set (simulating GetNotes failure)
// Then:  the model records the error and the view contains an error indicator
func TestTabApp_Detail_NotesFetchError_ShowsErrorIndicator(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Title: "Test", Status: "open"}
	m.width = 120
	m.height = 30

	fetchErr := errors.New("database query failed")
	updated, _ := m.Update(tuiDetailDataMsg{dropletID: "ci-aaa", err: fetchErr})
	um := updated.(tabAppModel)

	if um.detailErr == nil {
		t.Fatal("detailErr should be set when tuiDetailDataMsg carries an error")
	}

	view := um.View()
	if strings.Contains(view, "(no notes yet)") {
		t.Errorf("view should not show '(no notes yet)' when an error occurred; got:\n%s", view)
	}
	// The view must contain some error indication — either "error" or the error text.
	if !strings.Contains(view, "error") && !strings.Contains(view, fetchErr.Error()) {
		t.Errorf("view should contain error indication, got:\n%s", view)
	}
}

// ── Action dispatch overlay ──────────────────────────────────────────────────

// helper for overlay tests: a model in Detail tab with a loaded droplet.
func detailModelWithDroplet() tabAppModel {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Title: "Test task", Status: "in_progress"}
	m.width = 120
	m.height = 30
	return m
}

// TestTabApp_Detail_R_Key_OpensTextOverlay_ForRestart verifies that pressing 'r'
// in the Detail tab activates the text-entry overlay for the restart action.
//
// Given: a model in Detail tab with a loaded droplet
// When:  'r' is pressed
// Then:  overlayMode=overlayText, overlayAction=actionRestart
func TestTabApp_Detail_R_Key_OpensTextOverlay_ForRestart(t *testing.T) {
	m := detailModelWithDroplet()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d)", um.overlayMode, overlayText)
	}
	if um.overlayAction != actionRestart {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionRestart)
	}
}

// TestTabApp_Detail_X_Key_OpensConfirmOverlay_ForCancel verifies that pressing 'x'
// activates the confirmation overlay for the cancel action.
//
// Given: a model in Detail tab with a loaded droplet
// When:  'x' is pressed
// Then:  overlayMode=overlayConfirm, overlayAction=actionCancel
func TestTabApp_Detail_X_Key_OpensConfirmOverlay_ForCancel(t *testing.T) {
	m := detailModelWithDroplet()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayConfirm {
		t.Errorf("overlayMode = %d, want overlayConfirm (%d)", um.overlayMode, overlayConfirm)
	}
	if um.overlayAction != actionCancel {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionCancel)
	}
}

// TestTabApp_Detail_E_Key_OpensConfirmOverlay_ForPool verifies that pressing 'e'
// activates the confirmation overlay for the pool action.
//
// Given: a model in Detail tab with a loaded droplet
// When:  'e' is pressed
// Then:  overlayMode=overlayConfirm, overlayAction=actionPool
func TestTabApp_Detail_E_Key_OpensConfirmOverlay_ForPool(t *testing.T) {
	m := detailModelWithDroplet()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayConfirm {
		t.Errorf("overlayMode = %d, want overlayConfirm (%d)", um.overlayMode, overlayConfirm)
	}
	if um.overlayAction != actionPool {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionPool)
	}
}

// TestTabApp_Detail_N_Key_OpensTextOverlay_ForAddNote verifies that pressing 'n'
// activates the text-entry overlay for the add-note action.
//
// Given: a model in Detail tab with a loaded droplet
// When:  'n' is pressed
// Then:  overlayMode=overlayText, overlayAction=actionAddNote
func TestTabApp_Detail_N_Key_OpensTextOverlay_ForAddNote(t *testing.T) {
	m := detailModelWithDroplet()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d)", um.overlayMode, overlayText)
	}
	if um.overlayAction != actionAddNote {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionAddNote)
	}
}

// TestTabApp_Detail_S_Key_OpensTextOverlay_ForSetStep verifies that pressing 's'
// activates the text-entry overlay for the set-step action.
//
// Given: a model in Detail tab with a loaded droplet
// When:  's' is pressed
// Then:  overlayMode=overlayText, overlayAction=actionSetStep
func TestTabApp_Detail_S_Key_OpensTextOverlay_ForSetStep(t *testing.T) {
	m := detailModelWithDroplet()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d)", um.overlayMode, overlayText)
	}
	if um.overlayAction != actionSetStep {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionSetStep)
	}
}

// TestTabApp_Detail_ActionKey_WithNilDroplet_IsNoOp verifies that action keys are
// ignored when no droplet is loaded (detailDroplet == nil).
//
// Given: a model in Detail tab with no loaded droplet
// When:  'x' is pressed
// Then:  overlayMode remains overlayNone
func TestTabApp_Detail_ActionKey_WithNilDroplet_IsNoOp(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.detailDroplet = nil

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) when no droplet loaded", um.overlayMode, overlayNone)
	}
}

// TestTabApp_Detail_TextOverlay_TypeChar_AppendsToInput verifies that pressing a
// printable key while the text overlay is active appends to overlayInput.
//
// Given: a model with overlayText active and overlayInput=""
// When:  'h', 'i' are pressed
// Then:  overlayInput becomes "hi"
func TestTabApp_Detail_TextOverlay_TypeChar_AppendsToInput(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionAddNote
	m.overlayInput = ""

	for _, ch := range []rune{'h', 'i'} {
		raw, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = raw.(tabAppModel)
	}

	if m.overlayInput != "hi" {
		t.Errorf("overlayInput = %q, want %q", m.overlayInput, "hi")
	}
	if m.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d) — should stay open during typing", m.overlayMode, overlayText)
	}
}

// TestTabApp_Detail_TextOverlay_Backspace_RemovesLastChar verifies that pressing
// backspace in the text overlay removes the last rune from overlayInput.
//
// Given: a model with overlayText active and overlayInput="hello"
// When:  backspace is pressed
// Then:  overlayInput becomes "hell"
func TestTabApp_Detail_TextOverlay_Backspace_RemovesLastChar(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionAddNote
	m.overlayInput = "hello"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(tabAppModel)

	if um.overlayInput != "hell" {
		t.Errorf("overlayInput = %q, want %q after backspace", um.overlayInput, "hell")
	}
}

// TestTabApp_Detail_TextOverlay_Backspace_EmptyInput_IsNoOp verifies that backspace
// on an empty overlayInput does not panic or produce a negative index.
//
// Given: a model with overlayText active and overlayInput=""
// When:  backspace is pressed
// Then:  overlayInput remains ""
func TestTabApp_Detail_TextOverlay_Backspace_EmptyInput_IsNoOp(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionAddNote
	m.overlayInput = ""

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(tabAppModel)

	if um.overlayInput != "" {
		t.Errorf("overlayInput = %q, want empty after backspace on empty input", um.overlayInput)
	}
}

// TestTabApp_Detail_TextOverlay_Esc_DismissesOverlay verifies that pressing esc
// in the text overlay closes the overlay without executing any action.
//
// Given: a model with overlayText active and overlayInput="some text"
// When:  esc is pressed
// Then:  overlayMode=overlayNone, overlayInput=""
func TestTabApp_Detail_TextOverlay_Esc_DismissesOverlay(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionAddNote
	m.overlayInput = "some text"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after esc", um.overlayMode, overlayNone)
	}
	if um.overlayInput != "" {
		t.Errorf("overlayInput = %q, want empty after esc", um.overlayInput)
	}
}

// TestTabApp_Detail_TextOverlay_Enter_WithText_ReturnsCmd verifies that pressing
// enter in the text overlay with non-empty input closes the overlay and returns a cmd.
//
// Given: a model with overlayText active and overlayInput="implement"
// When:  enter is pressed
// Then:  overlayMode=overlayNone, overlayInput="", cmd != nil
func TestTabApp_Detail_TextOverlay_Enter_WithText_ReturnsCmd(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionRestart
	m.overlayInput = "implement"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after enter", um.overlayMode, overlayNone)
	}
	if um.overlayInput != "" {
		t.Errorf("overlayInput = %q, want empty after enter", um.overlayInput)
	}
	if cmd == nil {
		t.Error("expected a non-nil cmd after enter with text, got nil")
	}
}

// TestTabApp_Detail_TextOverlay_Enter_EmptyInput_IsNoOp verifies that pressing
// enter in the text overlay with empty input does not execute an action.
//
// Given: a model with overlayText active and overlayInput=""
// When:  enter is pressed
// Then:  overlayMode remains overlayText, cmd is nil
func TestTabApp_Detail_TextOverlay_Enter_EmptyInput_IsNoOp(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionAddNote
	m.overlayInput = ""

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d) — enter with empty input should be no-op", um.overlayMode, overlayText)
	}
	if cmd != nil {
		t.Error("expected nil cmd for enter with empty input, got non-nil")
	}
}

// TestTabApp_Detail_ConfirmOverlay_Y_ClosesOverlayAndReturnsCmd verifies that
// pressing 'y' in the confirm overlay closes it and returns an action cmd.
//
// Given: a model with overlayConfirm active for cancel action
// When:  'y' is pressed
// Then:  overlayMode=overlayNone, cmd != nil
func TestTabApp_Detail_ConfirmOverlay_Y_ClosesOverlayAndReturnsCmd(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayConfirm
	m.overlayAction = actionCancel

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after 'y'", um.overlayMode, overlayNone)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd after 'y' in confirm overlay, got nil")
	}
}

// TestTabApp_Detail_ConfirmOverlay_N_DismissesOverlay verifies that pressing 'n'
// in the confirm overlay closes it without executing any action.
//
// Given: a model with overlayConfirm active for pool action
// When:  'n' is pressed
// Then:  overlayMode=overlayNone, cmd is nil
func TestTabApp_Detail_ConfirmOverlay_N_DismissesOverlay(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayConfirm
	m.overlayAction = actionPool

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after 'n'", um.overlayMode, overlayNone)
	}
	if cmd != nil {
		t.Error("expected nil cmd after 'n' dismissal, got non-nil")
	}
}

// TestTabApp_Detail_ConfirmOverlay_Esc_DismissesOverlay verifies that pressing esc
// in the confirm overlay closes it without executing any action.
//
// Given: a model with overlayConfirm active for cancel action
// When:  esc is pressed
// Then:  overlayMode=overlayNone, cmd is nil
func TestTabApp_Detail_ConfirmOverlay_Esc_DismissesOverlay(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayConfirm
	m.overlayAction = actionCancel

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after esc", um.overlayMode, overlayNone)
	}
	if cmd != nil {
		t.Error("expected nil cmd after esc dismissal, got non-nil")
	}
}

// TestTabApp_Detail_ActionResult_Success_TriggersRefetch verifies that a successful
// tuiActionResultMsg triggers both a data-fetch and a notes-fetch command.
//
// Given: a model in Detail tab with selectedID="ci-aaa"
// When:  tuiActionResultMsg{dropletID:"ci-aaa", err:nil} arrives
// Then:  overlayErr is empty and a non-nil cmd is returned
func TestTabApp_Detail_ActionResult_Success_TriggersRefetch(t *testing.T) {
	m := detailModelWithDroplet()

	updated, cmd := m.Update(tuiActionResultMsg{dropletID: "ci-aaa", err: nil})
	um := updated.(tabAppModel)

	if um.overlayErr != "" {
		t.Errorf("overlayErr = %q, want empty after successful action", um.overlayErr)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd (refetch) after successful action result, got nil")
	}
}

// TestTabApp_Detail_ActionResult_Error_StoresErrorMessage verifies that a failed
// tuiActionResultMsg stores the error text in overlayErr.
//
// Given: a model in Detail tab with selectedID="ci-aaa"
// When:  tuiActionResultMsg with err arrives
// Then:  overlayErr contains the error message
func TestTabApp_Detail_ActionResult_Error_StoresErrorMessage(t *testing.T) {
	m := detailModelWithDroplet()
	actionErr := errors.New("db write failed")

	updated, _ := m.Update(tuiActionResultMsg{dropletID: "ci-aaa", err: actionErr})
	um := updated.(tabAppModel)

	if um.overlayErr == "" {
		t.Error("overlayErr should be set when action result carries an error")
	}
	if !strings.Contains(um.overlayErr, "db write failed") {
		t.Errorf("overlayErr = %q, want it to contain the error text", um.overlayErr)
	}
}

// TestTabApp_Detail_ActionResult_StaleID_IsIgnored verifies that action results
// for a different droplet are discarded.
//
// Given: a model in Detail tab with selectedID="ci-aaa"
// When:  tuiActionResultMsg for "ci-bbb" arrives
// Then:  overlayErr stays empty and no cmd is returned
func TestTabApp_Detail_ActionResult_StaleID_IsIgnored(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayErr = ""

	updated, cmd := m.Update(tuiActionResultMsg{dropletID: "ci-bbb", err: errors.New("should be ignored")})
	um := updated.(tabAppModel)

	if um.overlayErr != "" {
		t.Errorf("overlayErr = %q, want empty — stale result should be discarded", um.overlayErr)
	}
	if cmd != nil {
		t.Error("expected nil cmd for stale action result, got non-nil")
	}
}

// TestTabApp_Droplets_ActionResult_Success_TriggersRefetch verifies that a successful
// tuiActionResultMsg is handled even when the user has navigated to the Droplets tab.
//
// Given: a model on the Droplets tab with selectedID="ci-aaa" (navigated away mid-action)
// When:  tuiActionResultMsg{dropletID:"ci-aaa", err:nil} arrives
// Then:  overlayErr is empty and a non-nil refetch cmd is returned
func TestTabApp_Droplets_ActionResult_Success_TriggersRefetch(t *testing.T) {
	m := newTabAppModel("", "")
	m.tab = tabDroplets
	m.selectedID = "ci-aaa"

	updated, cmd := m.Update(tuiActionResultMsg{dropletID: "ci-aaa", err: nil})
	um := updated.(tabAppModel)

	if um.overlayErr != "" {
		t.Errorf("overlayErr = %q, want empty after successful action", um.overlayErr)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd (refetch) after successful action result on droplets tab, got nil")
	}
}

// TestTabApp_Droplets_ActionResult_Error_StoresErrorMessage verifies that a failed
// tuiActionResultMsg is handled even when the user has navigated to the Droplets tab.
//
// Given: a model on the Droplets tab with selectedID="ci-aaa"
// When:  tuiActionResultMsg with err arrives
// Then:  overlayErr contains the error message
func TestTabApp_Droplets_ActionResult_Error_StoresErrorMessage(t *testing.T) {
	m := newTabAppModel("", "")
	m.tab = tabDroplets
	m.selectedID = "ci-aaa"
	actionErr := errors.New("db write failed")

	updated, cmd := m.Update(tuiActionResultMsg{dropletID: "ci-aaa", err: actionErr})
	um := updated.(tabAppModel)

	if um.overlayErr == "" {
		t.Error("overlayErr should be set when action result carries an error on droplets tab")
	}
	if !strings.Contains(um.overlayErr, "db write failed") {
		t.Errorf("overlayErr = %q, want it to contain the error text", um.overlayErr)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd (refetch) even after errored action on droplets tab, got nil")
	}
}

// TestTabApp_Droplets_ActionResult_StaleID_IsIgnored verifies that action results
// for a different droplet are discarded even when the user is on the Droplets tab.
//
// Given: a model on the Droplets tab with selectedID="ci-aaa"
// When:  tuiActionResultMsg for "ci-bbb" arrives
// Then:  overlayErr stays empty and no cmd is returned
func TestTabApp_Droplets_ActionResult_StaleID_IsIgnored(t *testing.T) {
	m := newTabAppModel("", "")
	m.tab = tabDroplets
	m.selectedID = "ci-aaa"

	updated, cmd := m.Update(tuiActionResultMsg{dropletID: "ci-bbb", err: errors.New("should be ignored")})
	um := updated.(tabAppModel)

	if um.overlayErr != "" {
		t.Errorf("overlayErr = %q, want empty — stale result should be discarded", um.overlayErr)
	}
	if cmd != nil {
		t.Error("expected nil cmd for stale action result on droplets tab, got non-nil")
	}
}

// TestTabApp_Detail_OverlayActive_ScrollKeyGoesToOverlay verifies that when an
// overlay is active, scroll keys ('j') are consumed by the overlay handler rather
// than scrolling the underlying detail content.
//
// Given: a model with overlayConfirm active and detailScrollY=0
// When:  'j' is pressed (would normally scroll down)
// Then:  detailScrollY remains 0 (overlay consumed the key)
func TestTabApp_Detail_OverlayActive_ScrollKeyGoesToOverlay(t *testing.T) {
	m := detailModelWithDroplet()
	m.height = 4 // small viewport so 'j' would scroll if overlay were not active
	m.overlayMode = overlayConfirm
	m.overlayAction = actionCancel
	m.detailScrollY = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(tabAppModel)

	if um.detailScrollY != 0 {
		t.Errorf("detailScrollY = %d, want 0 — overlay should intercept 'j' before scroll handler", um.detailScrollY)
	}
	// 'j' in confirm overlay is not 'y', so overlay should dismiss.
	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) — non-y key should dismiss confirm overlay", um.overlayMode, overlayNone)
	}
}

// TestTabApp_Detail_View_ConfirmOverlay_ShowsPrompt verifies that when the confirm
// overlay is active, the rendered view includes the action-specific prompt and (y/n).
//
// Given: a model with overlayConfirm active for the cancel action
// When:  View() is called
// Then:  the output contains "cancel" and "y/n"
func TestTabApp_Detail_View_ConfirmOverlay_ShowsPrompt(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayConfirm
	m.overlayAction = actionCancel

	view := m.View()

	if !strings.Contains(view, "cancel") {
		t.Errorf("view should contain 'cancel' for cancel confirm overlay, got:\n%s", view)
	}
	if !strings.Contains(view, "y/n") {
		t.Errorf("view should contain 'y/n' for confirm overlay, got:\n%s", view)
	}
}

// TestTabApp_Detail_View_TextOverlay_ShowsInputAndPrompt verifies that when the
// text overlay is active, the rendered view shows the current input text and prompt.
//
// Given: a model with overlayText active for add-note and overlayInput="hello"
// When:  View() is called
// Then:  the output contains "hello" and a note-related prompt
func TestTabApp_Detail_View_TextOverlay_ShowsInputAndPrompt(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionAddNote
	m.overlayInput = "hello"

	view := m.View()

	if !strings.Contains(view, "hello") {
		t.Errorf("view should contain typed input 'hello', got:\n%s", view)
	}
	if !strings.Contains(view, "note") {
		t.Errorf("view should contain note prompt, got:\n%s", view)
	}
}

// TestTabApp_Detail_View_ActionError_ShowsInFooter verifies that when overlayErr
// is set and no overlay is active, the error message appears in the rendered view.
//
// Given: a model in Detail tab with overlayErr="something went wrong"
// When:  View() is called
// Then:  the output contains the error text
func TestTabApp_Detail_View_ActionError_ShowsInFooter(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayNone
	m.overlayErr = "something went wrong"

	view := m.View()

	if !strings.Contains(view, "something went wrong") {
		t.Errorf("view should contain overlayErr text, got:\n%s", view)
	}
}

// TestExecActionCmd_CisternNewFails_ReturnsError verifies that when cistern.New
// fails, execActionCmd returns a tuiActionResultMsg with err != nil.
//
// Given: a model whose dbPath points to a directory (not a valid SQLite file)
// When:  execActionCmd is invoked and the returned tea.Cmd is executed
// Then:  the resulting tuiActionResultMsg has err != nil and the correct dropletID
func TestExecActionCmd_CisternNewFails_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	m := newTabAppModel("", dir) // dir is not a valid SQLite file

	cmd := m.execActionCmd("ci-aaa", actionAddNote, "test note")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.dropletID != "ci-aaa" {
		t.Errorf("dropletID = %q, want %q", am.dropletID, "ci-aaa")
	}
	if am.err == nil {
		t.Error("err should be non-nil when cistern.New fails, got nil")
	}
}

// TestTabApp_Detail_View_ActionHints_InFooter verifies that the detail footer
// includes hints for all five action keys when no overlay is active.
//
// Given: a model in Detail tab with no overlay
// When:  View() is called
// Then:  the footer contains hints for r, x, e, n, s
func TestTabApp_Detail_View_ActionHints_InFooter(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayNone
	m.overlayErr = ""

	view := m.View()

	for _, hint := range []string{"r", "x", "e", "n", "s"} {
		if !strings.Contains(view, hint) {
			t.Errorf("view footer should contain action hint %q, got:\n%s", hint, view)
		}
	}
}

// ── execActionCmd success paths ──────────────────────────────────────────────

// newTestDBWithDroplet creates a fresh cistern DB in a temp directory, seeds it
// with one open droplet, and returns the dbPath and dropletID. The client is
// closed before returning so execActionCmd can reopen the DB via cistern.New.
func newTestDBWithDroplet(t *testing.T) (dbPath, dropletID string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "test.db")
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, err := c.Add("test-repo", "test droplet", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	return dbPath, d.ID
}

// TestExecActionCmd_Cancel_SetsDropletCancelled verifies that execActionCmd with
// actionCancel sets the droplet status to "cancelled".
//
// Given: a real cistern DB with an open droplet
// When:  execActionCmd with actionCancel is executed
// Then:  tuiActionResultMsg.err is nil and droplet status is "cancelled"
func TestExecActionCmd_Cancel_SetsDropletCancelled(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionCancel, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.dropletID != id {
		t.Errorf("dropletID = %q, want %q", am.dropletID, id)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "cancelled" {
		t.Errorf("status = %q, want %q", d.Status, "cancelled")
	}
}

// TestExecActionCmd_Pool_SetsDropletPooled verifies that execActionCmd
// with actionPool sets the droplet status to "pooled".
//
// Given: a real cistern DB with an open droplet
// When:  execActionCmd with actionPool is executed
// Then:  tuiActionResultMsg.err is nil and droplet status is "pooled"
func TestExecActionCmd_Pool_SetsDropletPooled(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionPool, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.dropletID != id {
		t.Errorf("dropletID = %q, want %q", am.dropletID, id)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "pooled" {
		t.Errorf("status = %q, want %q", d.Status, "pooled")
	}
}

// TestExecActionCmd_Restart_SetsDropletOpen verifies that execActionCmd with
// actionRestart sets the droplet status to "open".
//
// Given: a real cistern DB with a droplet
// When:  execActionCmd with actionRestart and a step name is executed
// Then:  tuiActionResultMsg.err is nil and droplet status is "open"
func TestExecActionCmd_Restart_SetsDropletOpen(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionRestart, "implement")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.dropletID != id {
		t.Errorf("dropletID = %q, want %q", am.dropletID, id)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "open" {
		t.Errorf("status = %q, want %q", d.Status, "open")
	}
}

// TestExecActionCmd_AddNote_CreatesNote verifies that execActionCmd with
// actionAddNote inserts a note for the droplet.
//
// Given: a real cistern DB with an open droplet and no notes
// When:  execActionCmd with actionAddNote and "hello" is executed
// Then:  tuiActionResultMsg.err is nil and a note with content "hello" exists
func TestExecActionCmd_AddNote_CreatesNote(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionAddNote, "hello")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.dropletID != id {
		t.Errorf("dropletID = %q, want %q", am.dropletID, id)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	notes, err := c.GetNotes(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("note count = %d, want 1", len(notes))
	}
	if notes[0].Content != "hello" {
		t.Errorf("note content = %q, want %q", notes[0].Content, "hello")
	}
}

// TestExecActionCmd_SetStep_UpdatesCataractae verifies that execActionCmd with
// actionSetStep updates the droplet's current_cataractae field.
//
// Given: a real cistern DB with an open droplet
// When:  execActionCmd with actionSetStep and "review" is executed
// Then:  tuiActionResultMsg.err is nil and droplet's CurrentCataractae is "review"
func TestExecActionCmd_SetStep_UpdatesCataractae(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionSetStep, "review")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.dropletID != id {
		t.Errorf("dropletID = %q, want %q", am.dropletID, id)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, err := c.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.CurrentCataractae != "review" {
		t.Errorf("CurrentCataractae = %q, want %q", d.CurrentCataractae, "review")
	}
}

// TestTabApp_Detail_ConfirmOverlay_UppercaseY_ClosesOverlayAndReturnsCmd verifies
// that pressing 'Y' (uppercase) in the confirm overlay closes it and returns an
// action cmd, matching the lowercase 'y' behaviour at tui.go:167.
//
// Given: a model with overlayConfirm active for cancel action
// When:  'Y' is pressed
// Then:  overlayMode=overlayNone, cmd != nil
func TestTabApp_Detail_ConfirmOverlay_UppercaseY_ClosesOverlayAndReturnsCmd(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayConfirm
	m.overlayAction = actionCancel

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after 'Y'", um.overlayMode, overlayNone)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd after 'Y' in confirm overlay, got nil")
	}
}

// ── Peek tab ─────────────────────────────────────────────────────────────────

// TestTabApp_Peek_PKeyFromDetail_SwitchesToPeekTab verifies that pressing 'p'
// in the Detail tab switches to the Peek tab.
//
// Given: a model in Detail tab with a selected droplet
// When:  'p' is pressed
// Then:  tab becomes tabPeek
func TestTabApp_Peek_PKeyFromDetail_SwitchesToPeekTab(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Title: "Task"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	um := updated.(tabAppModel)

	if um.tab != tabPeek {
		t.Errorf("tab = %d, want tabPeek (%d) after 'p'", um.tab, tabPeek)
	}
}

// TestTabApp_Peek_PKeyFromDetail_FlowingDroplet_SetsSession verifies that when
// the selected droplet has a matching CataractaeInfo, the peek session name is
// set to "<repo>-<aqueduct>".
//
// Given: a model in Detail tab with a flowing droplet (CataractaeInfo present)
// When:  'p' is pressed
// Then:  peek.session equals "myrepo-virgo"
func TestTabApp_Peek_PKeyFromDetail_FlowingDroplet_SetsSession(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{Name: "virgo", RepoName: "myrepo", DropletID: "ci-aaa", Step: "implement"},
		},
	}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Status: "in_progress"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	um := updated.(tabAppModel)

	if um.peek.session != "myrepo-virgo" {
		t.Errorf("peek.session = %q, want %q", um.peek.session, "myrepo-virgo")
	}
}

// TestTabApp_Peek_PKeyFromDetail_NotFlowing_ShowsPlaceholder verifies that when
// the selected droplet is not flowing (no matching CataractaeInfo), the Peek
// tab opens with an empty session (placeholder view).
//
// Given: a model in Detail tab with a non-flowing droplet (no CataractaeInfo)
// When:  'p' is pressed
// Then:  tab becomes tabPeek and peek.session is empty
func TestTabApp_Peek_PKeyFromDetail_NotFlowing_ShowsPlaceholder(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{Cataractae: nil}
	m.tab = tabDetail
	m.selectedID = "ci-aaa"
	m.detailDroplet = &cistern.Droplet{ID: "ci-aaa", Status: "open"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	um := updated.(tabAppModel)

	if um.tab != tabPeek {
		t.Errorf("tab = %d, want tabPeek (%d)", um.tab, tabPeek)
	}
	if um.peek.session != "" {
		t.Errorf("peek.session = %q, want empty for non-flowing droplet", um.peek.session)
	}
}

// TestTabApp_Peek_EscFromPeek_ReturnsToDetailTab verifies that pressing 'esc'
// in the Peek tab returns to the Detail tab.
//
// Given: a model in Peek tab
// When:  esc is pressed
// Then:  tab becomes tabDetail
func TestTabApp_Peek_EscFromPeek_ReturnsToDetailTab(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.selectedID = "ci-aaa"
	m.peek = newPeekModel(mockCapturer{}, "s", "hdr", 0)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(tabAppModel)

	if um.tab != tabDetail {
		t.Errorf("tab = %d, want tabDetail (%d) after esc from Peek", um.tab, tabDetail)
	}
}

// TestTabApp_Peek_QKey_Quits verifies that pressing 'q' in the Peek tab
// returns a tea.Quit command.
//
// Given: a model in Peek tab
// When:  'q' is pressed
// Then:  returned cmd resolves to tea.QuitMsg
func TestTabApp_Peek_QKey_Quits(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.peek = newPeekModel(mockCapturer{}, "s", "hdr", 0)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected quit cmd, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() = %T, want tea.QuitMsg", msg)
	}
}

// TestTabApp_Peek_PeekContentMsg_UpdatesPeekContent verifies that a
// peekContentMsg arriving while on the Peek tab updates the peek content.
//
// Given: a model in Peek tab with a configured peek model
// When:  peekContentMsg("agent output") arrives
// Then:  m.peek.content is "agent output"
func TestTabApp_Peek_PeekContentMsg_UpdatesPeekContent(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.peek = newPeekModel(mockCapturer{hasSession: true}, "s", "hdr", 0)
	m.peek.height = 24

	updated, _ := m.Update(peekContentMsg("agent output"))
	um := updated.(tabAppModel)

	if um.peek.content != "agent output" {
		t.Errorf("peek.content = %q, want %q", um.peek.content, "agent output")
	}
}

// TestTabApp_Peek_WindowResize_PropagatesSizeToPeek verifies that a
// WindowSizeMsg while on the Peek tab updates the peek model's dimensions,
// reserving one row for the footer.
//
// Given: a model in Peek tab
// When:  WindowSizeMsg{Width: 120, Height: 40} arrives
// Then:  peek.width=120 and peek.height=39 (height-1 reserved for footer)
func TestTabApp_Peek_WindowResize_PropagatesSizeToPeek(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.peek = newPeekModel(mockCapturer{}, "s", "hdr", 0)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(tabAppModel)

	if um.peek.width != 120 {
		t.Errorf("peek.width = %d, want 120", um.peek.width)
	}
	if um.peek.height != 39 {
		t.Errorf("peek.height = %d, want 39 (height-1 for footer)", um.peek.height)
	}
}

// TestTabApp_Peek_PeekTickMsg_ReturnsFetchCmd verifies that a peekTickMsg
// arriving while on the Peek tab returns a non-nil fetch command.
//
// Given: a model in Peek tab with a valid peek model
// When:  peekTickMsg arrives
// Then:  a non-nil cmd is returned
func TestTabApp_Peek_PeekTickMsg_ReturnsFetchCmd(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.peek = newPeekModel(mockCapturer{hasSession: true}, "s", "hdr", 0)

	_, cmd := m.Update(peekTickMsg{})
	if cmd == nil {
		t.Error("Update(peekTickMsg) should return a non-nil fetch Cmd")
	}
}

// TestTabApp_Peek_ViewContainsHeader verifies that the Peek tab view renders
// the peek model's header string.
//
// Given: a model in Peek tab with a configured header
// When:  View() is called
// Then:  view contains the header text
func TestTabApp_Peek_ViewContainsHeader(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.peek = newPeekModel(mockCapturer{}, "s", "[ci-abc] implement — flowing 3m", 0)
	m.peek.width = 80
	m.peek.height = 23
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "[ci-abc] implement — flowing 3m") {
		t.Errorf("View() missing peek header: %q", view)
	}
}

// TestTabApp_Peek_NotFlowing_ViewShowsNoSession verifies that when the Peek tab
// has no active session (empty content), the view shows a "session not active"
// placeholder.
//
// Given: a model in Peek tab with no content (session inactive)
// When:  View() is called
// Then:  view contains "(session not active)"
func TestTabApp_Peek_NotFlowing_ViewShowsNoSession(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabPeek
	m.peek = newPeekModel(mockCapturer{hasSession: false}, "", "[ci-aaa] — not flowing", 0)
	m.peek.width = 80
	m.peek.height = 23
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "session not active") {
		t.Errorf("View() should contain 'session not active' for no-session placeholder, got: %q", view)
	}
}
