package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
)

// --- helpers ---

// tempDB creates a temporary SQLite database and returns its path and a cleanup func.
func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

// tempCfg writes a minimal cistern.yaml referencing an aqueduct.yaml stub.
// Returns the path to the config file.
func tempCfg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Minimal workflow YAML.
	wfContent := `name: test
cataractae:
  - name: implement
    type: agent
  - name: review
    type: agent
  - name: merge
    type: automated
`
	wfPath := filepath.Join(dir, "aqueduct.yaml")
	if err := os.WriteFile(wfPath, []byte(wfContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Config referencing two operators named "virgo" and "marcia".
	cfgContent := `repos:
  - name: myrepo
    url: https://example.com/repo
    workflow_path: aqueduct.yaml
    cataractae: 2
    names:
      - virgo
      - marcia
    prefix: mr
max_cataractae: 4
`
	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// --- TestFetchDashboardData_FeedsDataCorrectly ---

func TestFetchDashboardData_FeedsDataCorrectly(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	// Seed the queue with known items.
	c, err := cistern.New(dbPath, "mr")
	if err != nil {
		t.Fatal(err)
	}

	// Add: 1 flowing assigned to "virgo", 1 queued, 1 closed.
	flowing, _ := c.Add("myrepo", "Feature A", "", 1, 2)
	c.GetReady("myrepo") // marks it in_progress
	c.Assign(flowing.ID, "virgo", "implement")

	_, _ = c.Add("myrepo", "Feature B", "", 2, 2) // stays open/queued

	closed, _ := c.Add("myrepo", "Feature C", "", 1, 2)
	c.CloseItem(closed.ID)
	c.Close()

	data := fetchDashboardData(cfgPath, dbPath)

	if data.CataractaeCount != 2 {
		t.Errorf("CataractaeCount = %d, want 2", data.CataractaeCount)
	}
	if data.FlowingCount != 1 {
		t.Errorf("FlowingCount = %d, want 1", data.FlowingCount)
	}
	if data.QueuedCount != 1 {
		t.Errorf("QueuedCount = %d, want 1", data.QueuedCount)
	}
	if data.DoneCount != 1 {
		t.Errorf("DoneCount = %d, want 1", data.DoneCount)
	}
	if !data.FarmRunning {
		t.Error("FarmRunning should be true when queue is accessible")
	}
	if data.FetchedAt.IsZero() {
		t.Error("FetchedAt should be set")
	}

	// Cataracta "virgo" should be assigned to the flowing item.
	var virgo *CataractaeInfo
	for i := range data.Cataractae {
		if data.Cataractae[i].Name == "virgo" {
			virgo = &data.Cataractae[i]
		}
	}
	if virgo == nil {
		t.Fatal("cataractae virgo not found in data.Cataractae")
	}
	if virgo.DropletID != flowing.ID {
		t.Errorf("virgo.DropletID = %q, want %q", virgo.DropletID, flowing.ID)
	}
	if virgo.Step != "implement" {
		t.Errorf("virgo.Step = %q, want %q", virgo.Step, "implement")
	}
	if virgo.CataractaeIndex != 1 {
		t.Errorf("virgo.CataractaeIndex = %d, want 1", virgo.CataractaeIndex)
	}
	if virgo.TotalCataractae != 3 {
		t.Errorf("virgo.TotalCataractae = %d, want 3", virgo.TotalCataractae)
	}

	// Cataracta "marcia" should be dry.
	var marcia *CataractaeInfo
	for i := range data.Cataractae {
		if data.Cataractae[i].Name == "marcia" {
			marcia = &data.Cataractae[i]
		}
	}
	if marcia == nil {
		t.Fatal("cataractae marcia not found in data.Cataractae")
	}
	if marcia.DropletID != "" {
		t.Errorf("marcia.DropletID = %q, want empty (dry)", marcia.DropletID)
	}

	// Cistern should contain flowing + queued (2 items).
	if len(data.CisternItems) != 2 {
		t.Errorf("len(CisternItems) = %d, want 2", len(data.CisternItems))
	}

	// Recent should contain the 1 closed item.
	if len(data.RecentItems) != 1 {
		t.Errorf("len(RecentItems) = %d, want 1", len(data.RecentItems))
	}
}

// --- TestFetchDashboardData_FarmNotRunning_ShowsDroughtState ---

func TestFetchDashboardData_FarmNotRunning_ShowsDroughtState(t *testing.T) {
	t.Run("missing config returns empty data", func(t *testing.T) {
		data := fetchDashboardData("/nonexistent/cistern.yaml", "/nonexistent/cistern.db")

		if data == nil {
			t.Fatal("expected non-nil DashboardData even on error")
		}
		if data.FarmRunning {
			t.Error("FarmRunning should be false when config is missing")
		}
		if data.CataractaeCount != 0 {
			t.Errorf("CataractaeCount = %d, want 0", data.CataractaeCount)
		}
		if data.FetchedAt.IsZero() {
			t.Error("FetchedAt should always be set")
		}
	})

	t.Run("valid config but missing DB shows cataractae dry", func(t *testing.T) {
		cfgPath := tempCfg(t)
		dbPath := filepath.Join(t.TempDir(), "nonexistent.db")
		// Don't create the DB — remove it if it exists.
		os.Remove(dbPath)

		// cistern.New creates the DB if missing, so we can't test a truly missing DB
		// at the queue level without making the path unwritable. Instead, test
		// that a fresh empty DB yields all-dry cataractae and zero counts.
		data := fetchDashboardData(cfgPath, dbPath)

		if data.CataractaeCount != 2 {
			t.Errorf("CataractaeCount = %d, want 2 (from config)", data.CataractaeCount)
		}
		if data.FlowingCount != 0 {
			t.Errorf("FlowingCount = %d, want 0 for empty DB", data.FlowingCount)
		}
		if data.QueuedCount != 0 {
			t.Errorf("QueuedCount = %d, want 0 for empty DB", data.QueuedCount)
		}
		for _, ch := range data.Cataractae {
			if ch.DropletID != "" {
				t.Errorf("cataractae %q should be dry (empty DropletID), got %q", ch.Name, ch.DropletID)
			}
		}
	})
}

// --- TestDashboard_ExitsCleanlyOnQ ---

func TestDashboard_ExitsCleanlyOnQ(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	inputCh := make(chan byte, 1)
	var out bytes.Buffer

	// Send 'q' after a short delay so the dashboard renders at least once first.
	go func() {
		time.Sleep(50 * time.Millisecond)
		inputCh <- 'q'
	}()

	err := RunDashboard(cfgPath, dbPath, inputCh, &out)
	if err != nil {
		t.Errorf("RunDashboard returned error on q: %v", err)
	}

	// Dashboard should have rendered at least one frame.
	if out.Len() == 0 {
		t.Error("expected some output before exit")
	}
}

func TestDashboard_ExitsCleanlyOnCtrlC(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	inputCh := make(chan byte, 1)
	var out bytes.Buffer

	go func() {
		time.Sleep(50 * time.Millisecond)
		inputCh <- 3 // Ctrl-C byte
	}()

	err := RunDashboard(cfgPath, dbPath, inputCh, &out)
	if err != nil {
		t.Errorf("RunDashboard returned error on Ctrl-C: %v", err)
	}
}

func TestDashboard_ExitsWhenInputClosed(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	inputCh := make(chan byte)
	var out bytes.Buffer

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(inputCh)
	}()

	err := RunDashboard(cfgPath, dbPath, inputCh, &out)
	if err != nil {
		t.Errorf("RunDashboard returned error when channel closed: %v", err)
	}
}

// --- TestRenderDashboard ---

func TestRenderDashboard_ContainsExpectedSections(t *testing.T) {
	steps := []string{"implement", "review", "merge"}
	data := &DashboardData{
		CataractaeCount: 2,
		FlowingCount:   1,
		QueuedCount:    1,
		DoneCount:      3,
		Cataractae: []CataractaeInfo{
			{Name: "virgo", DropletID: "ci-abc12", Step: "implement", Steps: steps, CataractaeIndex: 1, TotalCataractae: 3, Elapsed: 2*time.Minute + 14*time.Second},
			{Name: "marcia", Steps: steps},
		},
		CisternItems: []*cistern.Droplet{
			{ID: "ci-abc12", Repo: "cistern", Status: "in_progress", CurrentCataractae: "implement", Complexity: 2},
		},
		RecentItems: []*cistern.Droplet{
			{ID: "ci-xyz99", Status: "delivered", CurrentCataractae: "merge", UpdatedAt: time.Now()},
		},
		FarmRunning: true,
		FetchedAt:   time.Date(2026, 3, 14, 15, 7, 42, 0, time.UTC),
	}

	out := renderDashboard(data)

	// Flow graph should contain step names and node symbols.
	for _, want := range []string{"implement", "review", "merge", "●", "○"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in flow graph", want)
		}
	}
	// Aqueduct names appear in the flow graph rows.
	if !strings.Contains(out, "virgo") {
		t.Error("output missing cataractae name virgo")
	}
	if !strings.Contains(out, "marcia") {
		t.Error("output missing cataractae name marcia")
	}
	// Cistern counts.
	if !strings.Contains(out, "flowing") {
		t.Error("output missing flowing count")
	}
	if !strings.Contains(out, "queued") {
		t.Error("output missing queued count")
	}
	// Recent flow section.
	if !strings.Contains(out, "RECENT FLOW") {
		t.Error("output missing RECENT FLOW section")
	}
	// Timestamp and footer.
	if !strings.Contains(out, "15:07:42") {
		t.Error("output missing last update timestamp")
	}
	if !strings.Contains(out, "q to quit") {
		t.Error("output missing footer hint")
	}
}

func TestRenderFlowGraphRow_ActiveStep(t *testing.T) {
	// Use step="review" (non-first step) to verify ● appears on the incoming edge,
	// i.e. before the active step name: implement ──●──▶ review ──○──▶ qa
	ch := CataractaeInfo{
		Name:            "virgo",
		DropletID:       "ci-s76ho",
		Step:            "review",
		Steps:           []string{"implement", "review", "qa"},
		Elapsed:         3*time.Minute + 12*time.Second,
		CataractaeIndex:  2,
		TotalCataractae: 3,
	}
	graphLine, infoLine := renderFlowGraphRow(ch)

	stripANSI := func(s string) string {
		var out strings.Builder
		inEsc := false
		for _, r := range s {
			if r == '\033' {
				inEsc = true
				continue
			}
			if inEsc {
				if r == 'm' {
					inEsc = false
				}
				continue
			}
			out.WriteRune(r)
		}
		return out.String()
	}

	cleanGraph := stripANSI(graphLine)

	// ● must appear before "review" in the graph (incoming edge semantics).
	bulletIdx := strings.Index(cleanGraph, "●")
	if bulletIdx < 0 {
		t.Fatal("graph line should contain filled node ● for active step")
	}
	reviewIdx := strings.Index(cleanGraph, "review")
	if reviewIdx < 0 {
		t.Fatal("graph line should contain the active step name 'review'")
	}
	if bulletIdx >= reviewIdx {
		t.Errorf("● (col %d) should appear before 'review' (col %d) in graph line", bulletIdx, reviewIdx)
	}

	if !strings.Contains(cleanGraph, "○") {
		t.Error("graph line should contain hollow node ○ for inactive steps")
	}
	if !strings.Contains(cleanGraph, "implement") {
		t.Error("graph line should contain preceding step name 'implement'")
	}
	if !strings.Contains(infoLine, "↑") {
		t.Error("info line should contain pointer ↑")
	}
	if !strings.Contains(infoLine, "virgo") {
		t.Error("info line should contain aqueduct name")
	}
	if !strings.Contains(infoLine, "ci-s76ho") {
		t.Error("info line should contain droplet ID")
	}
	if !strings.Contains(infoLine, "3m 12s") {
		t.Error("info line should contain elapsed time")
	}
}

func TestRenderFlowGraphRow_Idle(t *testing.T) {
	ch := CataractaeInfo{
		Name:  "marcia",
		Steps: []string{"implement", "review", "qa"},
	}
	graphLine, infoLine := renderFlowGraphRow(ch)

	if strings.Contains(graphLine, "●") {
		t.Error("idle graph line should not contain filled node ●")
	}
	if !strings.Contains(graphLine, "○") {
		t.Error("idle graph line should contain hollow nodes ○")
	}
	if infoLine != "" {
		t.Errorf("idle aqueduct should have no info line, got %q", infoLine)
	}
}

func TestRenderFlowGraphRow_PointerAligned(t *testing.T) {
	ch := CataractaeInfo{
		Name:            "virgo",
		DropletID:       "ci-abc",
		Step:            "review",
		Steps:           []string{"implement", "review", "qa"},
		CataractaeIndex:  2,
		TotalCataractae: 3,
	}
	graphLine, infoLine := renderFlowGraphRow(ch)

	// Strip ANSI escape codes to get visually clean strings.
	stripANSI := func(s string) string {
		var out strings.Builder
		inEsc := false
		for _, r := range s {
			if r == '\033' {
				inEsc = true
				continue
			}
			if inEsc {
				if r == 'm' {
					inEsc = false
				}
				continue
			}
			out.WriteRune(r)
		}
		return out.String()
	}

	// runeIndex returns the rune (visual) index of sub in s.
	// strings.Index returns byte offsets; multi-byte chars (─, ○, ●, ▶, ↑)
	// mean byte offset ≠ visual column — so we convert explicitly.
	runeIndex := func(s, sub string) int {
		byteIdx := strings.Index(s, sub)
		if byteIdx < 0 {
			return -1
		}
		return len([]rune(s[:byteIdx]))
	}

	cleanGraph := stripANSI(graphLine)
	cleanInfo := stripANSI(infoLine)

	// ● must appear BEFORE the active step name "review".
	bulletPos := runeIndex(cleanGraph, "●")
	if bulletPos < 0 {
		t.Fatal("no ● in graph line")
	}
	reviewPos := runeIndex(cleanGraph, "review")
	if reviewPos < 0 {
		t.Fatal("no 'review' in graph line")
	}
	if bulletPos >= reviewPos {
		t.Errorf("● at visual col %d should be before 'review' at col %d (incoming edge semantics)", bulletPos, reviewPos)
	}

	// ↑ in the info line should align with the start of the active step name "review".
	arrowPos := runeIndex(cleanInfo, "↑")
	if arrowPos < 0 {
		t.Fatal("no ↑ in info line")
	}
	if arrowPos != reviewPos {
		t.Errorf("↑ at visual col %d should align with 'review' at col %d in graph line", arrowPos, reviewPos)
	}
}

func TestRenderDashboard_AqueductsClosedWhenNoCataractae(t *testing.T) {
	data := &DashboardData{
		Cataractae:   []CataractaeInfo{},
		FetchedAt: time.Now(),
	}
	out := renderDashboard(data)
	if !strings.Contains(out, "No aqueducts configured") {
		t.Error("expected 'No aqueducts configured' when no channels configured")
	}
}



// --- TestProgressBar ---

func TestProgressBar_FilledAndEmpty(t *testing.T) {
	tests := []struct {
		step, total, width int
		wantFilled         int
	}{
		{1, 6, 6, 1},
		{3, 6, 6, 3},
		{6, 6, 6, 6},
		{0, 6, 6, 0},
		{1, 0, 6, 0}, // zero total → all empty
	}
	for _, tt := range tests {
		bar := progressBar(tt.step, tt.total, tt.width)
		if len([]rune(bar)) != tt.width {
			t.Errorf("progressBar(%d,%d,%d) length = %d, want %d",
				tt.step, tt.total, tt.width, len([]rune(bar)), tt.width)
		}
		filled := strings.Count(bar, "█")
		if filled != tt.wantFilled {
			t.Errorf("progressBar(%d,%d,%d) filled = %d, want %d",
				tt.step, tt.total, tt.width, filled, tt.wantFilled)
		}
	}
}

// --- TestStepIndexInWorkflow ---

func TestStepIndexInWorkflow_ReturnsCorrectIndex(t *testing.T) {
	steps := []aqueduct.WorkflowCataractae{
		{Name: "implement"},
		{Name: "review"},
		{Name: "merge"},
	}

	if idx := cataractaeIndexInWorkflow("implement", steps); idx != 1 {
		t.Errorf("stepIndex(implement) = %d, want 1", idx)
	}
	if idx := cataractaeIndexInWorkflow("merge", steps); idx != 3 {
		t.Errorf("stepIndex(merge) = %d, want 3", idx)
	}
	if idx := cataractaeIndexInWorkflow("unknown", steps); idx != 0 {
		t.Errorf("stepIndex(unknown) = %d, want 0", idx)
	}
}

// --- TestFormatElapsed ---

func TestFormatElapsed_Seconds(t *testing.T) {
	got := formatElapsed(45 * time.Second)
	if got != "45s" {
		t.Errorf("formatElapsed(45s) = %q, want %q", got, "45s")
	}
}

func TestFormatElapsed_MinutesAndSeconds(t *testing.T) {
	got := formatElapsed(2*time.Minute + 14*time.Second)
	if got != "2m 14s" {
		t.Errorf("formatElapsed(2m14s) = %q, want %q", got, "2m 14s")
	}
}

// --- TestTuiAqueductRow — pillar template ---

// stripANSITest removes ANSI escape sequences from s, returning plain text.
func stripANSITest(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// TestTuiAqueductRow_LineCount verifies tuiAqueductRow returns exactly 12 lines:
// 2 channel rows + 9 pillar rows + 1 label row.
func TestTuiAqueductRow_LineCount(t *testing.T) {
	m := newDashboardTUIModel("", "")
	tests := []struct {
		name   string
		nSteps int
	}{
		{"one step", 1},
		{"two steps", 2},
		{"three steps", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([]string, tt.nSteps)
			for i := range steps {
				steps[i] = fmt.Sprintf("step%d", i+1)
			}
			ch := CataractaeInfo{Name: "virgo", Steps: steps}
			lines := m.tuiAqueductRow(ch, 0)
			const wantLines = 12 // 2 channel + 9 pillar + 1 label
			if len(lines) != wantLines {
				t.Errorf("tuiAqueductRow() returned %d lines, want %d", len(lines), wantLines)
			}
		})
	}
}

// TestTuiAqueductRow_CrownRowIsSolidBlocks verifies that the arch crown row
// (pillar row 5 = result[2]) contains N×28 ▒ characters in the pillar section.
// This fails with the old brick arch (which used ▀/█/▌) and passes with the
// new durdraw pillar template.
func TestTuiAqueductRow_CrownRowIsSolidBlocks(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{Name: "virgo", Steps: steps}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 3 {
		t.Fatalf("not enough lines: got %d", len(lines))
	}

	// result[2] = archLines[0] = pillar row 5 (full-width crown, first rendered row).
	// After stripping ANSI and prefix, the first N*28 runes must all be ▒.
	crownLine := stripANSITest(lines[2])
	runes := []rune(crownLine)

	// Prefix visual width = "  " + nameW(10) + "  " = 14 chars.
	const prefixLen = 14
	const pillarW = 28
	want := len(steps) * pillarW

	if len(runes) < prefixLen+want {
		t.Fatalf("crown row too short: %d runes, need at least %d", len(runes), prefixLen+want)
	}
	for i := 0; i < want; i++ {
		if runes[prefixLen+i] != '▒' {
			t.Errorf("crown row pillar[%d] = %q, want '▒'", i, runes[prefixLen+i])
			break
		}
	}
}

// TestTuiAqueductRow_PierBodyRowHasCorrectStructure verifies a pier body row
// (pillar rows 9–13 = result[11..15]) has the expected 12sp+░+4▒+11sp structure
// for each pillar, for a single-step aqueduct.
func TestTuiAqueductRow_PierBodyRowHasCorrectStructure(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement"}
	ch := CataractaeInfo{Name: "virgo", Steps: steps}
	lines := m.tuiAqueductRow(ch, 0)

	// result[6] = archLines[4] = pillar row 9 (first pier body row).
	// After stripping ANSI and prefix: 12 spaces + ░ + 4 ▒ + 11 spaces (+ waterfall).
	pierLine := stripANSITest(lines[6])
	runes := []rune(pierLine)

	const prefixLen = 14
	const pillarW = 28
	if len(runes) < prefixLen+pillarW {
		t.Fatalf("pier row too short: %d runes, need at least %d", len(runes), prefixLen+pillarW)
	}
	content := runes[prefixLen : prefixLen+pillarW]

	// Verify structure: 12 spaces, then ░, then 4 ▒, then 11 spaces.
	for i := 0; i < 12; i++ {
		if content[i] != ' ' {
			t.Errorf("pier row content[%d] = %q, want ' '", i, content[i])
			break
		}
	}
	if content[12] != '░' {
		t.Errorf("pier row content[12] = %q, want '░'", content[12])
	}
	for i := 13; i < 17; i++ {
		if content[i] != '▒' {
			t.Errorf("pier row content[%d] = %q, want '▒'", i, content[i])
			break
		}
	}
	for i := 17; i < 28; i++ {
		if content[i] != ' ' {
			t.Errorf("pier row content[%d] = %q, want ' '", i, content[i])
			break
		}
	}
}

// TestTuiAqueductRow_ActiveStepHasDifferentCrownColor verifies that the active
// step pillar uses a different ANSI color for ▒ in the crown row than idle pillars.
// Forces TrueColor rendering so lipgloss emits ANSI escape codes in the test context.
func TestTuiAqueductRow_ActiveStepHasDifferentCrownColor(t *testing.T) {
	// Force TrueColor so lipgloss emits ANSI escape sequences; restore after.
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(orig) })

	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}

	active := m.tuiAqueductRow(CataractaeInfo{
		Name:      "virgo",
		DropletID: "ci-abc12",
		Step:      "review",
		Steps:     steps,
	}, 0)

	idle := m.tuiAqueductRow(CataractaeInfo{
		Name:  "virgo",
		Steps: steps,
	}, 0)

	// Crown row is result[2]. Active version must differ from idle (different color escape).
	if active[2] == idle[2] {
		t.Error("active crown row should have different ANSI color than idle crown row")
	}

	// Both versions must have the same plain-text content (same ▒ chars, just different colors).
	if stripANSITest(active[2]) != stripANSITest(idle[2]) {
		t.Errorf("active and idle crown rows should have same plain text\nactive: %q\nidle:   %q",
			stripANSITest(active[2]), stripANSITest(idle[2]))
	}
}
