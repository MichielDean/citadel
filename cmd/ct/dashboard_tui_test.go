package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

func TestDashboard_PeekKey_InTmux_SpawnsNewWindow(t *testing.T) {
	// Inject insideTmux to simulate running inside a tmux session.
	origInsideTmux := insideTmux
	insideTmux = func() bool { return true }
	defer func() { insideTmux = origInsideTmux }()

	// Capture the new-window call.
	var gotDropletID, gotSession string
	origNewWindow := tmuxNewWindowFunc
	tmuxNewWindowFunc = func(dropletID, session string) error {
		gotDropletID = dropletID
		gotSession = session
		return nil
	}
	defer func() { tmuxNewWindowFunc = origNewWindow }()

	// Build a dashboard model with one active aqueduct.
	m := newDashboardTUIModel("", "")
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{
				Name:      "virgo",
				RepoName:  "myrepo",
				DropletID: "ci-test01",
				Step:      "implement",
				Steps:     []string{"implement", "review"},
			},
		},
	}

	// Press 'p' to trigger peek.
	updatedModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})

	// Execute the returned cmd to trigger tmuxNewWindowFunc.
	if cmd != nil {
		cmd()
	}

	// The dashboard should NOT have entered inline peek mode.
	um := updatedModel.(dashboardTUIModel)
	if um.peekActive {
		t.Error("peekActive should be false when spawning a new tmux window")
	}

	// Verify the new-window was called with the correct identifiers.
	if gotDropletID != "ci-test01" {
		t.Errorf("tmuxNewWindowFunc dropletID = %q, want %q", gotDropletID, "ci-test01")
	}
	wantSession := "myrepo-virgo"
	if gotSession != wantSession {
		t.Errorf("tmuxNewWindowFunc session = %q, want %q", gotSession, wantSession)
	}
}

// TestDashboard_PeekKey_InTmux_NewWindowError_FallsBackToInline verifies that
// when tmuxNewWindowFunc returns an error, the dashboard falls back to the
// inline capture-pane overlay and sets peekActive.
//
// Given: a dashboard model with one active aqueduct and insideTmux() = true
// When:  the 'p' key is pressed and tmuxNewWindowFunc returns an error
// Then:  the returned tea.Cmd yields a tuiPeekNewWindowErrMsg which, when
//
//	processed, causes peekActive to be true (inline overlay opened)

func TestDashboard_PeekKey_InTmux_NewWindowError_FallsBackToInline(t *testing.T) {
	origInsideTmux := insideTmux
	insideTmux = func() bool { return true }
	defer func() { insideTmux = origInsideTmux }()

	simulatedErr := errors.New("tmux: no server running")
	origNewWindow := tmuxNewWindowFunc
	tmuxNewWindowFunc = func(_, _ string) error { return simulatedErr }
	defer func() { tmuxNewWindowFunc = origNewWindow }()

	m := newDashboardTUIModel("", "")
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{
				Name:      "virgo",
				RepoName:  "myrepo",
				DropletID: "ci-test01",
				Step:      "implement",
				Steps:     []string{"implement", "review"},
			},
		},
	}

	// Press 'p': returns a cmd that will call tmuxNewWindowFunc.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("expected a tea.Cmd, got nil")
	}

	// Execute the cmd; it should return a tuiPeekNewWindowErrMsg.
	msg := cmd()
	errMsg, ok := msg.(tuiPeekNewWindowErrMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want tuiPeekNewWindowErrMsg", msg)
	}
	if errMsg.err != simulatedErr {
		t.Errorf("errMsg.err = %v, want %v", errMsg.err, simulatedErr)
	}

	// Process the error message; the model should activate the inline overlay.
	updatedModel, _ := m.Update(errMsg)
	um := updatedModel.(dashboardTUIModel)
	if !um.peekActive {
		t.Error("peekActive should be true after new-window error fallback to inline overlay")
	}

	// The peek header should mention the failure.
	if !strings.Contains(um.peek.header, "tmux new-window failed") {
		t.Errorf("peek header should mention failure, got: %q", um.peek.header)
	}
}

// TestDashboard_PeekSelect_InTmux_Success_ClearsPeekSelectMode verifies that
// when openPeekOn is called from the peekSelectMode picker (peekSelectMode=true)
// and insideTmux() is true and the new-window call succeeds, the returned model
// has peekSelectMode=false so the picker overlay does not persist.
//
// Given: a dashboard model with peekSelectMode=true, two active aqueducts,
//
//	insideTmux() returning true, and tmuxNewWindowFunc succeeding
//
// When:  'enter' is pressed to confirm the picker selection
// Then:  the returned model has peekSelectMode=false

func TestDashboard_PeekSelect_InTmux_Success_ClearsPeekSelectMode(t *testing.T) {
	origInsideTmux := insideTmux
	insideTmux = func() bool { return true }
	defer func() { insideTmux = origInsideTmux }()

	origNewWindow := tmuxNewWindowFunc
	tmuxNewWindowFunc = func(_, _ string) error { return nil }
	defer func() { tmuxNewWindowFunc = origNewWindow }()

	m := newDashboardTUIModel("", "")
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{
				Name:      "virgo",
				RepoName:  "myrepo",
				DropletID: "ci-test01",
				Step:      "implement",
				Steps:     []string{"implement", "review"},
			},
			{
				Name:      "scorpio",
				RepoName:  "myrepo",
				DropletID: "ci-test02",
				Step:      "review",
				Steps:     []string{"implement", "review"},
			},
		},
	}
	// Simulate being in the picker overlay with first aqueduct selected.
	m.peekSelectMode = true
	m.peekSelectIndex = 0

	// Press 'enter' to confirm selection from the peekSelectMode picker.
	updatedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	um := updatedModel.(dashboardTUIModel)
	if um.peekSelectMode {
		t.Error("peekSelectMode should be false after successful new-window spawn from picker")
	}
}

// TestTuiAqueductRow_MipmapArch_ReplacesOldPillarRows verifies that tuiAqueductRow
// uses the pre-rendered pixel art mipmap for the arch section instead of hand-drawn
// ASCII pillar rows, and that the total row count equals 3 header rows plus the
// mipmap height for the given terminal width.
//
// Layout returned by tuiAqueductRow with mipmap arch:
//
//	rows[0]     = nameLine
//	rows[1]     = infoLine
//	rows[2]     = lblLine
//	rows[3..N]  = mipmap arch lines (12 lines for width=0 → 36x12 mipmap)
//
// Given: a CataractaeInfo with a droplet assigned to the last step and a zero-width model
// When:  tuiAqueductRow is called at frame 0
// Then:  total rows == 15 (3 header + 12 mipmap lines for width=0)
//
//	and all mipmap rows (rows[3:]) are non-empty

func TestDashboardTUIModel_NotIdleAfterFirstDataMsg(t *testing.T) {
	m := newDashboardTUIModel("", "")

	data := &DashboardData{FlowingCount: 0, FetchedAt: time.Now()}
	updated, _ := m.Update(tuiDataMsg(data))
	um := updated.(dashboardTUIModel)

	if um.idleMode {
		t.Error("idleMode should be false after first data message — no prior state to compare")
	}
	if um.stateHash == "" {
		t.Error("stateHash should be set after first data message")
	}
}

// TestDashboardTUIModel_EntersIdleModeAfterUnchangedPoll verifies that the
// model enters idle mode after a second consecutive idle data message.
//
// Given: model that has received one idle data message (stateHash set)
// When:  a second identical idle data message arrives
// Then:  idleMode is true

func TestDashboardTUIModel_EntersIdleModeAfterUnchangedPoll(t *testing.T) {
	m := newDashboardTUIModel("", "")
	idleData := &DashboardData{FlowingCount: 0, FetchedAt: time.Now()}

	// First message: sets the hash baseline.
	m1, _ := m.Update(tuiDataMsg(idleData))
	um1 := m1.(dashboardTUIModel)
	if um1.idleMode {
		t.Fatal("should not be idle after first message")
	}

	// Second message: same state → should enter idle mode.
	m2, _ := um1.Update(tuiDataMsg(idleData))
	um2 := m2.(dashboardTUIModel)

	if !um2.idleMode {
		t.Error("idleMode should be true after second consecutive idle data message")
	}
}

// TestDashboardTUIModel_ExitsIdleModeWhenDropletFlows verifies that idle mode
// exits when FlowingCount > 0.
//
// Given: model already in idle mode
// When:  a data message arrives with FlowingCount=1
// Then:  idleMode is false

func TestDashboardTUIModel_ExitsIdleModeWhenDropletFlows(t *testing.T) {
	m := newDashboardTUIModel("", "")
	idleData := &DashboardData{FlowingCount: 0, FetchedAt: time.Now()}

	// Enter idle mode.
	m1, _ := m.Update(tuiDataMsg(idleData))
	m2, _ := m1.(dashboardTUIModel).Update(tuiDataMsg(idleData))
	um2 := m2.(dashboardTUIModel)
	if !um2.idleMode {
		t.Fatal("precondition: model should be in idle mode")
	}

	// Active data message → exit idle.
	activeData := &DashboardData{FlowingCount: 1, FetchedAt: time.Now()}
	m3, _ := um2.Update(tuiDataMsg(activeData))
	um3 := m3.(dashboardTUIModel)

	if um3.idleMode {
		t.Error("idleMode should be false when FlowingCount > 0")
	}
}

// TestDashboardTUIModel_ExitsIdleModeWhenStateChanges verifies that idle mode
// exits when the dashboard state changes (e.g. a new droplet is queued), even
// if FlowingCount remains 0.
//
// Given: model in idle mode with QueuedCount=0
// When:  a data message arrives with QueuedCount=1 (new item queued)
// Then:  idleMode is false

func TestDashboardTUIModel_ExitsIdleModeWhenStateChanges(t *testing.T) {
	m := newDashboardTUIModel("", "")
	idleData := &DashboardData{FlowingCount: 0, QueuedCount: 0, FetchedAt: time.Now()}

	// Enter idle mode.
	m1, _ := m.Update(tuiDataMsg(idleData))
	m2, _ := m1.(dashboardTUIModel).Update(tuiDataMsg(idleData))
	um2 := m2.(dashboardTUIModel)
	if !um2.idleMode {
		t.Fatal("precondition: model should be in idle mode")
	}

	// State change: new droplet queued.
	changedData := &DashboardData{FlowingCount: 0, QueuedCount: 1, FetchedAt: time.Now()}
	m3, _ := um2.Update(tuiDataMsg(changedData))
	um3 := m3.(dashboardTUIModel)

	if um3.idleMode {
		t.Error("idleMode should be false after a state change (new droplet queued)")
	}
}

// --- Title display tests ---

// TestTuiAqueductRow_InfoLine_IncludesTitle verifies that the info line in an
// active aqueduct arch row includes the droplet title styled dim after the
// elapsed time. The title must fit within the archBlockW budget (archPillarW+2=38)
// after the droplet ID and elapsed time are deducted.
//
// Given: a CataractaeInfo with DropletID, a short Title that fits in the block, and Elapsed
// When:  tuiAqueductRow is called
// Then:  rows[1] (the info line) contains the title text

func TestTuiFlowGraphRow_InfoLine_IncludesTitle(t *testing.T) {
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-abc123",
		Title:     "Add retry logic to export pipeline",
		Step:      "implement",
		Steps:     []string{"implement", "review"},
		Elapsed:   3 * time.Minute,
	}
	m := dashboardTUIModel{width: 120}
	_, infoLine := m.tuiFlowGraphRow(ch)

	if !strings.Contains(infoLine, "Add retry logic to export pipeline") {
		t.Errorf("flow graph info line should include title, got: %q", infoLine)
	}
}

// --- selectArchMipmap tests ---

// TestSelectArchMipmap_ReturnsNonEmpty verifies that selectArchMipmap always
// returns a non-empty string regardless of the input width.

// --- Inline flow notes tests ---

// TestViewInlineFlowNotes_WhenMatchingActivity_ReturnsNoteLines verifies that
// viewInlineFlowNotes returns content lines for a droplet with a matching
// FlowActivity.
//
// Given: a model with a FlowActivity for "ci-abc12" containing one recent note
// When:  viewInlineFlowNotes is called with CataractaeInfo{DropletID: "ci-abc12"}
// Then:  the returned lines contain the note's content text

func TestViewInlineFlowNotes_WhenMatchingActivity_ReturnsNoteLines(t *testing.T) {
	noteTime := time.Now().Add(-5 * time.Minute)
	m := dashboardTUIModel{
		width: 120,
		data: &DashboardData{
			FlowActivities: []FlowActivity{
				{
					DropletID: "ci-abc12",
					Title:     "Fix the bug",
					Step:      "review",
					RecentNotes: []cistern.CataractaeNote{
						{
							CataractaeName: "reviewer",
							Content:        "Looks good overall",
							CreatedAt:      noteTime,
						},
					},
				},
			},
		},
	}
	ch := CataractaeInfo{DropletID: "ci-abc12"}
	lines := m.viewInlineFlowNotes(ch)
	joined := stripANSITest(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "Looks good overall") {
		t.Errorf("expected note content in inline flow notes, got: %q", joined)
	}
}

// TestViewInlineFlowNotes_WhenNoMatchingActivity_ReturnsEmpty verifies that
// viewInlineFlowNotes returns an empty slice when there is no matching
// FlowActivity for the given droplet.
//
// Given: a model with FlowActivities for a different droplet
// When:  viewInlineFlowNotes is called with CataractaeInfo{DropletID: "ci-other"}
// Then:  the returned slice is empty

func TestViewInlineFlowNotes_WhenNoMatchingActivity_ReturnsEmpty(t *testing.T) {
	m := dashboardTUIModel{
		width: 120,
		data: &DashboardData{
			FlowActivities: []FlowActivity{
				{DropletID: "ci-xyz99", Title: "Other droplet"},
			},
		},
	}
	ch := CataractaeInfo{DropletID: "ci-other"}
	lines := m.viewInlineFlowNotes(ch)
	if len(lines) != 0 {
		t.Errorf("expected no lines for unmatched droplet, got: %v", lines)
	}
}

// TestViewAqueductArches_ActiveWithNotes_InlinesFlowNotesAfterProgressBar verifies
// that an active aqueduct's progress bar is immediately followed by its inline
// flow notes.
//
// Given: a model with one active aqueduct "virgo" carrying droplet "ci-abc12",
//
//	and a FlowActivity for "ci-abc12" with a note "Important finding here"
//
// When:  viewAqueductArches is called
// Then:  the output contains both the droplet ID (progress bar) and the note
//
//	text (inline flow notes), with the note appearing after the droplet ID

func TestViewAqueductArches_ActiveWithNotes_InlinesFlowNotesAfterProgressBar(t *testing.T) {
	noteTime := time.Now().Add(-2 * time.Minute)
	m := dashboardTUIModel{
		width: 120,
		data: &DashboardData{
			Cataractae: []CataractaeInfo{
				{
					Name:      "virgo",
					RepoName:  "myrepo",
					DropletID: "ci-abc12",
					Step:      "implement",
					Steps:     []string{"implement", "review"},
					Elapsed:   90 * time.Second,
				},
			},
			FlowActivities: []FlowActivity{
				{
					DropletID: "ci-abc12",
					Title:     "Fix the bug",
					Step:      "implement",
					RecentNotes: []cistern.CataractaeNote{
						{
							CataractaeName: "implementer",
							Content:        "Important finding here",
							CreatedAt:      noteTime,
						},
					},
				},
			},
		},
	}

	lines := m.viewAqueductArches()
	joined := stripANSITest(strings.Join(lines, "\n"))

	if !strings.Contains(joined, "ci-abc12") {
		t.Errorf("expected droplet ID in arches output, got: %q", joined)
	}
	if !strings.Contains(joined, "Important finding here") {
		t.Errorf("expected inline flow note in arches output, got: %q", joined)
	}
	// Note must appear after the droplet ID (progress bar comes first).
	idPos := strings.Index(joined, "ci-abc12")
	notePos := strings.Index(joined, "Important finding here")
	if notePos <= idPos {
		t.Errorf("flow note should appear after progress bar (id at %d, note at %d)", idPos, notePos)
	}
}

// TestView_NoCURRENTFLOWSection verifies that the standalone CURRENT FLOW
// section has been removed from the TUI View output.
//
// Given: a model with data and one flowing droplet
// When:  View() is called
// Then:  the output does NOT contain the "CURRENT FLOW" header

func TestView_NoCURRENTFLOWSection(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 120
	m.height = 50
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{
				Name:      "virgo",
				DropletID: "ci-abc12",
				Step:      "implement",
				Steps:     []string{"implement", "review"},
			},
		},
		FlowActivities: []FlowActivity{
			{
				DropletID: "ci-abc12",
				Title:     "Fix the bug",
				Step:      "implement",
			},
		},
		FetchedAt: time.Now(),
	}

	out := m.View()
	stripped := stripANSITest(out)
	if strings.Contains(stripped, "CURRENT FLOW") {
		t.Error("TUI View should not contain standalone CURRENT FLOW section header")
	}
}
