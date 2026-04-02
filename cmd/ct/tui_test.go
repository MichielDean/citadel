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
// height=10, viewH=9. Content (no steps, no issues):
//   header + meta + sep + ISSUES heading + "(no issues)" + sep + NOTES heading = 7 fixed lines
//   + (2N-1) note lines  →  2N+6 total.
//   With N=5: 16 lines, maxScroll = 16-9 = 7.
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
// Given: detail tab with 5 notes, height=10 (maxScroll=7)
// When:  'j' is pressed 20 times
// Then:  detailScrollY <= 7
func TestTabApp_Detail_ScrollDown_ClampsAtMaxScroll(t *testing.T) {
	m := detailModelWithNotes(5)
	// maxScroll = 2*5+6 - 9 = 16-9 = 7

	for i := 0; i < 20; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		m = updated.(tabAppModel)
	}

	const wantMax = 7
	if m.detailScrollY > wantMax {
		t.Errorf("detailScrollY = %d, want <= %d (should be clamped at maxScroll)", m.detailScrollY, wantMax)
	}
}

// TestTabApp_Detail_EndKey_ClampsToMaxScroll verifies that pressing 'G' sets
// detailScrollY to the maximum scrollable offset, not an arbitrary large value.
//
// Given: detail tab with 5 notes, height=10 (maxScroll=7)
// When:  'G' is pressed
// Then:  detailScrollY == 7
func TestTabApp_Detail_EndKey_ClampsToMaxScroll(t *testing.T) {
	m := detailModelWithNotes(5)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	um := updated.(tabAppModel)

	const wantMax = 7
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
// Given: detail tab with 5 notes, height=10 (maxScroll=7)
// When:  pgdown is pressed
// Then:  detailScrollY <= 7
func TestTabApp_Detail_PgDown_ClampsAtMaxScroll(t *testing.T) {
	m := detailModelWithNotes(5)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	um := updated.(tabAppModel)

	const wantMax = 7
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

// TestTabApp_ActionResult_GlobalAction_EmptyDropletID_WithSelectedDroplet_TriggersRefetch
// verifies that a global action result (dropletID="") is never discarded even
// when the user has navigated to a different droplet while the action was in-flight.
//
// Given: a model with selectedID="ci-aaa"
// When:  tuiActionResultMsg{dropletID:"", err:nil} arrives (global action, e.g. create droplet)
// Then:  cmd is non-nil (refetch triggered — result was not discarded)
func TestTabApp_ActionResult_GlobalAction_EmptyDropletID_WithSelectedDroplet_TriggersRefetch(t *testing.T) {
	m := newTabAppModel("", "")
	m.selectedID = "ci-aaa"

	_, cmd := m.Update(tuiActionResultMsg{dropletID: "", err: nil})

	if cmd == nil {
		t.Error("expected non-nil cmd for global action result (empty dropletID) — result should not be discarded")
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

// ── tuiPaletteActionMsg ───────────────────────────────────────────────────────

// TestTabApp_PaletteActionMsg_Action_OpensDetailAndOverlay verifies that each
// action's tuiPaletteActionMsg switches to the Detail tab and opens the correct
// overlay: overlayConfirm for cancel/pool, overlayText for restart/add note.
//
// Given: a tabAppModel with one droplet in CisternItems
// When:  tuiPaletteActionMsg{dropletID: "ci-aaa", action: <action>} arrives
// Then:  tab=tabDetail, overlayMode=<expected>, overlayAction=<action>
func TestTabApp_PaletteActionMsg_Action_OpensDetailAndOverlay(t *testing.T) {
	tests := []struct {
		action      string
		overlayMode int
	}{
		{actionCancel, overlayConfirm},
		{actionPool, overlayConfirm},
		{actionRestart, overlayText},
		{actionAddNote, overlayText},
	}
	for _, tt := range tests {
		m := newTabAppModel("", "")
		m.data = &DashboardData{
			CisternItems: []*cistern.Droplet{
				{ID: "ci-aaa", Title: "Test", Status: "in_progress"},
			},
		}
		updated, _ := m.Update(tuiPaletteActionMsg{dropletID: "ci-aaa", action: tt.action})
		um := updated.(tabAppModel)
		if um.tab != tabDetail {
			t.Errorf("action %q: tab = %d, want tabDetail (%d)", tt.action, um.tab, tabDetail)
		}
		if um.overlayMode != tt.overlayMode {
			t.Errorf("action %q: overlayMode = %d, want %d", tt.action, um.overlayMode, tt.overlayMode)
		}
		if um.overlayAction != tt.action {
			t.Errorf("action %q: overlayAction = %q, want %q", tt.action, um.overlayAction, tt.action)
		}
	}
}

// TestTabApp_PaletteActionMsg_UnknownDroplet_IsNoOp verifies that a
// tuiPaletteActionMsg for a droplet not present in any data list is a no-op.
//
// Given: a tabAppModel whose data does not contain "ci-zzz"
// When:  tuiPaletteActionMsg{dropletID: "ci-zzz", action: actionCancel} arrives
// Then:  tab remains tabDroplets, overlayMode remains overlayNone
func TestTabApp_PaletteActionMsg_UnknownDroplet_IsNoOp(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Status: "open"},
		},
	}

	updated, _ := m.Update(tuiPaletteActionMsg{dropletID: "ci-zzz", action: actionCancel})
	um := updated.(tabAppModel)

	if um.tab != tabDroplets {
		t.Errorf("tab = %d, want tabDroplets (%d)", um.tab, tabDroplets)
	}
	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d)", um.overlayMode, overlayNone)
	}
}

// ── New outcome/state action palette msg → overlay dispatch ──────────────────

// TestTabApp_PaletteActionMsg_NewActions_OpensDetailAndOverlay verifies that
// the five new outcome/state actions open the Detail tab with the correct
// overlay mode: overlayConfirm for pass/close/reopen/approve,
// overlayText for recirculate.
//
// Given: a tabAppModel with one droplet in CisternItems
// When:  tuiPaletteActionMsg{dropletID: "ci-aaa", action: <action>} arrives
// Then:  tab=tabDetail, overlayMode=<expected>, overlayAction=<action>
func TestTabApp_PaletteActionMsg_NewActions_OpensDetailAndOverlay(t *testing.T) {
	tests := []struct {
		action      string
		overlayMode int
	}{
		{actionPass, overlayConfirm},
		{actionRecirculate, overlayText},
		{actionClose, overlayConfirm},
		{actionReopen, overlayConfirm},
		{actionApprove, overlayConfirm},
	}
	for _, tt := range tests {
		m := newTabAppModel("", "")
		m.data = &DashboardData{
			CisternItems: []*cistern.Droplet{
				{ID: "ci-aaa", Title: "Test", Status: "in_progress"},
			},
		}
		updated, _ := m.Update(tuiPaletteActionMsg{dropletID: "ci-aaa", action: tt.action})
		um := updated.(tabAppModel)
		if um.tab != tabDetail {
			t.Errorf("action %q: tab = %d, want tabDetail (%d)", tt.action, um.tab, tabDetail)
		}
		if um.overlayMode != tt.overlayMode {
			t.Errorf("action %q: overlayMode = %d, want %d", tt.action, um.overlayMode, tt.overlayMode)
		}
		if um.overlayAction != tt.action {
			t.Errorf("action %q: overlayAction = %q, want %q", tt.action, um.overlayAction, tt.action)
		}
	}
}

// ── Recirculate text overlay: empty input is valid ───────────────────────────

// TestTabApp_Detail_TextOverlay_Recirculate_EmptyInput_ExecutesCmd verifies
// that pressing enter in the recirculate text overlay with empty input still
// closes the overlay and returns an action cmd (empty = "use current step").
//
// Given: a model with overlayText active for actionRecirculate and overlayInput=""
// When:  enter is pressed
// Then:  overlayMode=overlayNone and cmd != nil
func TestTabApp_Detail_TextOverlay_Recirculate_EmptyInput_ExecutesCmd(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionRecirculate
	m.overlayInput = ""

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after enter", um.overlayMode, overlayNone)
	}
	if cmd == nil {
		t.Error("expected a non-nil cmd for recirculate with empty input, got nil")
	}
}

// ── viewDetail footer prompts for new actions ─────────────────────────────────

// TestTabApp_ViewDetail_Footer_NewActions verifies that the detail footer shows
// the correct prompt string for each of the five new outcome/state actions.
func TestTabApp_ViewDetail_Footer_NewActions(t *testing.T) {
	tests := []struct {
		action      string
		overlayMode int
		wantPrompt  string
	}{
		{actionPass, overlayConfirm, "pass this droplet?"},
		{actionClose, overlayConfirm, "close this droplet?"},
		{actionReopen, overlayConfirm, "reopen this droplet?"},
		{actionApprove, overlayConfirm, "approve this droplet?"},
		{actionRecirculate, overlayText, "recirculate to step"},
	}
	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			m := detailModelWithDroplet()
			m.overlayMode = tt.overlayMode
			m.overlayAction = tt.action
			if !strings.Contains(m.View(), tt.wantPrompt) {
				t.Errorf("viewDetail footer missing %q for action %q", tt.wantPrompt, tt.action)
			}
		})
	}
}

// ── execActionCmd: new outcome/state actions ──────────────────────────────────

// TestExecActionCmd_Pass_OnOpenDroplet_SetsDelivered verifies that execActionCmd
// with actionPass on an open (non-in_progress) droplet sets status to "delivered".
//
// Given: a real cistern DB with an open droplet
// When:  execActionCmd with actionPass is executed
// Then:  tuiActionResultMsg.err is nil and droplet status is "delivered"
func TestExecActionCmd_Pass_OnOpenDroplet_SetsDelivered(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionPass, "")
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
	if d.Status != "delivered" {
		t.Errorf("status = %q, want %q", d.Status, "delivered")
	}
}

// TestExecActionCmd_Recirculate_WithStep_SetsOpenAtStep verifies that
// execActionCmd with actionRecirculate and an explicit step sets the droplet to
// "open" at that cataractae.
//
// Given: a real cistern DB with an open droplet
// When:  execActionCmd with actionRecirculate and input "review" is executed
// Then:  tuiActionResultMsg.err is nil, status is "open", CurrentCataractae is "review"
func TestExecActionCmd_Recirculate_WithStep_SetsOpenAtStep(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionRecirculate, "review")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
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
	if d.CurrentCataractae != "review" {
		t.Errorf("CurrentCataractae = %q, want %q", d.CurrentCataractae, "review")
	}
}

// TestExecActionCmd_Recirculate_EmptyStep_UsesCurrentCataractae verifies that
// execActionCmd with actionRecirculate and empty input falls back to the droplet's
// current cataractae.
//
// Given: a droplet with CurrentCataractae="implement"
// When:  execActionCmd with actionRecirculate and input "" is executed
// Then:  tuiActionResultMsg.err is nil and CurrentCataractae remains "implement"
func TestExecActionCmd_Recirculate_EmptyStep_UsesCurrentCataractae(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Set a known cataractae so the empty-input fallback is testable.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetCataractae(id, "implement"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionRecirculate, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.CurrentCataractae != "implement" {
		t.Errorf("CurrentCataractae = %q, want %q (empty input should use current cataractae)", d.CurrentCataractae, "implement")
	}
}

// TestExecActionCmd_Close_SetsDelivered verifies that execActionCmd with
// actionClose sets the droplet status to "delivered".
//
// Given: a real cistern DB with an open droplet
// When:  execActionCmd with actionClose is executed
// Then:  tuiActionResultMsg.err is nil and droplet status is "delivered"
func TestExecActionCmd_Close_SetsDelivered(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)
	m := newTabAppModel("", dbPath)

	cmd := m.execActionCmd(id, actionClose, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
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
	if d.Status != "delivered" {
		t.Errorf("status = %q, want %q", d.Status, "delivered")
	}
}

// TestExecActionCmd_Reopen_SetsOpen verifies that execActionCmd with
// actionReopen sets the droplet status to "open".
//
// Given: a real cistern DB with a droplet closed as delivered
// When:  execActionCmd with actionReopen is executed
// Then:  tuiActionResultMsg.err is nil and droplet status is "open"
func TestExecActionCmd_Reopen_SetsOpen(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// First close it so reopen has something to act on.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CloseItem(id); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionReopen, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "open" {
		t.Errorf("status = %q, want %q", d.Status, "open")
	}
}

// TestExecActionCmd_Pass_OnInProgressDroplet_SetsOutcomeOnly verifies that
// execActionCmd with actionPass on an in_progress droplet sets outcome="pass"
// but does NOT call CloseItem — status remains in_progress.
//
// Given: a real cistern DB with an in_progress droplet
// When:  execActionCmd with actionPass is executed
// Then:  tuiActionResultMsg.err is nil, outcome is "pass", status is still "in_progress"
func TestExecActionCmd_Pass_OnInProgressDroplet_SetsOutcomeOnly(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Advance to in_progress.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(id, "in_progress"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionPass, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != "pass" {
		t.Errorf("outcome = %q, want %q", d.Outcome, "pass")
	}
	if d.Status != "in_progress" {
		t.Errorf("status = %q, want %q (in_progress pass must not close item)", d.Status, "in_progress")
	}
}

// TestExecActionCmd_Recirculate_OnInProgressDroplet_SetsOutcome verifies that
// execActionCmd with actionRecirculate on an in_progress droplet sets
// outcome="recirculate" (via SetOutcome) without calling Assign.
//
// Given: a real cistern DB with an in_progress droplet
// When:  execActionCmd with actionRecirculate and empty input is executed
// Then:  tuiActionResultMsg.err is nil, outcome is "recirculate", status is still "in_progress"
func TestExecActionCmd_Recirculate_OnInProgressDroplet_SetsOutcome(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Advance to in_progress.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(id, "in_progress"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionRecirculate, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != "recirculate" {
		t.Errorf("outcome = %q, want %q", d.Outcome, "recirculate")
	}
	if d.Status != "in_progress" {
		t.Errorf("status = %q, want %q (in_progress recirculate must not call Assign)", d.Status, "in_progress")
	}
}

// TestExecActionCmd_Recirculate_WithStep_OnInProgressDroplet_SetsOutcomeWithStep
// verifies that actionRecirculate with a non-empty step on an in_progress droplet
// sets outcome="recirculate:<step>" via SetOutcome.
//
// Given: a real cistern DB with an in_progress droplet
// When:  execActionCmd with actionRecirculate and input "review" is executed
// Then:  tuiActionResultMsg.err is nil, outcome is "recirculate:review", status is still "in_progress"
func TestExecActionCmd_Recirculate_WithStep_OnInProgressDroplet_SetsOutcomeWithStep(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Advance to in_progress.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(id, "in_progress"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionRecirculate, "review")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != "recirculate:review" {
		t.Errorf("outcome = %q, want %q", d.Outcome, "recirculate:review")
	}
	if d.Status != "in_progress" {
		t.Errorf("status = %q, want %q (in_progress recirculate must not call Assign)", d.Status, "in_progress")
	}
}

// TestExecActionCmd_Approve_SetsDeliveryStep verifies that execActionCmd with
// actionApprove sets the droplet's current_cataractae to "delivery" and
// status to "open" (via Assign).
//
// Given: a real cistern DB with a droplet at cataractae "human"
// When:  execActionCmd with actionApprove is executed
// Then:  tuiActionResultMsg.err is nil, status is "open", CurrentCataractae is "delivery"
func TestExecActionCmd_Approve_SetsDeliveryStep(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Set cataractae to "human" to simulate human-gated state.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetCataractae(id, "human"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionApprove, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.CurrentCataractae != "delivery" {
		t.Errorf("CurrentCataractae = %q, want %q", d.CurrentCataractae, "delivery")
	}
	if d.Status != "open" {
		t.Errorf("status = %q, want %q (Assign should set status=open)", d.Status, "open")
	}
}

// TestExecActionCmd_Pass_OnTerminalDroplet_ReturnsError verifies that execActionCmd
// with actionPass on a terminal (cancelled) droplet returns an error and does not
// modify the droplet — guarding against TOCTOU races where a concurrent status
// change happens after the palette is shown but before the action executes.
//
// Given: a real cistern DB with a cancelled droplet
// When:  execActionCmd with actionPass is executed
// Then:  tuiActionResultMsg.err is non-nil and droplet status remains "cancelled"
func TestExecActionCmd_Pass_OnTerminalDroplet_ReturnsError(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Cancel the droplet to make it terminal.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Cancel(id, ""); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionPass, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("err = nil, want non-nil error for terminal droplet")
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "cancelled" {
		t.Errorf("status = %q, want %q (cancelled droplet must not be modified)", d.Status, "cancelled")
	}
}

// TestExecActionCmd_Recirculate_OnTerminalDroplet_ReturnsError verifies that
// execActionCmd with actionRecirculate on a terminal (cancelled) droplet returns
// an error and does not reopen the droplet — guarding against TOCTOU races.
//
// Given: a real cistern DB with a cancelled droplet
// When:  execActionCmd with actionRecirculate is executed
// Then:  tuiActionResultMsg.err is non-nil and droplet status remains "cancelled"
func TestExecActionCmd_Recirculate_OnTerminalDroplet_ReturnsError(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Cancel the droplet to make it terminal.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Cancel(id, ""); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionRecirculate, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("err = nil, want non-nil error for terminal droplet")
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "cancelled" {
		t.Errorf("status = %q, want %q (cancelled droplet must not be reopened)", d.Status, "cancelled")
	}
}

// TestExecActionCmd_Approve_OnTerminalDroplet_ReturnsError verifies that
// execActionCmd with actionApprove on a terminal (cancelled) droplet returns
// an error and does not reopen the droplet — guarding against TOCTOU races
// where the droplet is cancelled after the palette renders but before confirm.
// Cancel does not clear current_cataractae, so a cancelled droplet at "human"
// would otherwise pass the CurrentCataractae check and be silently reopened.
//
// Given: a real cistern DB with a cancelled droplet at cataractae "human"
// When:  execActionCmd with actionApprove is executed
// Then:  tuiActionResultMsg.err is non-nil and droplet status remains "cancelled"
func TestExecActionCmd_Approve_OnTerminalDroplet_ReturnsError(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Set cataractae to "human" and then cancel — simulates the TOCTOU window.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetCataractae(id, "human"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	if err := c.Cancel(id, ""); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionApprove, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("err = nil, want non-nil error for terminal droplet")
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "cancelled" {
		t.Errorf("status = %q, want %q (cancelled droplet must not be reopened)", d.Status, "cancelled")
	}
}

// TestExecActionCmd_Approve_WhenNotHumanGated_ReturnsError verifies that
// execActionCmd with actionApprove on a droplet not at the "human" cataractae
// returns an error and does not force the droplet to the delivery step —
// guarding against TOCTOU races where a stale palette shows approve for a
// droplet that has since moved past the human gate.
//
// Given: a real cistern DB with a droplet at cataractae "implement" (not "human")
// When:  execActionCmd with actionApprove is executed
// Then:  tuiActionResultMsg.err is non-nil and CurrentCataractae remains "implement"
func TestExecActionCmd_Approve_WhenNotHumanGated_ReturnsError(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	// Set cataractae to "implement" — not the human gate.
	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetCataractae(id, "implement"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionApprove, "")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("err = nil, want non-nil error when not at human gate")
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.CurrentCataractae != "implement" {
		t.Errorf("CurrentCataractae = %q, want %q (non-human droplet must not be moved to delivery)", d.CurrentCataractae, "implement")
	}
}

// ── Structural actions: multi-field overlay ───────────────────────────────────

// TestTabApp_Droplets_N_Key_OpensMultiOverlay_ForCreate verifies that pressing
// 'N' (shift-n) in the Droplets tab activates the multi-field overlay for the
// new-droplet creation form.
//
// Given: a model in Droplets tab with data loaded
// When:  'N' is pressed
// Then:  overlayMode=overlayMulti, overlayAction=actionCreateDroplet, overlayMultiIdx=0
func TestTabApp_Droplets_N_Key_OpensMultiOverlay_ForCreate(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.width = 120
	m.height = 30

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayMulti {
		t.Errorf("overlayMode = %d, want overlayMulti (%d)", um.overlayMode, overlayMulti)
	}
	if um.overlayAction != actionCreateDroplet {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionCreateDroplet)
	}
	if um.overlayMultiIdx != 0 {
		t.Errorf("overlayMultiIdx = %d, want 0", um.overlayMultiIdx)
	}
	if len(um.overlayMultiFields) == 0 {
		t.Error("overlayMultiFields should be non-empty after opening create form")
	}
}

// TestTabApp_MultiOverlay_TypeChar_AppendsToInput verifies that pressing a
// printable key while overlayMulti is active appends to overlayInput.
//
// Given: a model with overlayMulti active and overlayInput=""
// When:  'a', 'b', 'c' are pressed
// Then:  overlayInput becomes "abc"
func TestTabApp_MultiOverlay_TypeChar_AppendsToInput(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayMulti
	m.overlayAction = actionEditMeta
	m.overlayMultiFields = []string{"title", "priority (1-5)", "complexity (1-3)", "description"}
	m.overlayMultiIdx = 0
	m.overlayMultiValues = make([]string, 4)
	m.overlayInput = ""

	for _, ch := range []rune{'a', 'b', 'c'} {
		raw, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = raw.(tabAppModel)
	}

	if m.overlayInput != "abc" {
		t.Errorf("overlayInput = %q, want %q", m.overlayInput, "abc")
	}
	if m.overlayMode != overlayMulti {
		t.Errorf("overlayMode = %d, want overlayMulti (%d) — should stay open during typing", m.overlayMode, overlayMulti)
	}
}

// TestTabApp_MultiOverlay_Backspace_RemovesLastChar verifies that pressing
// backspace in the multi-field overlay removes the last rune.
//
// Given: a model with overlayMulti active and overlayInput="hello"
// When:  backspace is pressed
// Then:  overlayInput becomes "hell"
func TestTabApp_MultiOverlay_Backspace_RemovesLastChar(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayMulti
	m.overlayAction = actionEditMeta
	m.overlayMultiFields = []string{"title", "priority"}
	m.overlayMultiIdx = 0
	m.overlayMultiValues = make([]string, 2)
	m.overlayInput = "hello"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(tabAppModel)

	if um.overlayInput != "hell" {
		t.Errorf("overlayInput = %q, want %q after backspace", um.overlayInput, "hell")
	}
}

// TestTabApp_MultiOverlay_Enter_AdvancesToNextField verifies that pressing
// Enter on a non-last field saves the value and advances to the next field.
//
// Given: a model with overlayMulti active on field 0 of 2, overlayInput="myrepo"
// When:  enter is pressed
// Then:  overlayMultiIdx becomes 1, overlayInput is cleared, overlayMultiValues[0]="myrepo"
func TestTabApp_MultiOverlay_Enter_AdvancesToNextField(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDroplets
	m.width = 120
	m.height = 30
	m.overlayMode = overlayMulti
	m.overlayAction = actionCreateDroplet
	m.overlayMultiFields = []string{"repo", "title", "description", "complexity (1-3)"}
	m.overlayMultiIdx = 0
	m.overlayMultiValues = make([]string, 4)
	m.overlayInput = "myrepo"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMultiIdx != 1 {
		t.Errorf("overlayMultiIdx = %d, want 1 after enter on field 0", um.overlayMultiIdx)
	}
	if um.overlayInput != "" {
		t.Errorf("overlayInput = %q, want empty after advancing to next field", um.overlayInput)
	}
	if um.overlayMultiValues[0] != "myrepo" {
		t.Errorf("overlayMultiValues[0] = %q, want %q", um.overlayMultiValues[0], "myrepo")
	}
	if um.overlayMode != overlayMulti {
		t.Errorf("overlayMode = %d, want overlayMulti (%d) — form should remain open", um.overlayMode, overlayMulti)
	}
}

// TestTabApp_MultiOverlay_Enter_OnLastField_ClosesAndDispatches verifies that
// pressing Enter on the last field closes the overlay and returns a non-nil cmd.
//
// Given: a model with overlayMulti on the last field, overlayInput="2"
// When:  enter is pressed
// Then:  overlayMode=overlayNone, cmd != nil
func TestTabApp_MultiOverlay_Enter_OnLastField_ClosesAndDispatches(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDroplets
	m.width = 120
	m.height = 30
	m.overlayMode = overlayMulti
	m.overlayAction = actionCreateDroplet
	m.overlayMultiFields = []string{"repo", "title", "description", "complexity (1-3)"}
	m.overlayMultiIdx = 3 // last field
	m.overlayMultiValues = []string{"myrepo", "mytitle", "desc", ""}
	m.overlayInput = "2"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after completing last field", um.overlayMode, overlayNone)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd after completing last field, got nil")
	}
}

// TestTabApp_MultiOverlay_Esc_DismissesOverlay verifies that pressing esc in
// the multi-field overlay closes it without executing any action.
//
// Given: a model with overlayMulti active on field 1 with overlayInput="foo"
// When:  esc is pressed
// Then:  overlayMode=overlayNone, overlayInput=""
func TestTabApp_MultiOverlay_Esc_DismissesOverlay(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayMulti
	m.overlayAction = actionEditMeta
	m.overlayMultiFields = []string{"title", "priority"}
	m.overlayMultiIdx = 1
	m.overlayMultiValues = []string{"saved-title", ""}
	m.overlayInput = "foo"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) after esc", um.overlayMode, overlayNone)
	}
	if um.overlayInput != "" {
		t.Errorf("overlayInput = %q, want empty after esc", um.overlayInput)
	}
	if cmd != nil {
		t.Error("expected nil cmd after esc dismissal, got non-nil")
	}
}

// ── Detail panel: issue list and cursor ──────────────────────────────────────

// TestTabApp_DetailDataMsg_WithIssues_PopulatesDetailIssues verifies that a
// tuiDetailDataMsg carrying issues populates detailIssues on the model.
//
// Given: a model in Detail tab with selectedID="ci-aaa"
// When:  tuiDetailDataMsg arrives with one issue
// Then:  detailIssues has length 1
func TestTabApp_DetailDataMsg_WithIssues_PopulatesDetailIssues(t *testing.T) {
	m := detailModelWithDroplet()

	updated, _ := m.Update(tuiDetailDataMsg{
		dropletID: "ci-aaa",
		issues: []cistern.DropletIssue{
			{ID: "ci-aaa-xxxxx", DropletID: "ci-aaa", Description: "test issue", Status: "open"},
		},
	})
	um := updated.(tabAppModel)

	if len(um.detailIssues) != 1 {
		t.Errorf("detailIssues len = %d, want 1", len(um.detailIssues))
	}
}

// TestTabApp_Detail_BracketRight_MoveIssueCursorForward verifies that pressing
// ']' advances the issue cursor to the next issue.
//
// Given: a model with detailIssues=[2 issues] and detailIssueCursor=0
// When:  ']' is pressed
// Then:  detailIssueCursor becomes 1
func TestTabApp_Detail_BracketRight_MoveIssueCursorForward(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
		{ID: "ci-aaa-yyyyy", Status: "open"},
	}
	m.detailIssueCursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	um := updated.(tabAppModel)

	if um.detailIssueCursor != 1 {
		t.Errorf("detailIssueCursor = %d, want 1 after ']'", um.detailIssueCursor)
	}
}

// TestTabApp_Detail_BracketLeft_MoveIssueCursorBackward verifies that pressing
// '[' moves the issue cursor to the previous issue.
//
// Given: a model with detailIssues=[2 issues] and detailIssueCursor=1
// When:  '[' is pressed
// Then:  detailIssueCursor becomes 0
func TestTabApp_Detail_BracketLeft_MoveIssueCursorBackward(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
		{ID: "ci-aaa-yyyyy", Status: "open"},
	}
	m.detailIssueCursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	um := updated.(tabAppModel)

	if um.detailIssueCursor != 0 {
		t.Errorf("detailIssueCursor = %d, want 0 after '['", um.detailIssueCursor)
	}
}

// TestTabApp_Detail_BracketRight_AtLastItem_Stays verifies that pressing ']'
// at the last issue does not advance the cursor past the end.
//
// Given: a model with detailIssues=[2 issues] and detailIssueCursor=1 (last item)
// When:  ']' is pressed
// Then:  detailIssueCursor stays at 1
func TestTabApp_Detail_BracketRight_AtLastItem_Stays(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
		{ID: "ci-aaa-yyyyy", Status: "open"},
	}
	m.detailIssueCursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	um := updated.(tabAppModel)

	if um.detailIssueCursor != 1 {
		t.Errorf("detailIssueCursor = %d, want 1 (should not advance past last item)", um.detailIssueCursor)
	}
}

// TestTabApp_Detail_BracketLeft_AtZero_Stays verifies that pressing '[' at the
// first issue does not move the cursor to a negative index.
//
// Given: a model with detailIssues=[2 issues] and detailIssueCursor=0
// When:  '[' is pressed
// Then:  detailIssueCursor stays at 0
func TestTabApp_Detail_BracketLeft_AtZero_Stays(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
		{ID: "ci-aaa-yyyyy", Status: "open"},
	}
	m.detailIssueCursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	um := updated.(tabAppModel)

	if um.detailIssueCursor != 0 {
		t.Errorf("detailIssueCursor = %d, want 0 (should not go below 0)", um.detailIssueCursor)
	}
}

// TestTabApp_Detail_BracketRight_AtNoCursor_MovesToFirst verifies that pressing
// ']' when detailIssueCursor=-1 (no selection) selects the first issue.
//
// Given: a model with detailIssues=[1 issue] and detailIssueCursor=-1
// When:  ']' is pressed
// Then:  detailIssueCursor becomes 0
func TestTabApp_Detail_BracketRight_AtNoCursor_MovesToFirst(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
	}
	m.detailIssueCursor = -1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	um := updated.(tabAppModel)

	if um.detailIssueCursor != 0 {
		t.Errorf("detailIssueCursor = %d, want 0 after ']' from no-selection", um.detailIssueCursor)
	}
}

// ── Detail panel: resolve/reject issue via inline cursor ─────────────────────

// TestTabApp_Detail_V_Key_OpensTextOverlay_ForResolve verifies that pressing 'v'
// when an issue is selected activates overlayText for the resolve action.
//
// Given: a model with detailIssues=[1 issue], detailIssueCursor=0
// When:  'v' is pressed
// Then:  overlayMode=overlayText, overlayAction=actionResolveIssue, pendingIssueID set
func TestTabApp_Detail_V_Key_OpensTextOverlay_ForResolve(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
	}
	m.detailIssueCursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d)", um.overlayMode, overlayText)
	}
	if um.overlayAction != actionResolveIssue {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionResolveIssue)
	}
	if um.pendingIssueID != "ci-aaa-xxxxx" {
		t.Errorf("pendingIssueID = %q, want %q", um.pendingIssueID, "ci-aaa-xxxxx")
	}
}

// TestTabApp_Detail_V_Key_NoIssueSelected_IsNoOp verifies that pressing 'v'
// when no issue is selected (cursor=-1) does not open an overlay.
//
// Given: a model with detailIssues=[1 issue], detailIssueCursor=-1
// When:  'v' is pressed
// Then:  overlayMode remains overlayNone
func TestTabApp_Detail_V_Key_NoIssueSelected_IsNoOp(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
	}
	m.detailIssueCursor = -1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) — no issue selected", um.overlayMode, overlayNone)
	}
}

// TestTabApp_Detail_U_Key_OpensTextOverlay_ForReject verifies that pressing 'u'
// when an issue is selected activates overlayText for the reject action.
//
// Given: a model with detailIssues=[1 issue], detailIssueCursor=0
// When:  'u' is pressed
// Then:  overlayMode=overlayText, overlayAction=actionRejectIssue
func TestTabApp_Detail_U_Key_OpensTextOverlay_ForReject(t *testing.T) {
	m := detailModelWithDroplet()
	m.detailIssues = []cistern.DropletIssue{
		{ID: "ci-aaa-xxxxx", Status: "open"},
	}
	m.detailIssueCursor = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayText {
		t.Errorf("overlayMode = %d, want overlayText (%d)", um.overlayMode, overlayText)
	}
	if um.overlayAction != actionRejectIssue {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionRejectIssue)
	}
}

// TestTabApp_Detail_V_Key_Enter_EmptyEvidence_DispatchesCmd verifies that
// pressing enter with empty evidence on a resolve overlay dispatches a cmd
// (evidence is optional for resolve/reject).
//
// Given: a model with overlayText/actionResolveIssue active, overlayInput=""
// When:  enter is pressed
// Then:  cmd != nil (not a no-op even with empty input)
func TestTabApp_Detail_V_Key_Enter_EmptyEvidence_DispatchesCmd(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionResolveIssue
	m.pendingIssueID = "ci-aaa-xxxxx"
	m.overlayInput = ""

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d) — should close after enter", um.overlayMode, overlayNone)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for resolve with empty evidence (evidence is optional), got nil")
	}
}

// ── execActionCmd: new structural actions ─────────────────────────────────────

// TestExecActionCmd_AddDependency_AddsDep verifies that execActionCmd with
// actionAddDep creates a dependency between two droplets.
//
// Given: a real cistern DB with two droplets
// When:  execActionCmd with actionAddDep is executed with the second droplet ID as input
// Then:  tuiActionResultMsg.err is nil and GetDependencies returns the second droplet
func TestExecActionCmd_AddDependency_AddsDep(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	dep, err := c.Add("test-repo", "dep droplet", "", 1, 1)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionAddDep, dep.ID)
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	deps, err := c2.GetDependencies(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0] != dep.ID {
		t.Errorf("GetDependencies = %v, want [%q]", deps, dep.ID)
	}
}

// TestExecActionCmd_RemoveDependency_RemovesDep verifies that execActionCmd with
// actionRemoveDep removes a previously added dependency.
//
// Given: a real cistern DB with a droplet that has one dependency
// When:  execActionCmd with actionRemoveDep is executed
// Then:  tuiActionResultMsg.err is nil and GetDependencies returns empty
func TestExecActionCmd_RemoveDependency_RemovesDep(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	dep, err := c.Add("test-repo", "dep droplet", "", 1, 1)
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	if err := c.AddDependency(id, dep.ID); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionRemoveDep, dep.ID)
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	deps, err := c2.GetDependencies(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 0 {
		t.Errorf("GetDependencies = %v, want empty after removal", deps)
	}
}

// TestExecActionCmd_FileIssue_CreatesIssue verifies that execActionCmd with
// actionFileIssue creates an open issue on the droplet.
//
// Given: a real cistern DB with a droplet
// When:  execActionCmd with actionFileIssue and a description is executed
// Then:  tuiActionResultMsg.err is nil and ListIssues returns one open issue
func TestExecActionCmd_FileIssue_CreatesIssue(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	m := newTabAppModel("", dbPath)
	cmd := m.execActionCmd(id, actionFileIssue, "missing tests")
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	issues, err := c.ListIssues(id, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Errorf("ListIssues len = %d, want 1", len(issues))
	}
	if issues[0].Description != "missing tests" {
		t.Errorf("issue description = %q, want %q", issues[0].Description, "missing tests")
	}
}

// ── execMultiActionCmd: create droplet ───────────────────────────────────────

// TestExecMultiActionCmd_CreateDroplet_CreatesDroplet verifies that
// execMultiActionCmd with actionCreateDroplet creates a new droplet in the DB.
//
// Given: a real cistern DB
// When:  execMultiActionCmd with actionCreateDroplet and [repo, title, desc, complexity]
// Then:  tuiActionResultMsg.err is nil and a droplet with the given title exists
func TestExecMultiActionCmd_CreateDroplet_CreatesDroplet(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	m := newTabAppModel("", dbPath)
	cmd := m.execMultiActionCmd(actionCreateDroplet, []string{"test-repo", "my new task", "some description", "2"})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	items, err := c.List("test-repo", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("List len = %d, want 1", len(items))
	}
	if items[0].Title != "my new task" {
		t.Errorf("title = %q, want %q", items[0].Title, "my new task")
	}
	if items[0].Complexity != 2 {
		t.Errorf("complexity = %d, want 2", items[0].Complexity)
	}
}

// TestExecMultiActionCmd_CreateDroplet_EmptyRepo_ReturnsError verifies that
// execMultiActionCmd with actionCreateDroplet and an empty repo returns an error.
//
// Given: a real cistern DB
// When:  execMultiActionCmd is invoked with repo=""
// Then:  tuiActionResultMsg.err is non-nil
func TestExecMultiActionCmd_CreateDroplet_EmptyRepo_ReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	m := newTabAppModel("", dbPath)
	cmd := m.execMultiActionCmd(actionCreateDroplet, []string{"", "my title", "", "1"})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("err = nil, want non-nil when repo is empty")
	}
}

// TestExecMultiActionCmd_CreateDroplet_EmptyTitle_ReturnsError verifies that
// execMultiActionCmd with actionCreateDroplet and an empty title returns an error.
//
// Given: a real cistern DB
// When:  execMultiActionCmd is invoked with title=""
// Then:  tuiActionResultMsg.err is non-nil
func TestExecMultiActionCmd_CreateDroplet_EmptyTitle_ReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	m := newTabAppModel("", dbPath)
	cmd := m.execMultiActionCmd(actionCreateDroplet, []string{"myrepo", "", "", "1"})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("err = nil, want non-nil when title is empty")
	}
}

// TestExecMultiActionCmd_EditMeta_UpdatesTitle verifies that execMultiActionCmd
// with actionEditMeta updates the droplet title.
//
// Given: a real cistern DB with an open droplet
// When:  execMultiActionCmd is invoked with a new title
// Then:  tuiActionResultMsg.err is nil and the droplet has the new title
func TestExecMultiActionCmd_EditMeta_UpdatesTitle(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	m := newTabAppModel("", dbPath)
	m.selectedID = id
	cmd := m.execMultiActionCmd(actionEditMeta, []string{"updated title", "", "", ""})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
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
	if d.Title != "updated title" {
		t.Errorf("title = %q, want %q", d.Title, "updated title")
	}
}

// ── tuiPaletteActionMsg: new structural actions ───────────────────────────────

// TestTabApp_PaletteActionMsg_StructuralActions_OpensDetailAndOverlay verifies
// that structural palette actions (edit meta, add dep, remove dep, file issue)
// open the Detail tab with the correct overlay.
//
// Given: a tabAppModel with one droplet in CisternItems
// When:  tuiPaletteActionMsg arrives for each structural action
// Then:  tab=tabDetail, overlayMode=expected, overlayAction=action
func TestTabApp_PaletteActionMsg_StructuralActions_OpensDetailAndOverlay(t *testing.T) {
	tests := []struct {
		action      string
		overlayMode int
	}{
		{actionAddDep, overlayText},
		{actionRemoveDep, overlayText},
		{actionFileIssue, overlayText},
		{actionEditMeta, overlayMulti},
		{actionResolveIssue, overlayMulti},
		{actionRejectIssue, overlayMulti},
	}
	for _, tt := range tests {
		m := newTabAppModel("", "")
		m.data = &DashboardData{
			CisternItems: []*cistern.Droplet{
				{ID: "ci-aaa", Title: "Test", Status: "open"},
			},
		}
		updated, _ := m.Update(tuiPaletteActionMsg{dropletID: "ci-aaa", action: tt.action})
		um := updated.(tabAppModel)
		if um.tab != tabDetail {
			t.Errorf("action %q: tab = %d, want tabDetail (%d)", tt.action, um.tab, tabDetail)
		}
		if um.overlayMode != tt.overlayMode {
			t.Errorf("action %q: overlayMode = %d, want %d", tt.action, um.overlayMode, tt.overlayMode)
		}
		if um.overlayAction != tt.action {
			t.Errorf("action %q: overlayAction = %q, want %q", tt.action, um.overlayAction, tt.action)
		}
	}
}

// TestTabApp_PaletteActionMsg_CreateDroplet_SetsMultiOverlayOnDropletsTab verifies
// that actionCreateDroplet via palette sets overlayMulti on the Droplets tab
// without requiring a selected droplet.
//
// Given: a tabAppModel with no data
// When:  tuiPaletteActionMsg{dropletID: "", action: actionCreateDroplet} arrives
// Then:  tab=tabDroplets, overlayMode=overlayMulti, overlayAction=actionCreateDroplet
func TestTabApp_PaletteActionMsg_CreateDroplet_SetsMultiOverlayOnDropletsTab(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}

	updated, _ := m.Update(tuiPaletteActionMsg{dropletID: "", action: actionCreateDroplet})
	um := updated.(tabAppModel)

	if um.tab != tabDroplets {
		t.Errorf("tab = %d, want tabDroplets (%d)", um.tab, tabDroplets)
	}
	if um.overlayMode != overlayMulti {
		t.Errorf("overlayMode = %d, want overlayMulti (%d)", um.overlayMode, overlayMulti)
	}
	if um.overlayAction != actionCreateDroplet {
		t.Errorf("overlayAction = %q, want %q", um.overlayAction, actionCreateDroplet)
	}
	if len(um.overlayMultiFields) == 0 {
		t.Error("overlayMultiFields should be non-empty for create form")
	}
}

// TestTabApp_MultiOverlay_EditMeta_MultiFields verifies that editMeta multi-field
// overlay has the expected number of fields (title, priority, complexity, description).
//
// Given: a tuiPaletteActionMsg for actionEditMeta on an existing droplet
// When:  the message is handled
// Then:  overlayMultiFields has 4 fields
func TestTabApp_MultiOverlay_EditMeta_MultiFields(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{
		CisternItems: []*cistern.Droplet{
			{ID: "ci-aaa", Title: "Test", Status: "open"},
		},
	}

	updated, _ := m.Update(tuiPaletteActionMsg{dropletID: "ci-aaa", action: actionEditMeta})
	um := updated.(tabAppModel)

	if len(um.overlayMultiFields) != 4 {
		t.Errorf("overlayMultiFields len = %d, want 4 for editMeta", len(um.overlayMultiFields))
	}
}

// TestTabApp_Detail_V_Key_TextOverlay_WithEvidence_DispatchesIssueCmd verifies
// that entering evidence text and pressing enter dispatches a non-nil cmd.
//
// Given: a model with overlayText/actionResolveIssue active and some evidence text
// When:  enter is pressed
// Then:  overlayMode=overlayNone, cmd != nil
func TestTabApp_Detail_V_Key_TextOverlay_WithEvidence_DispatchesIssueCmd(t *testing.T) {
	m := detailModelWithDroplet()
	m.overlayMode = overlayText
	m.overlayAction = actionResolveIssue
	m.pendingIssueID = "ci-aaa-xxxxx"
	m.overlayInput = "fixed the issue"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(tabAppModel)

	if um.overlayMode != overlayNone {
		t.Errorf("overlayMode = %d, want overlayNone (%d)", um.overlayMode, overlayNone)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd after enter with evidence text, got nil")
	}
}

// ── execMultiActionCmd: resolve/reject issue (palette path) ──────────────────

// TestExecMultiActionCmd_ResolveIssue_ResolvesIssue verifies that
// execMultiActionCmd with actionResolveIssue marks the targeted issue as resolved.
//
// Given: a real cistern DB with a droplet and an open issue
// When:  execMultiActionCmd with actionResolveIssue and [issueID, evidence] is executed
// Then:  tuiActionResultMsg.err is nil and the issue status is "resolved"
func TestExecMultiActionCmd_ResolveIssue_ResolvesIssue(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	iss, err := c.AddIssue(id, "test-flagger", "found a problem")
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	m := newTabAppModel("", dbPath)
	m.selectedID = id
	cmd := m.execMultiActionCmd(actionResolveIssue, []string{iss.ID, "all fixed"})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	issues, err := c2.ListIssues(id, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Status != "resolved" {
		t.Errorf("issue status = %q (after resolve), want %q", issues[0].Status, "resolved")
	}
}

// TestExecMultiActionCmd_RejectIssue_RejectsIssue verifies that
// execMultiActionCmd with actionRejectIssue marks the targeted issue as unresolved.
//
// Given: a real cistern DB with a droplet and an open issue
// When:  execMultiActionCmd with actionRejectIssue and [issueID, evidence] is executed
// Then:  tuiActionResultMsg.err is nil and the issue status is "unresolved"
func TestExecMultiActionCmd_RejectIssue_RejectsIssue(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	iss, err := c.AddIssue(id, "test-flagger", "found a problem")
	c.Close()
	if err != nil {
		t.Fatal(err)
	}

	m := newTabAppModel("", dbPath)
	m.selectedID = id
	cmd := m.execMultiActionCmd(actionRejectIssue, []string{iss.ID, "still an issue"})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err != nil {
		t.Errorf("err = %v, want nil", am.err)
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	issues, err := c2.ListIssues(id, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Status != "unresolved" {
		t.Errorf("issue status = %q (after reject), want %q", issues[0].Status, "unresolved")
	}
}

// TestExecMultiActionCmd_ResolveIssue_EmptyID_ReturnsError verifies that
// execMultiActionCmd returns an error when no issue ID is provided.
//
// Given: a real cistern DB
// When:  execMultiActionCmd with actionResolveIssue and ["", ""] is executed
// Then:  tuiActionResultMsg.err is non-nil
func TestExecMultiActionCmd_ResolveIssue_EmptyID_ReturnsError(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	m := newTabAppModel("", dbPath)
	m.selectedID = id
	cmd := m.execMultiActionCmd(actionResolveIssue, []string{"", ""})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("expected error for empty issue ID, got nil")
	}
}

// ── updateDroplets: overlayErr cleared on keypress ────────────────────────────

// TestUpdateDroplets_KeyPress_ClearsOverlayErr verifies that pressing a key in
// the Droplets tab (with no overlay active) clears a prior overlayErr.
//
// Given: a model on the Droplets tab with overlayErr set and no overlay active
// When:  a key is pressed
// Then:  overlayErr is ""
func TestUpdateDroplets_KeyPress_ClearsOverlayErr(t *testing.T) {
	m := newTabAppModel("", "")
	m.data = &DashboardData{}
	m.tab = tabDroplets
	m.overlayMode = overlayNone
	m.overlayErr = "something went wrong"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	um := updated.(tabAppModel)

	if um.overlayErr != "" {
		t.Errorf("overlayErr = %q after keypress, want empty string", um.overlayErr)
	}
}

// ── execMultiActionCmd: actionEditMeta partial-update guard ──────────────────

// TestExecMultiActionCmd_EditMeta_InProgress_NoPartialUpdate verifies that
// editing a droplet that is in_progress with non-title fields returns an error
// and does NOT update the title (atomic: no partial state).
//
// Given: a cistern DB with a droplet whose status is in_progress
// When:  execMultiActionCmd with actionEditMeta and [newTitle, priority, "", ""]
// Then:  tuiActionResultMsg.err is non-nil and the title is unchanged
func TestExecMultiActionCmd_EditMeta_InProgress_NoPartialUpdate(t *testing.T) {
	dbPath, id := newTestDBWithDroplet(t)

	c, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus(id, "in_progress"); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()

	m := newTabAppModel("", dbPath)
	m.selectedID = id
	// Title AND priority — EditDroplet will fail for in_progress, title must not be committed.
	cmd := m.execMultiActionCmd(actionEditMeta, []string{"new title", "3", "", ""})
	msg := cmd()

	am, ok := msg.(tuiActionResultMsg)
	if !ok {
		t.Fatalf("expected tuiActionResultMsg, got %T", msg)
	}
	if am.err == nil {
		t.Error("expected error for in_progress droplet with edit fields, got nil")
	}

	c2, err := cistern.New(dbPath, "ci")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	d, err := c2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title == "new title" {
		t.Error("title was updated despite error — partial update occurred")
	}
}
