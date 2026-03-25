package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestDashboard_PeekKey_InTmux_SpawnsNewWindow verifies that when 'p' is pressed
// inside a tmux session with one active aqueduct, a new tmux window is spawned
// targeting the correct session rather than opening the inline peek overlay.
//
// Given: a dashboard model with one active aqueduct (Name="virgo", RepoName="myrepo",
//
//	DropletID="ci-test01") and insideTmux() returning true
//
// When:  the 'p' key is pressed and the returned tea.Cmd is executed
// Then:  tmuxNewWindowFunc is called with dropletID="ci-test01" and session="myrepo-virgo",
//
//	and peekActive remains false (dashboard is not interrupted)
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

// TestTuiAqueductRow_WaterfallIndex_WidePoolRowsAtBottom verifies that when a
// droplet is on the final step the wide-pool waterfall rows (containing "≈")
// appear at the bottom of the arch, not near the top.
//
// wfRows is a 14-element array indexed 0..13. The arch loop runs r=5..13 (9
// iterations). Using wfRows[r] skips the first five entries and places the
// wide-pool rows (indices 7–8) at arch rows r=7 and r=8 — near the top.
// The correct index is wfRows[r-5], which maps r=12→wfRows[7] and
// r=13→wfRows[8], placing the pool at the very bottom of the waterfall.
//
// Result layout returned by tuiAqueductRow:
//
//	rows[0]    = nameLine
//	rows[1]    = infoLine
//	rows[2]    = lblLine
//	rows[3]    = l1 (channel top)
//	rows[4]    = l2 (channel water + wfExit)
//	rows[5..13] = arch rows for r=5..13
//
// Given: a CataractaeInfo with a droplet assigned to the last step
// When:  tuiAqueductRow is called at frame 0
// Then:  "≈" appears only in rows[12] and rows[13], never in rows[5..11]
func TestTuiAqueductRow_WaterfallIndex_WidePoolRowsAtBottom(t *testing.T) {
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-test01",
		Step:      "merge", // last step → isLastStep = true
		Steps:     steps,
	}
	m := dashboardTUIModel{}
	rows := m.tuiAqueductRow(ch, 0)

	// Sanity: nameLine + infoLine + lblLine + l1 + l2 + 9 arch rows = 14.
	if len(rows) != 14 {
		t.Fatalf("tuiAqueductRow returned %d rows, want 14", len(rows))
	}

	// Upper arch rows must NOT contain the wide-pool "≈" glyph.
	for i := 5; i <= 11; i++ {
		if strings.Contains(rows[i], "≈") {
			t.Errorf("rows[%d] contains '≈' (wide-pool row should be at the bottom, not row %d); got: %q", i, i, rows[i])
		}
	}

	// The last two arch rows MUST contain "≈" (wfRows[7] and wfRows[8]).
	for i := 12; i <= 13; i++ {
		if !strings.Contains(rows[i], "≈") {
			t.Errorf("rows[%d] missing '≈' (wide-pool row should appear at bottom of waterfall); got: %q", i, rows[i])
		}
	}
}

// --- Adaptive idle mode tests ---

// TestDashboardTUIModel_NotIdleAfterFirstDataMsg verifies that the model does
// not enter idle mode after the very first data message — there is no prior
// hash to compare against, so the state is considered unsettled.
//
// Given: a freshly created model with no prior state hash
// When:  an idle data message arrives (FlowingCount=0)
// Then:  idleMode is false (first poll is never idle)
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
// elapsed time.
//
// Given: a CataractaeInfo with DropletID, Title, and Elapsed set
// When:  tuiAqueductRow is called on a model with sufficient terminal width
// Then:  rows[1] (the info line) contains the title text
func TestTuiAqueductRow_InfoLine_IncludesTitle(t *testing.T) {
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-abc123",
		Title:     "Add retry logic to export pipeline",
		Step:      "implement",
		Steps:     []string{"implement", "review"},
		Elapsed:   5 * time.Minute,
	}
	m := dashboardTUIModel{width: 120}
	rows := m.tuiAqueductRow(ch, 0)

	if len(rows) < 2 {
		t.Fatalf("tuiAqueductRow returned %d rows, want at least 2", len(rows))
	}
	if !strings.Contains(rows[1], "Add retry logic to export pipeline") {
		t.Errorf("info line should include title, got: %q", rows[1])
	}
}

// TestTuiAqueductRow_InfoLine_TruncatesLongTitle verifies that a title longer
// than the available terminal width is truncated with "…".
//
// Given: a CataractaeInfo with a 200-char title and a narrow terminal (80 cols)
// When:  tuiAqueductRow is called
// Then:  rows[1] contains "…" and does not contain the full title string
func TestTuiAqueductRow_InfoLine_TruncatesLongTitle(t *testing.T) {
	longTitle := strings.Repeat("x", 200)
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-abc123",
		Title:     longTitle,
		Step:      "implement",
		Steps:     []string{"implement"},
		Elapsed:   2 * time.Minute,
	}
	m := dashboardTUIModel{width: 80}
	rows := m.tuiAqueductRow(ch, 0)

	if len(rows) < 2 {
		t.Fatalf("tuiAqueductRow returned %d rows, want at least 2", len(rows))
	}
	if !strings.Contains(rows[1], "…") {
		t.Errorf("info line should truncate long title with '…', got: %q", rows[1])
	}
	if strings.Contains(rows[1], longTitle) {
		t.Error("info line should not contain the full long title")
	}
}

// TestTuiAqueductRow_InfoLine_EmptyWhenInactive verifies that the info line is
// empty for an idle (no droplet) aqueduct.
//
// Given: a CataractaeInfo with no DropletID
// When:  tuiAqueductRow is called
// Then:  rows[1] is empty
func TestTuiAqueductRow_InfoLine_EmptyWhenInactive(t *testing.T) {
	ch := CataractaeInfo{
		Name:     "virgo",
		RepoName: "myrepo",
		Steps:    []string{"implement", "review"},
	}
	m := dashboardTUIModel{width: 120}
	rows := m.tuiAqueductRow(ch, 0)

	if len(rows) < 2 {
		t.Fatalf("tuiAqueductRow returned %d rows, want at least 2", len(rows))
	}
	if rows[1] != "" {
		t.Errorf("info line should be empty for idle aqueduct, got: %q", rows[1])
	}
}

// TestTuiFlowGraphRow_InfoLine_IncludesTitle verifies that the info line
// returned by tuiFlowGraphRow contains the droplet title when one is set.
//
// Given: a CataractaeInfo with DropletID, active Step, and Title
// When:  tuiFlowGraphRow is called on a model with sufficient width
// Then:  the returned infoLine contains the title text
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
