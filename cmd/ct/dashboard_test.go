package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// TestTuiAqueductRow_LabelRowAboveArch verifies that:
// - lines[2] is the label row (contains all step names)
// - lines[3] is the channel top row (▀ characters)
// - lines[4] is the channel water row (█ walls)
func TestTuiAqueductRow_LabelRowAboveArch(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:      "virgo",
		DropletID: "ci-abc12",
		Step:      "review",
		Steps:     steps,
	}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 5 {
		t.Fatalf("expected at least 5 lines, got %d", len(lines))
	}

	// lines[2] is the label row — must contain all step names.
	labelRow := stripANSITest(lines[2])
	for _, step := range steps {
		if !strings.Contains(labelRow, step) {
			t.Errorf("label row %q should contain step name %q", labelRow, step)
		}
	}

	// lines[3] is the channel top row (▀ characters).
	chanTop := stripANSITest(lines[3])
	if !strings.Contains(chanTop, "▀") {
		t.Errorf("lines[3] should be the channel top row (▀), got %q", chanTop)
	}

	// lines[4] is the channel water row (█ walls).
	chanWater := stripANSITest(lines[4])
	if !strings.Contains(chanWater, "█") {
		t.Errorf("lines[4] should be the channel water row (█ walls), got %q", chanWater)
	}

	// lines[4] must NOT contain ▀ (that belongs to the channel top, not water row).
	if strings.Contains(chanWater, "▀") {
		t.Errorf("channel water row (lines[4]) should not contain ▀: %q", chanWater)
	}
}

// TestTuiAqueductRow_WaterFillsOnlyToActiveStep verifies that the channel water row
// contains wave characters only up to and including the active step's column,
// and the remaining columns are empty (dry channel).
// It also verifies that the row never overflows its expected visual width even
// when the info string is longer than the wet section (edge case: first step active).
func TestTuiAqueductRow_WaterFillsOnlyToActiveStep(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}

	const (
		prefixLen = 14
		pillarW   = 28
		nSteps    = 3
		chanW     = nSteps * pillarW
		innerW    = chanW - 2
	)
	// Both cases are mid-pipeline (not the last step), so wfExit is absent.
	const expectedRowLen = prefixLen + 1 + innerW + 1 // 98

	cases := []struct {
		name            string
		step            string
		activeIdx       int
		cataractaeIndex int
		elapsed         time.Duration
	}{
		{
			// activeIdx=0: wetInnerW=(1*28-1)=27, infoStr with "1m 0s" is 29 chars —
			// exercises the truncation path in buildChanWater.
			name:            "first step active (activeIdx=0)",
			step:            "implement",
			activeIdx:       0,
			cataractaeIndex: 1,
			elapsed:         60 * time.Second,
		},
		{
			// activeIdx=1: wetInnerW=(2*28-1)=55, dryInnerW=27.
			name:            "second step active (activeIdx=1)",
			step:            "review",
			activeIdx:       1,
			cataractaeIndex: 2,
			elapsed:         0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := CataractaeInfo{
				Name:            "virgo",
				DropletID:       "ci-abc12",
				Step:            tc.step,
				Steps:           steps,
				CataractaeIndex: tc.cataractaeIndex,
				TotalCataractae: nSteps,
				Elapsed:         tc.elapsed,
			}
			lines := m.tuiAqueductRow(ch, 0)

			// lines[4] is the channel water row (name[0], info[1], label[2], chan-top[3]).
			chanWater := stripANSITest(lines[4])
			runes := []rune(chanWater)

			// Row must not overflow — catch buildChanWater returning more than wetInnerW chars.
			if len(runes) != expectedRowLen {
				t.Fatalf("channel water row visual width: got %d, want %d\nrow: %q",
					len(runes), expectedRowLen, chanWater)
			}

			// Visual layout (no waterfall for mid-pipeline steps):
			//   prefix(14) + "█"(1) + inner(82) + "█"(1)
			//   wetInnerW = (activeIdx+1)*pillarW - 1  (off-by-one corrected)
			wetInnerW := (tc.activeIdx+1)*pillarW - 1
			dryInnerW := innerW - wetInnerW
			innerStart := prefixLen + 1 // skip prefix and left wall
			dryStart   := innerStart + wetInnerW

			// The dry portion must be all spaces.
			for i := dryStart; i < dryStart+dryInnerW; i++ {
				if runes[i] != ' ' {
					t.Errorf("dry channel at rune %d should be ' ', got %q\nrow: %q",
						i, runes[i], chanWater)
					break
				}
			}

			// The wet portion must contain non-space content (wave chars or info text).
			wetSection := string(runes[innerStart : innerStart+wetInnerW])
			if !strings.ContainsAny(wetSection, "░▒▓≈") {
				t.Errorf("wet channel portion should contain wave characters (░▒▓≈)\nwet section: %q", wetSection)
			}
		})
	}
}

// TestTuiAqueductRow_IdleAqueductHasNoWater verifies that an idle aqueduct (no active
// droplet) renders the channel water row as a fully dry, empty channel (all spaces).
func TestTuiAqueductRow_IdleAqueductHasNoWater(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}

	// Idle: no DropletID
	ch := CataractaeInfo{
		Name:  "virgo",
		Steps: steps,
	}
	lines := m.tuiAqueductRow(ch, 0)

	// lines[4] is the channel water row (name[0], info[1], label[2], chan-top[3]).
	chanWater := stripANSITest(lines[4])
	runes := []rune(chanWater)

	const (
		prefixLen = 14
		pillarW   = 28
		chanW     = 3 * pillarW
		innerW    = chanW - 2
	)
	innerStart := prefixLen + 1
	innerEnd   := innerStart + innerW

	if len(runes) < innerEnd {
		t.Fatalf("channel water row too short: got %d runes, need at least %d", len(runes), innerEnd)
	}

	// Entire inner channel must be spaces (no wave animation for idle).
	for i := innerStart; i < innerEnd; i++ {
		if runes[i] != ' ' {
			t.Errorf("idle channel at rune %d should be ' ', got %q\nrow: %q",
				i, runes[i], chanWater)
			break
		}
	}
}

// TestTuiAqueductRow_LineCount verifies tuiAqueductRow returns exactly 14 lines:
// 1 name row + 1 info row + 1 label row + 2 channel rows + 9 pillar rows.
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
			const wantLines = 14 // 1 name + 1 info + 1 label + 2 channel + 9 pillar
			if len(lines) != wantLines {
				t.Errorf("tuiAqueductRow() returned %d lines, want %d", len(lines), wantLines)
			}
		})
	}
}

// TestTuiAqueductRow_CrownRowIsSolidBlocks verifies that the arch crown row
// (pillar row 5 = result[5]) contains N×28 ▒ characters in the pillar section.
// This fails with the old brick arch (which used ▀/█/▌) and passes with the
// new durdraw pillar template.
func TestTuiAqueductRow_CrownRowIsSolidBlocks(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{Name: "virgo", Steps: steps}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 6 {
		t.Fatalf("not enough lines: got %d", len(lines))
	}

	// result[5] = archLines[0] = pillar row 5 (full-width crown, first rendered row).
	// [0]=name, [1]=info, [2]=label, [3]=channel top, [4]=channel water, [5]=first arch row.
	// After stripping ANSI and prefix, the first N*28 runes must all be ▒.
	crownLine := stripANSITest(lines[5])
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
// (pillar rows 9–13 = result[13..17]) has the expected 12sp+░+4▒+11sp structure
// for each pillar, for a single-step aqueduct.
func TestTuiAqueductRow_PierBodyRowHasCorrectStructure(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement"}
	ch := CataractaeInfo{Name: "virgo", Steps: steps}
	lines := m.tuiAqueductRow(ch, 0)

	// result[9] = archLines[4] = pillar row 9 (first pier body row).
	// [0]=name, [1]=info, [2]=label, [3]=chan top, [4]=chan water, [5..8]=arch rows 5-8, [9]=arch row 9.
	// After stripping ANSI and prefix: 12 spaces + ░ + 4 ▒ + 11 spaces (+ waterfall).
	pierLine := stripANSITest(lines[9])
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

// --- TestViewDroughtArch — drought display ---

// TestViewDroughtArch_LineCount verifies viewDroughtArch returns exactly 15 lines:
// 1 drought label + 14 pillar rows (no channel water, no waterfall, no step labels).
func TestViewDroughtArch_LineCount(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 80
	lines := m.viewDroughtArch()
	const wantLines = 15 // 1 label + 14 pillar rows
	if len(lines) != wantLines {
		t.Errorf("viewDroughtArch() returned %d lines, want %d", len(lines), wantLines)
	}
}

// TestViewDroughtArch_LabelContainsDrought verifies the first line contains "drought".
func TestViewDroughtArch_LabelContainsDrought(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 80
	lines := m.viewDroughtArch()
	if !strings.Contains(stripANSITest(lines[0]), "drought") {
		t.Errorf("first line should contain 'drought', got %q", lines[0])
	}
}

// TestViewDroughtArch_CrownRowContainsBlocks verifies pillar row 5 (lines[6])
// contains exactly 28 consecutive ▒ characters.
func TestViewDroughtArch_CrownRowContainsBlocks(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 80
	lines := m.viewDroughtArch()

	if len(lines) < 7 {
		t.Fatalf("not enough lines: got %d", len(lines))
	}

	// lines[0] = drought label; lines[1..14] = pillar rows 0..13.
	// Pillar row 5 (crown) = lines[6].
	crownLine := stripANSITest(lines[6])

	const pillarW = 28
	if want := strings.Repeat("▒", pillarW); !strings.Contains(crownLine, want) {
		t.Errorf("drought crown row should contain %d consecutive ▒ chars, got %q", pillarW, crownLine)
	}
}

// TestViewDroughtArch_PierBodyRowHasCorrectStructure verifies a pier body row
// (lines[10], corresponding to pillar row 9) has the expected 12sp+░+4▒+11sp pattern.
func TestViewDroughtArch_PierBodyRowHasCorrectStructure(t *testing.T) {
	const termWidth = 80
	const pillarW   = 28
	const leftPad   = (termWidth - pillarW) / 2 // = 26

	m := newDashboardTUIModel("", "")
	m.width = termWidth
	lines := m.viewDroughtArch()

	// lines[0] = drought label, lines[1..14] = pillar rows 0..13.
	// Pillar row 9 (first pier body) = lines[10].
	pierLine := stripANSITest(lines[10])
	runes := []rune(pierLine)

	if len(runes) < leftPad+pillarW {
		t.Fatalf("pier row too short: %d runes, need at least %d", len(runes), leftPad+pillarW)
	}
	content := runes[leftPad : leftPad+pillarW]

	// Expected structure: 12 spaces + ░ + 4 ▒ + 11 spaces.
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

// TestViewAqueductArches_DroughtState_ShowsDroughtLabel verifies that when all
// aqueducts are idle, viewAqueductArches shows the drought display (not compact idle rows).
func TestViewAqueductArches_DroughtState_ShowsDroughtLabel(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 80
	steps := []string{"implement", "review", "merge"}
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{Name: "virgo", Steps: steps},
			{Name: "marcia", Steps: steps},
		},
	}

	lines := m.viewAqueductArches()
	if len(lines) == 0 {
		t.Fatal("viewAqueductArches() returned no lines in drought state")
	}
	allText := strings.Join(lines, "\n")
	cleanText := stripANSITest(allText)

	// Must show "drought" label.
	if !strings.Contains(cleanText, "drought") {
		t.Error("drought state should display 'drought' label")
	}
	// Must NOT show individual aqueduct names (no name prefix in drought arch).
	if strings.Contains(cleanText, "virgo") {
		t.Error("drought display should not contain individual aqueduct name 'virgo'")
	}
	// Must NOT show old compact idle row format.
	if strings.Contains(cleanText, "idle") {
		t.Error("drought display should not contain 'idle' text from compact row format")
	}
}

// TestViewAqueductArches_ActiveAqueductDoesNotShowDrought verifies that when at
// least one aqueduct is active, the normal arch + compact idle display is used.
func TestViewAqueductArches_ActiveAqueductDoesNotShowDrought(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.width = 80
	steps := []string{"implement", "review", "merge"}
	m.data = &DashboardData{
		Cataractae: []CataractaeInfo{
			{Name: "virgo", DropletID: "ci-abc12", Step: "implement", Steps: steps},
			{Name: "marcia", Steps: steps},
		},
	}

	lines := m.viewAqueductArches()
	allText := strings.Join(lines, "\n")
	cleanText := stripANSITest(allText)

	if strings.Contains(cleanText, "drought") {
		t.Error("active state should not show drought display")
	}
	if !strings.Contains(cleanText, "marcia") {
		t.Error("idle aqueduct 'marcia' should appear in compact idle row")
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

	// Crown row is result[5] (after name[0], info[1], label[2], chan-top[3], chan-water[4]).
	// Active version must differ from idle (different color escape).
	if active[5] == idle[5] {
		t.Error("active crown row should have different ANSI color than idle crown row")
	}

	// Both versions must have the same plain-text content (same ▒ chars, just different colors).
	if stripANSITest(active[5]) != stripANSITest(idle[5]) {
		t.Errorf("active and idle crown rows should have same plain text\nactive: %q\nidle:   %q",
			stripANSITest(active[5]), stripANSITest(idle[5]))
	}
}

// TestTuiAqueductRow_NameLineShowsAqueductAndRepo verifies that lines[0] contains
// both the aqueduct name and the repo name.
func TestTuiAqueductRow_NameLineShowsAqueductAndRepo(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:      "virgo",
		RepoName:  "cistern",
		DropletID: "ci-abc12",
		Step:      "review",
		Steps:     steps,
	}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	nameLine := stripANSITest(lines[0])
	if !strings.Contains(nameLine, "virgo") {
		t.Errorf("name line should contain aqueduct name 'virgo', got %q", nameLine)
	}
	if !strings.Contains(nameLine, "cistern") {
		t.Errorf("name line should contain repo name 'cistern', got %q", nameLine)
	}
}

// TestTuiAqueductRow_InfoLineShowsDropletInfo_WhenActive verifies that lines[1]
// contains the droplet ID and elapsed time for active aqueducts, but no progress bar.
func TestTuiAqueductRow_InfoLineShowsDropletInfo_WhenActive(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:            "virgo",
		RepoName:        "cistern",
		DropletID:       "ci-abc12",
		Step:            "review",
		Steps:           steps,
		CataractaeIndex:  2,
		TotalCataractae: 3,
		Elapsed:         3*time.Minute + 7*time.Second,
	}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	infoLine := stripANSITest(lines[1])
	if !strings.Contains(infoLine, "ci-abc12") {
		t.Errorf("info line should contain droplet ID 'ci-abc12', got %q", infoLine)
	}
	if !strings.Contains(infoLine, "3m 7s") {
		t.Errorf("info line should contain elapsed '3m 7s', got %q", infoLine)
	}
	if strings.ContainsAny(infoLine, "░█") {
		t.Errorf("info line must not contain progress bar chars (░ or █), got %q", infoLine)
	}
}

// TestTuiAqueductRow_InfoLineEmpty_WhenIdle verifies that lines[1] is empty
// (no droplet info) for idle aqueducts.
func TestTuiAqueductRow_InfoLineEmpty_WhenIdle(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:  "virgo",
		Steps: steps,
	}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	infoLine := stripANSITest(lines[1])
	if strings.TrimSpace(infoLine) != "" {
		t.Errorf("idle info line should be empty (all spaces), got %q", infoLine)
	}
}

// TestTuiAqueductRow_WaterChannelPureWave_NoDropletInfo verifies that the channel
// water row (lines[4]) does not contain the droplet ID or elapsed time — those
// belong on the info line (lines[1]) only.
func TestTuiAqueductRow_WaterChannelPureWave_NoDropletInfo(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{
		Name:            "virgo",
		DropletID:       "ci-abc12",
		Step:            "review",
		Steps:           steps,
		CataractaeIndex:  2,
		TotalCataractae: 3,
		Elapsed:         3*time.Minute + 7*time.Second,
	}
	lines := m.tuiAqueductRow(ch, 0)

	if len(lines) < 5 {
		t.Fatalf("expected at least 5 lines, got %d", len(lines))
	}
	chanWater := stripANSITest(lines[4])
	if strings.Contains(chanWater, "ci-abc12") {
		t.Errorf("channel water row should not contain droplet ID, got %q", chanWater)
	}
	if strings.Contains(chanWater, "3m") {
		t.Errorf("channel water row should not contain elapsed time, got %q", chanWater)
	}
}

// TestTuiAqueductRow_WaterfallOnlyOnLastStep verifies that the waterfall animation
// (wfExit on the channel water row, wfRows on arch lines) is only rendered when the
// active step is the final step in the pipeline. For mid-pipeline steps and idle
// aqueducts the waterfall must be absent.
func TestTuiAqueductRow_WaterfallOnlyOnLastStep(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}

	const (
		prefixLen = 14
		pillarW   = 28
		nSteps    = 3
		chanW     = nSteps * pillarW
		innerW    = chanW - 2
		wfExitLen = 4 // "░▒▓▓" — visible chars in wfExit
	)
	const rowLenWithWF    = prefixLen + 1 + innerW + 1 + wfExitLen // 102
	const rowLenWithoutWF = prefixLen + 1 + innerW + 1             // 98

	cases := []struct {
		name      string
		step      string
		dropletID string
		wantWF    bool
	}{
		{
			name:      "last step active — waterfall visible",
			step:      "merge",
			dropletID: "ci-abc12",
			wantWF:    true,
		},
		{
			name:      "mid step active — no waterfall",
			step:      "review",
			dropletID: "ci-abc12",
			wantWF:    false,
		},
		{
			name:      "first step active — no waterfall",
			step:      "implement",
			dropletID: "ci-abc12",
			wantWF:    false,
		},
		{
			name:      "idle (no droplet) — no waterfall",
			step:      "",
			dropletID: "",
			wantWF:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := CataractaeInfo{
				Name:      "virgo",
				DropletID: tc.dropletID,
				Step:      tc.step,
				Steps:     steps,
			}
			lines := m.tuiAqueductRow(ch, 0)

			// lines[4] is the channel water row (name[0], info[1], label[2], chan-top[3]).
			chanWater := stripANSITest(lines[4])
			runes := []rune(chanWater)

			wantLen := rowLenWithoutWF
			if tc.wantWF {
				wantLen = rowLenWithWF
			}
			if len(runes) != wantLen {
				t.Errorf("channel water row visual width: got %d, want %d (wantWF=%v)\nrow: %q",
					len(runes), wantLen, tc.wantWF, chanWater)
			}
		})
	}
}

// --- TestActiveAqueducts ---

func TestActiveAqueducts_ReturnsOnlyActive(t *testing.T) {
	cataractae := []CataractaeInfo{
		{Name: "virgo", DropletID: "ci-abc12"},
		{Name: "marcia", DropletID: ""},
		{Name: "leo", DropletID: "ci-xyz99"},
	}
	got := activeAqueducts(cataractae)
	if len(got) != 2 {
		t.Fatalf("want 2 active, got %d", len(got))
	}
	if got[0].Name != "virgo" {
		t.Errorf("got[0].Name = %q, want virgo", got[0].Name)
	}
	if got[1].Name != "leo" {
		t.Errorf("got[1].Name = %q, want leo", got[1].Name)
	}
}

func TestActiveAqueducts_EmptyWhenAllIdle(t *testing.T) {
	cataractae := []CataractaeInfo{
		{Name: "virgo", DropletID: ""},
		{Name: "marcia", DropletID: ""},
	}
	got := activeAqueducts(cataractae)
	if len(got) != 0 {
		t.Errorf("want 0, got %d", len(got))
	}
}

// --- TestPeekSelect ---

// makeModelWithNActive creates a dashboardTUIModel with n active aqueducts plus one idle.
func makeModelWithNActive(n int) dashboardTUIModel {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review"}
	var cataractae []CataractaeInfo
	for i := 0; i < n; i++ {
		cataractae = append(cataractae, CataractaeInfo{
			Name:      fmt.Sprintf("aqueduct%d", i+1),
			RepoName:  "myrepo",
			DropletID: fmt.Sprintf("ci-id%d", i+1),
			Step:      "implement",
			Steps:     steps,
		})
	}
	// Add one idle to make the data non-trivial.
	cataractae = append(cataractae, CataractaeInfo{Name: "idle1", Steps: steps})
	m.data = &DashboardData{Cataractae: cataractae}
	return m
}

func TestPeekSelect_OneActive_OpensPeekDirectly(t *testing.T) {
	m := makeModelWithNActive(1)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	um := updated.(dashboardTUIModel)

	if um.peekSelectMode {
		t.Error("peekSelectMode should not be set when only one active aqueduct")
	}
	if !um.peekActive {
		t.Error("peekActive should be true when only one active aqueduct")
	}
}

func TestPeekSelect_MultipleActive_EntersPicker(t *testing.T) {
	m := makeModelWithNActive(2)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	um := updated.(dashboardTUIModel)

	if !um.peekSelectMode {
		t.Error("peekSelectMode should be set when multiple active aqueducts")
	}
	if um.peekActive {
		t.Error("peekActive should not be set yet in picker mode")
	}
	if um.peekSelectIndex != 0 {
		t.Errorf("peekSelectIndex should start at 0, got %d", um.peekSelectIndex)
	}
}

func TestPeekSelect_NoActive_DoesNothing(t *testing.T) {
	m := newDashboardTUIModel("", "")
	m.data = &DashboardData{Cataractae: []CataractaeInfo{
		{Name: "idle1", DropletID: ""},
	}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	um := updated.(dashboardTUIModel)

	if um.peekSelectMode {
		t.Error("peekSelectMode should not be set with no active aqueducts")
	}
	if um.peekActive {
		t.Error("peekActive should not be set with no active aqueducts")
	}
}

func TestPeekSelect_Esc_CancelsPicker(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(dashboardTUIModel)

	if um.peekSelectMode {
		t.Error("peekSelectMode should be cleared on esc")
	}
}

func TestPeekSelect_Q_CancelsPicker(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	um := updated.(dashboardTUIModel)

	if um.peekSelectMode {
		t.Error("peekSelectMode should be cleared on q")
	}
}

func TestPeekSelect_Down_IncrementsIndex(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	um := updated.(dashboardTUIModel)

	if um.peekSelectIndex != 1 {
		t.Errorf("peekSelectIndex should be 1 after down, got %d", um.peekSelectIndex)
	}
}

func TestPeekSelect_Up_DecrementsIndex(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	um := updated.(dashboardTUIModel)

	if um.peekSelectIndex != 0 {
		t.Errorf("peekSelectIndex should be 0 after up, got %d", um.peekSelectIndex)
	}
}

func TestPeekSelect_IndexClampedAtZero(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	um := updated.(dashboardTUIModel)

	if um.peekSelectIndex != 0 {
		t.Errorf("peekSelectIndex should stay at 0, got %d", um.peekSelectIndex)
	}
}

func TestPeekSelect_IndexClampedAtMax(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 1 // last index for 2 active aqueducts

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	um := updated.(dashboardTUIModel)

	if um.peekSelectIndex != 1 {
		t.Errorf("peekSelectIndex should stay at 1, got %d", um.peekSelectIndex)
	}
}

func TestPeekSelect_Enter_OpensPeekOnSelected(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 1 // select second active aqueduct

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(dashboardTUIModel)

	if um.peekSelectMode {
		t.Error("peekSelectMode should be cleared after enter")
	}
	if !um.peekActive {
		t.Error("peekActive should be set after enter")
	}
	wantSession := "myrepo-aqueduct2"
	if um.peek.session != wantSession {
		t.Errorf("peek.session = %q, want %q", um.peek.session, wantSession)
	}
}

func TestPeekSelect_View_ContainsAqueductNames(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.width = 100
	m.height = 24

	view := m.View()

	if !strings.Contains(view, "aqueduct1") {
		t.Error("picker view should contain aqueduct1")
	}
	if !strings.Contains(view, "aqueduct2") {
		t.Error("picker view should contain aqueduct2")
	}
}

func TestPeekSelect_View_ShowsKeyHints(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.width = 100
	m.height = 24

	view := m.View()

	if !strings.Contains(view, "esc") {
		t.Error("picker view should mention esc to cancel")
	}
	if !strings.Contains(view, "enter") {
		t.Error("picker view should mention enter to connect")
	}
}

// Issue 1: stale peekSelectIndex after data refresh

func TestPeekSelect_DataRefresh_ClampsIndexWhenActiveDecreases(t *testing.T) {
	// Given: picker is open with index pointing at second of two active aqueducts.
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 1

	// When: a data refresh arrives with only one active aqueduct.
	oneActive := makeModelWithNActive(1)
	updated, _ := m.Update(tuiDataMsg(oneActive.data))
	um := updated.(dashboardTUIModel)

	// Then: index is clamped to 0 (last valid index).
	if um.peekSelectIndex != 0 {
		t.Errorf("peekSelectIndex = %d after active decrease, want 0", um.peekSelectIndex)
	}
	if !um.peekSelectMode {
		t.Error("peekSelectMode should remain true when there is still one active aqueduct")
	}
}

func TestPeekSelect_DataRefresh_AutoClosesWhenNoActiveAqueducts(t *testing.T) {
	// Given: picker is open.
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.peekSelectIndex = 1

	// When: a data refresh arrives with zero active aqueducts.
	noneActive := makeModelWithNActive(0)
	updated, _ := m.Update(tuiDataMsg(noneActive.data))
	um := updated.(dashboardTUIModel)

	// Then: picker is automatically closed.
	if um.peekSelectMode {
		t.Error("peekSelectMode should be cleared when no active aqueducts remain")
	}
}

// Issue 2: missing tea.WindowSizeMsg handler in peekSelectMode

func TestPeekSelect_WindowResize_UpdatesDimensions(t *testing.T) {
	// Given: picker is open with initial dimensions.
	m := makeModelWithNActive(2)
	m.peekSelectMode = true
	m.width = 80
	m.height = 24

	// When: a window resize event arrives.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(dashboardTUIModel)

	// Then: dimensions are updated and picker remains open.
	if um.width != 120 {
		t.Errorf("width = %d after resize, want 120", um.width)
	}
	if um.height != 40 {
		t.Errorf("height = %d after resize, want 40", um.height)
	}
	if !um.peekSelectMode {
		t.Error("peekSelectMode should remain true after resize")
	}
}

// ── Peek overlay lifecycle: ctrl+c must not quit the program ────────────────
//
// In a web PTY context (xterm.js → WebSocket → PTY), the browser may send
// ctrl+c (0x03) when the peek overlay opens — either as a copy-shortcut or as
// part of a terminal capability response sequence.  Previously, ctrl+c while
// peek was active returned tea.Quit, killing the subprocess and causing the
// browser to reconnect in a loop.
//
// Fix: ctrl+c while peek or picker is active closes the overlay, not the
// program.  ctrl+c in the bare dashboard view still quits (intentional).

// TestDashboard_PeekActive_CtrlC_ClosesPeekNotQuit verifies that pressing
// ctrl+c while the peek overlay is open closes the overlay without quitting
// the Bubble Tea program.
//
// Given: dashboard with peek overlay open
// When:  ctrl+c key is pressed
// Then:  peek overlay closes (peekActive = false) and the returned cmd is nil
func TestDashboard_PeekActive_CtrlC_ClosesPeekNotQuit(t *testing.T) {
	m := makeModelWithNActive(1)
	m.peekActive = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	um := updated.(dashboardTUIModel)

	if um.peekActive {
		t.Error("peekActive should be false after ctrl+c while peek is open")
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("ctrl+c while peek is open must not return tea.Quit — TUI should stay alive")
		}
	}
}

// TestDashboard_PeekActive_Esc_ClosesPeekNotQuit confirms that esc closes the
// peek overlay without quitting (existing correct behaviour, guarded by test).
//
// Given: dashboard with peek overlay open
// When:  esc key is pressed
// Then:  peek overlay closes and cmd is nil
func TestDashboard_PeekActive_Esc_ClosesPeekNotQuit(t *testing.T) {
	m := makeModelWithNActive(1)
	m.peekActive = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um := updated.(dashboardTUIModel)

	if um.peekActive {
		t.Error("peekActive should be false after esc")
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("esc while peek is open must not return tea.Quit")
		}
	}
}

// TestDashboard_PeekSelectMode_CtrlC_CancelsPickerNotQuit verifies that
// pressing ctrl+c while the aqueduct picker is open cancels the picker without
// quitting the program (same fix applied to the picker overlay for consistency).
//
// Given: dashboard with aqueduct picker open (2 active aqueducts)
// When:  ctrl+c key is pressed
// Then:  picker closes (peekSelectMode = false) and the returned cmd is nil
func TestDashboard_PeekSelectMode_CtrlC_CancelsPickerNotQuit(t *testing.T) {
	m := makeModelWithNActive(2)
	m.peekSelectMode = true

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	um := updated.(dashboardTUIModel)

	if um.peekSelectMode {
		t.Error("peekSelectMode should be false after ctrl+c while picker is open")
	}
	if cmd != nil {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Error("ctrl+c while picker is open must not return tea.Quit — TUI should stay alive")
		}
	}
}
