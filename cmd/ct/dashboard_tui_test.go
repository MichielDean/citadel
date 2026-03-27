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
func TestTuiAqueductRow_MipmapArch_ReplacesOldPillarRows(t *testing.T) {
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-test01",
		Step:      "merge", // last step → isLastStep = true
		Steps:     steps,
	}
	// Zero-width model → selectArchMipmap(0) returns 36x12 mipmap (12 lines).
	m := dashboardTUIModel{}
	rows := m.tuiAqueductRow(ch, 0)

	const wantHeaderRows = 3   // nameLine + infoLine + lblLine
	const wantMipmapLines = 12 // 36x12 mipmap: 12 visual lines after cursor-seq strip
	wantTotal := wantHeaderRows + wantMipmapLines
	if len(rows) != wantTotal {
		t.Fatalf("tuiAqueductRow returned %d rows, want %d (3 header + 12 mipmap)", len(rows), wantTotal)
	}

	// All mipmap rows must be non-empty (pixel art content present).
	for i := wantHeaderRows; i < len(rows); i++ {
		if rows[i] == "" {
			t.Errorf("rows[%d] is empty; expected non-empty mipmap line", i)
		}
	}
}

// TestTuiAqueductRow_ActiveMipmap_WaterAnimatesTopRows verifies that for an
// active aqueduct the mipmap's top 2 rows (rows[3] and rows[4]) contain animated
// wave characters (░▒▓≈) rather than the static pixel-art content.
//
// Given: a CataractaeInfo with an active droplet at the first step
// When:  tuiAqueductRow is called at frame 0
// Then:  rows[3] and rows[4] (first two mipmap rows) contain the wave char ≈,
//
//	which is unique to the wave animation and absent from the static mipmap
func TestTuiAqueductRow_ActiveMipmap_WaterAnimatesTopRows(t *testing.T) {
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-test01",
		Step:      "implement",
		Steps:     []string{"implement", "review"},
	}
	m := dashboardTUIModel{}
	rows := m.tuiAqueductRow(ch, 0)

	const headerRows = 3
	if len(rows) < headerRows+2 {
		t.Fatalf("expected at least %d rows, got %d", headerRows+2, len(rows))
	}

	// ≈ is unique to the wave animation pattern; the static mipmap never contains it.
	for _, rowIdx := range []int{headerRows, headerRows + 1} {
		if !strings.Contains(stripANSI(rows[rowIdx]), "≈") {
			t.Errorf("rows[%d] should contain wave char '≈', got: %q", rowIdx, stripANSI(rows[rowIdx]))
		}
	}
}

// TestTuiAqueductRow_IdleMipmap_TopRowsAreStatic verifies that for an idle
// aqueduct (no active droplet) the mipmap rows are unmodified static pixel art —
// no wave characters are injected.
//
// Given: a CataractaeInfo with no DropletID (idle)
// When:  tuiAqueductRow is called at frame 0
// Then:  rows[3] (first mipmap row) does not contain the wave char ≈
func TestTuiAqueductRow_IdleMipmap_TopRowsAreStatic(t *testing.T) {
	ch := CataractaeInfo{
		Name:     "virgo",
		RepoName: "myrepo",
		Steps:    []string{"implement", "review"},
	}
	m := dashboardTUIModel{}
	rows := m.tuiAqueductRow(ch, 0)

	const headerRows = 3
	if len(rows) < headerRows+1 {
		t.Fatalf("expected at least %d rows, got %d", headerRows+1, len(rows))
	}

	if strings.Contains(stripANSI(rows[headerRows]), "≈") {
		t.Errorf("rows[%d] for idle aqueduct should not contain wave char '≈', got: %q",
			headerRows, stripANSI(rows[headerRows]))
	}
}

// TestTuiAqueductRow_WaterExitAppendsToLastMipmapRow verifies that for an
// active aqueduct on its final step the wfExit indicator (░▒▓▓) is appended
// to the last mipmap row rather than a separate channel row.
//
// Given: a CataractaeInfo with a droplet assigned to the final step
// When:  tuiAqueductRow is called at frame 0
// Then:  the last returned row (last mipmap line) contains "▓▓" from wfExit
func TestTuiAqueductRow_WaterExitAppendsToLastMipmapRow(t *testing.T) {
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "myrepo",
		DropletID: "ci-test01",
		Step:      "merge",
		Steps:     []string{"implement", "review", "merge"},
	}
	m := dashboardTUIModel{}
	rows := m.tuiAqueductRow(ch, 0)

	if len(rows) == 0 {
		t.Fatal("tuiAqueductRow returned no rows")
	}

	lastRow := rows[len(rows)-1]
	if !strings.Contains(stripANSI(lastRow), "▓▓") {
		t.Errorf("last row should contain wfExit '▓▓', got: %q", stripANSI(lastRow))
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

// --- selectArchMipmap tests ---

// TestSelectArchMipmap_ReturnsCorrectLevelForWidth verifies that selectArchMipmap
// picks the mipmap level whose width is closest to the available slot width,
// using the thresholds:
//
//	width >= 90 → 100x38; width >= 70 → 80x30; width >= 50 → 60x22; else → 36x12.
//
// Given: various terminal widths
// When:  selectArchMipmap is called
// Then:  the returned string has the expected number of lines for each level
func TestSelectArchMipmap_ReturnsCorrectLevelForWidth(t *testing.T) {
	tests := []struct {
		width     int
		wantLines int
		desc      string
	}{
		{120, 37, "width 120 >= 90 → 100x38 (37 visual lines after cursor-seq strip)"},
		{100, 37, "width 100 >= 90 → 100x38 (37 visual lines after cursor-seq strip)"},
		{90, 37, "width == 90 → 100x38 (37 visual lines after cursor-seq strip)"},
		{89, 30, "width 89 < 90 → 80x30 (30 lines)"},
		{80, 30, "width 80 >= 70 → 80x30 (30 lines)"},
		{70, 30, "width == 70 → 80x30 (30 lines)"},
		{69, 22, "width 69 < 70 → 60x22 (22 lines)"},
		{60, 22, "width 60 >= 50 → 60x22 (22 lines)"},
		{50, 22, "width == 50 → 60x22 (22 lines)"},
		{49, 12, "width 49 < 50 → 36x12 (12 lines)"},
		{36, 12, "width 36 < 50 → 36x12 (12 lines)"},
		{0, 12, "width 0 < 50 → 36x12 (12 lines)"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := selectArchMipmap(tc.width)
			if got == "" {
				t.Fatalf("selectArchMipmap(%d): returned empty string", tc.width)
			}
			lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
			if len(lines) != tc.wantLines {
				t.Errorf("selectArchMipmap(%d): got %d lines, want %d", tc.width, len(lines), tc.wantLines)
			}
		})
	}
}

// TestSelectArchMipmap_EachLevelReturnsDistinctContent verifies that all four
// mipmap levels are distinct strings (i.e., the embed loaded different files).
//
// Given: the four boundary widths that select different levels
// When:  selectArchMipmap is called for each
// Then:  all four returned strings are non-empty and pairwise distinct
func TestSelectArchMipmap_EachLevelReturnsDistinctContent(t *testing.T) {
	large := selectArchMipmap(90)  // 100x38
	medium := selectArchMipmap(70) // 80x30
	small := selectArchMipmap(50)  // 60x22
	xsmall := selectArchMipmap(0)  // 36x12

	for name, s := range map[string]string{
		"large (100x38)": large,
		"medium (80x30)": medium,
		"small (60x22)":  small,
		"xsmall (36x12)": xsmall,
	} {
		if s == "" {
			t.Errorf("selectArchMipmap returned empty string for %s", name)
		}
	}
	if large == medium {
		t.Error("large (100x38) and medium (80x30) mipmaps are identical — embed may be wrong")
	}
	if medium == small {
		t.Error("medium (80x30) and small (60x22) mipmaps are identical — embed may be wrong")
	}
	if small == xsmall {
		t.Error("small (60x22) and xsmall (36x12) mipmaps are identical — embed may be wrong")
	}
	if large == xsmall {
		t.Error("large (100x38) and xsmall (36x12) mipmaps are identical — embed may be wrong")
	}
	if large == small {
		t.Error("large (100x38) and small (60x22) mipmaps are identical — embed may be wrong")
	}
	if medium == xsmall {
		t.Error("medium (80x30) and xsmall (36x12) mipmaps are identical — embed may be wrong")
	}
}
