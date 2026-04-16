package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichielDean/cistern/internal/cistern"
)

// TestDashboard_PeekKey_AlwaysCallsTmuxAttach verifies that pressing 'p'
// always attempts tmux attach-session via tea.ExecProcess, regardless of $TMUX
// environment, and that the dashboard does NOT enter inline peek mode when
// attach succeeds.
//
// Given: a dashboard model with one active aqueduct
// When:  the 'p' key is pressed and the attach succeeds
// Then:  openPeekAttachCmdFunc is called with the correct session name,
//
//	and peekActive remains false (no inline overlay opened)
func TestDashboard_PeekKey_AlwaysCallsTmuxAttach(t *testing.T) {
	var gotSession string
	origAttach := openPeekAttachCmdFunc
	openPeekAttachCmdFunc = func(session string, fn func(error) tea.Msg) tea.Cmd {
		gotSession = session
		return func() tea.Msg { return fn(nil) }
	}
	defer func() { openPeekAttachCmdFunc = origAttach }()

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

	// Execute the returned cmd (simulates bubbletea running the attach).
	if cmd != nil {
		cmd()
	}

	// The dashboard should NOT have entered inline peek mode.
	um := updatedModel.(dashboardTUIModel)
	if um.peekActive {
		t.Error("peekActive should be false when tmux attach succeeds")
	}

	// Verify attach was called with the correct session name.
	wantSession := "myrepo-virgo"
	if gotSession != wantSession {
		t.Errorf("openPeekAttachCmdFunc session = %q, want %q", gotSession, wantSession)
	}
}

// TestDashboard_PeekKey_AttachFails_FallsBackToInline verifies that when the
// tmux attach-session subprocess fails, the dashboard falls back to the inline
// capture-pane overlay and sets peekActive.
//
// Given: a dashboard model with one active aqueduct and the attach returning an error
// When:  the 'p' key is pressed and the attach fails
// Then:  the returned tea.Cmd yields a tuiPeekAttachErrMsg which, when
//
//	processed, causes peekActive to be true (inline overlay opened),
//	and the header mentions "tmux attach-session failed" (not "not inside tmux")
func TestDashboard_PeekKey_AttachFails_FallsBackToInline(t *testing.T) {
	simulatedErr := errors.New("tmux: no server running")
	origAttach := openPeekAttachCmdFunc
	openPeekAttachCmdFunc = func(_ string, fn func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg { return fn(simulatedErr) }
	}
	defer func() { openPeekAttachCmdFunc = origAttach }()

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

	// Press 'p': returns a cmd that (via our injected func) returns the error msg.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatal("expected a tea.Cmd, got nil")
	}

	// Execute the cmd; it should return a tuiPeekAttachErrMsg.
	msg := cmd()
	errMsg, ok := msg.(tuiPeekAttachErrMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want tuiPeekAttachErrMsg", msg)
	}
	if errMsg.err != simulatedErr {
		t.Errorf("errMsg.err = %v, want %v", errMsg.err, simulatedErr)
	}

	// Process the error message; the model should activate the inline overlay.
	updatedModel, _ := m.Update(errMsg)
	um := updatedModel.(dashboardTUIModel)
	if !um.peekActive {
		t.Error("peekActive should be true after attach error fallback to inline overlay")
	}

	// The header should mention the attach failure, not "not inside tmux".
	if !strings.Contains(um.peek.header, "tmux attach-session failed") {
		t.Errorf("peek header should mention attach failure, got: %q", um.peek.header)
	}
	if strings.Contains(um.peek.header, "not inside tmux") {
		t.Errorf("peek header should not contain 'not inside tmux', got: %q", um.peek.header)
	}
}

// TestDashboard_PeekSelect_AttachSucceeds_ClearsPeekSelectMode verifies that
// when openPeekOn is called from the peekSelectMode picker and the attach
// succeeds, the returned model has peekSelectMode=false.
//
// Given: a dashboard model with peekSelectMode=true, two active aqueducts,
//
//	and the attach succeeding
//
// When:  'enter' is pressed to confirm the picker selection
// Then:  the returned model has peekSelectMode=false
func TestDashboard_PeekSelect_AttachSucceeds_ClearsPeekSelectMode(t *testing.T) {
	origAttach := openPeekAttachCmdFunc
	openPeekAttachCmdFunc = func(_ string, fn func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg { return fn(nil) }
	}
	defer func() { openPeekAttachCmdFunc = origAttach }()

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
		t.Error("peekSelectMode should be false after successful attach from picker")
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

// TestViewInlineFlowNotes_WhenEmptyRecentNotes_ReturnsNoNotesMessage verifies
// that viewInlineFlowNotes renders the "(no notes yet — first pass)" placeholder
// when a FlowActivity matches but has no RecentNotes.
//
// Given: a model with a FlowActivity for "ci-abc12" with an empty RecentNotes slice
// When:  viewInlineFlowNotes is called with CataractaeInfo{DropletID: "ci-abc12"}
// Then:  the returned lines contain "(no notes yet — first pass)"

func TestViewInlineFlowNotes_WhenEmptyRecentNotes_ReturnsNoNotesMessage(t *testing.T) {
	m := dashboardTUIModel{
		width: 120,
		data: &DashboardData{
			FlowActivities: []FlowActivity{
				{
					DropletID:   "ci-abc12",
					Title:       "Fix the bug",
					Step:        "implement",
					RecentNotes: []cistern.CataractaeNote{},
				},
			},
		},
	}
	ch := CataractaeInfo{DropletID: "ci-abc12"}
	lines := m.viewInlineFlowNotes(ch)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "no notes yet") {
		t.Errorf("expected '(no notes yet — first pass)' placeholder, got: %q", joined)
	}
}

// TestViewAqueductArches_IdleAqueduct_RemainsCompactSingleLine verifies that
// idle aqueducts are rendered as a single compact line with no inline flow notes
// appended.
//
// Given: a model with one idle aqueduct (no DropletID) named "libra"
// When:  viewAqueductArches is called
// Then:  the output contains exactly one line for "libra" (no inline notes below it)

func TestViewAqueductArches_IdleAqueduct_RemainsCompactSingleLine(t *testing.T) {
	m := dashboardTUIModel{
		width: 120,
		data: &DashboardData{
			Cataractae: []CataractaeInfo{
				{
					Name:      "libra",
					RepoName:  "myrepo",
					DropletID: "", // idle — no active droplet
				},
			},
			FlowActivities: []FlowActivity{},
		},
	}

	lines := m.viewAqueductArches()

	// Count lines that mention "libra".
	libraCount := 0
	for _, l := range lines {
		if strings.Contains(stripANSITest(l), "libra") {
			libraCount++
		}
	}
	if libraCount != 1 {
		t.Errorf("idle aqueduct 'libra' should appear in exactly 1 line, got %d lines; full output: %v", libraCount, lines)
	}

	// Total output should be exactly one line (no blank lines, no inline notes).
	if len(lines) != 1 {
		t.Errorf("viewAqueductArches with one idle aqueduct should return 1 line, got %d: %v", len(lines), lines)
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

// TestOpenInlinePeek_StageElapsedZero_OmitsStageAge verifies that openInlinePeek
// omits the stage age from the header when StageElapsed is 0, consistent with
// all other display surfaces that hide stage age when not dispatched.
//
// Given: a CataractaeInfo with Elapsed set but StageElapsed=0
// When:  openInlinePeek is called
// Then:  the header does not contain "(stage"
func TestOpenInlinePeek_StageElapsedZero_OmitsStageAge(t *testing.T) {
	ch := CataractaeInfo{
		RepoName:     "myrepo",
		Name:         "virgo",
		DropletID:    "ci-abc12",
		Step:         "implement",
		Elapsed:      5 * time.Minute,
		StageElapsed: 0,
	}
	m := dashboardTUIModel{width: 120, height: 40}
	model, _ := m.openInlinePeek(ch, errors.New("attach failed"))
	pm := model.peek
	if strings.Contains(pm.header, "(stage") {
		t.Errorf("openInlinePeek header should not contain '(stage' when StageElapsed=0, got:\n%s", pm.header)
	}
	if !strings.Contains(pm.header, "flowing 5m") {
		t.Errorf("openInlinePeek header should contain overall elapsed when StageElapsed=0, got:\n%s", pm.header)
	}
}

// TestOpenInlinePeek_StageElapsedNonZero_ShowsStageAge verifies that openInlinePeek
// includes the stage age in the header when StageElapsed > 0.
//
// Given: a CataractaeInfo with Elapsed and StageElapsed both set
// When:  openInlinePeek is called
// Then:  the header contains "(stage <duration>)"
func TestOpenInlinePeek_StageElapsedNonZero_ShowsStageAge(t *testing.T) {
	ch := CataractaeInfo{
		RepoName:     "myrepo",
		Name:         "virgo",
		DropletID:    "ci-abc12",
		Step:         "implement",
		Elapsed:      10 * time.Minute,
		StageElapsed: 3*time.Minute + 22*time.Second,
	}
	m := dashboardTUIModel{width: 120, height: 40}
	model, _ := m.openInlinePeek(ch, errors.New("attach failed"))
	pm := model.peek
	if !strings.Contains(pm.header, "(stage 3m 22s)") {
		t.Errorf("openInlinePeek header should contain '(stage 3m 22s)' when StageElapsed is set, got:\n%s", pm.header)
	}
}

// TestTuiFlowGraphRow_StageElapsed_ShownAppended verifies that tuiFlowGraphRow
// shows stage elapsed appended alongside overall elapsed (not replacing it),
// consistent with the non-TUI renderFlowGraphRow.
//
// Given: an aqueduct with StageElapsed=2m 14s and Elapsed=5m 30s
// When:  tuiFlowGraphRow is called
// Then:  the info line contains both elapsed durations
func TestTuiFlowGraphRow_StageElapsed_ShownAppended(t *testing.T) {
	ch := CataractaeInfo{
		Name:            "virgo",
		DropletID:       "ci-abc12",
		Step:            "review",
		Steps:           []string{"implement", "review", "qa"},
		Elapsed:         5*time.Minute + 30*time.Second,
		StageElapsed:    2*time.Minute + 14*time.Second,
		CataractaeIndex: 2,
		TotalCataractae: 3,
	}
	m := dashboardTUIModel{width: 120}
	_, infoLine := m.tuiFlowGraphRow(ch)
	stripped := stripANSITest(infoLine)
	if !strings.Contains(stripped, "5m 30s") {
		t.Errorf("tuiFlowGraphRow info line should contain overall elapsed '5m 30s', got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "2m 14s") {
		t.Errorf("tuiFlowGraphRow info line should contain stage elapsed '2m 14s' when StageElapsed is set, got:\n%s", stripped)
	}
}

// TestTuiFlowGraphRow_StageElapsedZero_OmitsStageAge verifies that
// tuiFlowGraphRow does not show a stage age when StageElapsed is 0.
//
// Given: an aqueduct with Elapsed=5m 30s and StageElapsed=0
// When:  tuiFlowGraphRow is called
// Then:  the info line contains overall elapsed but no standalone "0s"
func TestTuiFlowGraphRow_StageElapsedZero_OmitsStageAge(t *testing.T) {
	ch := CataractaeInfo{
		Name:            "virgo",
		DropletID:       "ci-abc12",
		Step:            "review",
		Steps:           []string{"implement", "review", "qa"},
		Elapsed:         5*time.Minute + 30*time.Second,
		StageElapsed:    0,
		CataractaeIndex: 2,
		TotalCataractae: 3,
	}
	m := dashboardTUIModel{width: 120}
	_, infoLine := m.tuiFlowGraphRow(ch)
	stripped := stripANSITest(infoLine)
	if strings.Contains(stripped, " 0s ") {
		t.Errorf("tuiFlowGraphRow info line should not show standalone '0s' stage age when StageElapsed=0, got:\n%s", stripped)
	}
	if !strings.Contains(stripped, "5m 30s") {
		t.Errorf("tuiFlowGraphRow info line should contain overall elapsed '5m 30s', got:\n%s", stripped)
	}
}
