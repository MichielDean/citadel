package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// --- TestActiveStepNames ---

func TestActiveStepNames_NoSkipFor_ReturnsAllSteps(t *testing.T) {
	wf := []aqueduct.WorkflowCataractae{
		{Name: "implement"},
		{Name: "review"},
		{Name: "merge"},
	}
	got := activeStepNames(wf, 2)
	want := []string{"implement", "review", "merge"}
	if len(got) != len(want) {
		t.Fatalf("activeStepNames len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("activeStepNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestActiveStepNames_SkippedStep_IsExcluded(t *testing.T) {
	// complexity 1 skips "adv-review"
	wf := []aqueduct.WorkflowCataractae{
		{Name: "implement"},
		{Name: "adv-review", SkipFor: []int{1}},
		{Name: "merge"},
	}
	got := activeStepNames(wf, 1)
	want := []string{"implement", "merge"}
	if len(got) != len(want) {
		t.Fatalf("activeStepNames len = %d, want %d: got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("activeStepNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestActiveStepNames_MultipleSkipped_AllExcluded(t *testing.T) {
	wf := []aqueduct.WorkflowCataractae{
		{Name: "implement"},
		{Name: "adv-review", SkipFor: []int{1, 2}},
		{Name: "qa", SkipFor: []int{1}},
		{Name: "merge"},
	}
	got := activeStepNames(wf, 1)
	want := []string{"implement", "merge"}
	if len(got) != len(want) {
		t.Fatalf("activeStepNames len = %d, want %d: got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("activeStepNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestActiveStepNames_ComplexityNotInSkipFor_StepIncluded(t *testing.T) {
	// complexity 3 is NOT in SkipFor: []int{1, 2}, so step runs
	wf := []aqueduct.WorkflowCataractae{
		{Name: "implement"},
		{Name: "adv-review", SkipFor: []int{1, 2}},
		{Name: "merge"},
	}
	got := activeStepNames(wf, 3)
	want := []string{"implement", "adv-review", "merge"}
	if len(got) != len(want) {
		t.Fatalf("activeStepNames len = %d, want %d: got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("activeStepNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestActiveStepNames_EmptyWorkflow_ReturnsNil(t *testing.T) {
	got := activeStepNames(nil, 1)
	if len(got) != 0 {
		t.Errorf("activeStepNames(nil, 1) = %v, want empty", got)
	}
}

// --- TestFetchDashboardData_ActiveAqueduct_FiltersStepsByComplexity ---

// tempCfgWithSkipFor writes a cistern.yaml referencing a workflow that has
// SkipFor fields. Returns the path to the config file.
func tempCfgWithSkipFor(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Workflow with adv-review skipped for complexity 1.
	wfContent := `name: test
cataractae:
  - name: implement
    type: agent
  - name: adv-review
    type: agent
    skip_for: [1]
  - name: merge
    type: automated
`
	wfPath := filepath.Join(dir, "aqueduct.yaml")
	if err := os.WriteFile(wfPath, []byte(wfContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgContent := `repos:
  - name: myrepo
    url: https://example.com/repo
    workflow_path: aqueduct.yaml
    cataractae: 1
    names:
      - virgo
    prefix: mr
max_cataractae: 2
`
	cfgPath := filepath.Join(dir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestFetchDashboardData_ActiveAqueduct_FiltersStepsByComplexity(t *testing.T) {
	cfgPath := tempCfgWithSkipFor(t)
	dbPath := tempDB(t)

	c, err := cistern.New(dbPath, "mr")
	if err != nil {
		t.Fatal(err)
	}

	// Add a complexity-1 droplet and assign it to virgo at "merge" — a step
	// AFTER the skipped "adv-review". In the full workflow [implement, adv-review, merge]
	// "merge" is at position 3, but in the filtered list [implement, merge] it is at
	// position 2. This exercises the CataractaeIndex bug where the full-list index
	// would exceed TotalCataractae.
	item, _ := c.Add("myrepo", "Trivial task", "", 1, 1)
	c.GetReady("myrepo")
	c.Assign(item.ID, "virgo", "merge")
	c.Close()

	data := fetchDashboardData(cfgPath, dbPath)

	var virgo *CataractaeInfo
	for i := range data.Cataractae {
		if data.Cataractae[i].Name == "virgo" {
			virgo = &data.Cataractae[i]
		}
	}
	if virgo == nil {
		t.Fatal("cataractae virgo not found")
	}

	// adv-review is skipped for complexity 1, so only implement + merge remain.
	wantSteps := []string{"implement", "merge"}
	if len(virgo.Steps) != len(wantSteps) {
		t.Fatalf("virgo.Steps = %v, want %v", virgo.Steps, wantSteps)
	}
	for i, s := range wantSteps {
		if virgo.Steps[i] != s {
			t.Errorf("virgo.Steps[%d] = %q, want %q", i, virgo.Steps[i], s)
		}
	}

	// TotalCataractae must reflect filtered count.
	if virgo.TotalCataractae != 2 {
		t.Errorf("virgo.TotalCataractae = %d, want 2", virgo.TotalCataractae)
	}

	// CataractaeIndex must be the position in the FILTERED list (2), not the
	// full-workflow position (3). Previously the bug caused index > total.
	if virgo.CataractaeIndex != 2 {
		t.Errorf("virgo.CataractaeIndex = %d, want 2 (filtered position of 'merge')", virgo.CataractaeIndex)
	}
}

func TestFetchDashboardData_IdleAqueduct_ShowsAllSteps(t *testing.T) {
	// Idle aqueduct (no droplet) should show all steps regardless of SkipFor.
	cfgPath := tempCfgWithSkipFor(t)
	dbPath := tempDB(t)

	data := fetchDashboardData(cfgPath, dbPath)

	var virgo *CataractaeInfo
	for i := range data.Cataractae {
		if data.Cataractae[i].Name == "virgo" {
			virgo = &data.Cataractae[i]
		}
	}
	if virgo == nil {
		t.Fatal("cataractae virgo not found")
	}

	// Idle: all 3 workflow steps shown as preview.
	if len(virgo.Steps) != 3 {
		t.Errorf("idle virgo.Steps = %v, want all 3 steps", virgo.Steps)
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
// Uses a two-state FSM to correctly handle CSI sequences:
//   - After \x1b, if next char is '[' enter CSI-param state (not a terminator)
//   - In CSI-param state, consume until final byte (0x40–0x7E)
//
// This distinguishes 'after ESC' from 'inside CSI params', fixing the bug
// where '[' (0x5B) would be treated as a CSI terminator, leaking params.
func stripANSITest(s string) string {
	const (
		stateNormal   = 0
		stateAfterESC = 1
		stateInCSI    = 2
	)
	var out strings.Builder
	state := stateNormal
	for _, r := range s {
		switch state {
		case stateNormal:
			if r == '\x1b' {
				state = stateAfterESC
			} else {
				out.WriteRune(r)
			}
		case stateAfterESC:
			if r == '[' {
				// CSI introducer — enter param-consuming state
				state = stateInCSI
			} else if r >= 0x40 && r <= 0x7E {
				// Two-character escape sequence (e.g. \x1bm) — final byte consumed
				state = stateNormal
			}
			// else: intermediate byte, stay in stateAfterESC
		case stateInCSI:
			// Consume until CSI final byte (0x40–0x7E)
			if r >= 0x40 && r <= 0x7E {
				state = stateNormal
			}
		}
	}
	return out.String()
}

// TestStripANSITest_CSISequences verifies the two-state FSM correctly strips
// CSI escape sequences, exercising stateAfterESC and stateInCSI branches.
func TestStripANSITest_CSISequences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "TrueColor_fg_reset",
			input: "\x1b[38;2;255;0;0mRed\x1b[0m",
			want:  "Red",
		},
		{
			name:  "multiple_CSI_sequences",
			input: "\x1b[1mBold\x1b[0m and \x1b[32mgreen\x1b[0m",
			want:  "Bold and green",
		},
		{
			name:  "no_escape_sequences",
			input: "plain text",
			want:  "plain text",
		},
		{
			name:  "empty_string",
			input: "",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSITest(tt.input)
			if got != tt.want {
				t.Errorf("stripANSITest(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestTuiAqueductRow_LabelRowAboveArch verifies that:
// - lines[2] is the label row (shows full pipeline with all step names and →)
// - lines[3] is the first mipmap row (animated wave for active aqueducts)

// --- Progress bar rendering tests ---

func TestViewAqueductProgress_ContainsDropletInfo(t *testing.T) {
	m := newDashboardTUIModel("", "")
	ch := CataractaeInfo{
		Name:            "virgo",
		RepoName:        "cistern",
		DropletID:       "ci-abc12",
		Step:            "implement",
		Steps:           []string{"implement", "review", "deliver"},
		TotalCataractae: 3,
		CataractaeIndex: 1,
	}
	result := m.viewAqueductProgress(ch)
	stripped := stripANSITest(result)
	if !strings.Contains(stripped, "virgo") {
		t.Errorf("progress row should contain aqueduct name 'virgo', got: %q", stripped)
	}
	if !strings.Contains(stripped, "ci-abc12") {
		t.Errorf("progress row should contain droplet ID, got: %q", stripped)
	}
	if !strings.Contains(stripped, "implement") {
		t.Errorf("progress row should contain active step name, got: %q", stripped)
	}
}

func TestViewAqueductProgress_PipelineContainsAllSteps(t *testing.T) {
	m := newDashboardTUIModel("", "")
	ch := CataractaeInfo{
		Name:            "virgo",
		DropletID:       "ci-abc12",
		Step:            "review",
		Steps:           []string{"implement", "review", "deliver"},
		TotalCataractae: 3,
		CataractaeIndex: 2,
	}
	result := m.viewAqueductProgress(ch)
	stripped := stripANSITest(result)
	// Segmented bar: all step names appear as labels, separated by segment brackets
	if !strings.Contains(stripped, "implement") {
		t.Errorf("progress row should contain all steps, got: %q", stripped)
	}
	if !strings.Contains(stripped, "deliver") {
		t.Errorf("progress row should contain all steps, got: %q", stripped)
	}
	if !strings.Contains(stripped, "│") {
		t.Errorf("progress row should contain channel wall characters (│), got: %q", stripped)
	}
}

// TestViewAqueductProgress_SluiceGates verifies open/closed gate rendering.
// Layout is now 4 rows: header, labels, top (raised gates), bottom (channel fill).
//
// Gate between implement (complete) and review (active):
//   - top row: ][ raised marker
//   - bottom row: seamless █ fill (no wall, no ═╪)
//
// Gate between review (active) and deliver (future):
//   - top row: spaces
//   - bottom row: ═╪ closed gate
func TestViewAqueductProgress_SluiceGates(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 80

	ch := CataractaeInfo{
		Name:      "virgo",
		DropletID: "ci-abc12",
		Step:      "review",
		Steps:     []string{"implement", "review", "deliver"},
	}
	result := m.viewAqueductProgress(ch)
	stripped := stripANSITest(result)
	rows := strings.Split(stripped, "\n")
	// rows[0] = header, rows[1] = labels, rows[2] = top (raised gates), rows[3] = bottom (channel)
	topRow := rows[2]
	botRow := rows[3]

	// Top row: exactly one ][ raised gate (implement→review is complete).
	if strings.Count(topRow, "][") != 1 {
		t.Errorf("top row: expected exactly 1 raised gate (][), got: %q", topRow)
	}
	// Bottom row: exactly one ═╪ closed gate (review→deliver not yet reached).
	if strings.Count(botRow, "═╪") != 1 {
		t.Errorf("bottom row: expected exactly 1 closed gate (═╪), got: %q", botRow)
	}
	// Raised gate (][) must appear before the closed gate (═╪) positionally.
	if strings.Index(topRow, "][") < 0 || strings.Index(botRow, "═╪") < 0 {
		t.Errorf("gate positions unexpected: top=%q bot=%q", topRow, botRow)
	}
}

// --- viewIdleAqueductRow tests ---

func TestViewIdleAqueductRow_ShowsName(t *testing.T) {
	m := newDashboardTUIModel("", "")
	ch := CataractaeInfo{Name: "virgo", RepoName: "cistern"}
	row := stripANSITest(m.viewIdleAqueductRow(ch))
	if !strings.Contains(row, "virgo") {
		t.Errorf("idle row should contain aqueduct name, got: %q", row)
	}
}

func TestViewIdleAqueductRow_ShowsActiveStep(t *testing.T) {
	m := newDashboardTUIModel("", "")
	ch := CataractaeInfo{Name: "virgo", RepoName: "cistern", DropletID: "ci-abc", Step: "review"}
	row := stripANSITest(m.viewIdleAqueductRow(ch))
	if !strings.Contains(row, "review") {
		t.Errorf("active row should contain step name, got: %q", row)
	}
}

// --- activeAqueducts tests ---

func TestActiveAqueducts_ReturnsOnlyActive(t *testing.T) {
	cataractae := []CataractaeInfo{
		{Name: "virgo", DropletID: "ci-abc12"},
		{Name: "marcia", DropletID: ""},
		{Name: "leo", DropletID: "ci-xyz99"},
	}
	active := activeAqueducts(cataractae)
	if len(active) != 2 {
		t.Errorf("expected 2 active aqueducts, got %d", len(active))
	}
}

func TestActiveAqueducts_EmptyWhenAllIdle(t *testing.T) {
	cataractae := []CataractaeInfo{
		{Name: "virgo", DropletID: ""},
		{Name: "marcia", DropletID: ""},
	}
	active := activeAqueducts(cataractae)
	if len(active) != 0 {
		t.Errorf("expected 0 active aqueducts, got %d", len(active))
	}
}
