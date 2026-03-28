package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

	if len(lines) < 4 {
		t.Fatalf("expected at least 4 lines, got %d", len(lines))
	}

	// lines[2] is the label row — shows the full pipeline.
	labelRow := stripANSITest(lines[2])
	if !strings.Contains(labelRow, "review") {
		t.Errorf("label row %q should contain active step name 'review'", labelRow)
	}
	if !strings.Contains(labelRow, "→") {
		t.Errorf("label row %q should contain pipeline separator '→'", labelRow)
	}

	// lines[3] is the first mipmap row; for an active aqueduct it shows animated wave chars.
	firstMipmap := stripANSITest(lines[3])
	if !strings.ContainsAny(firstMipmap, "░▒▓≈") {
		t.Errorf("lines[3] for active aqueduct should contain wave chars (░▒▓≈), got %q", firstMipmap)
	}
}


// TestTuiAqueductRow_LineCount verifies tuiAqueductRow returns the correct number
// of lines: 3 header rows (name, info, label) plus the mipmap height.
// The mipmap is selected by archPillarW=36 → 36x12 → 12 visual lines.
// Total = 3 + 12 = 15. Independent of step count.
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
			// 3 header rows + 12 mipmap lines (36x12 mipmap — arch selected by
			// archPillarW=36, not terminal width)
			const wantLines = 15
			if len(lines) != wantLines {
				t.Errorf("tuiAqueductRow() returned %d lines, want %d", len(lines), wantLines)
			}
		})
	}
}

// TestTuiAqueductRow_MipmapArchLinesNonEmpty verifies that the arch section of
// tuiAqueductRow (rows[3:]) consists of non-empty mipmap lines.
// The ASCII pillar pixel map has been replaced by a pre-rendered ANSI mipmap;
// this test confirms the mipmap content is present in the arch section.
func TestTuiAqueductRow_MipmapArchLinesNonEmpty(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}
	ch := CataractaeInfo{Name: "virgo", Steps: steps}
	lines := m.tuiAqueductRow(ch, 0)

	const headerRows = 3 // name, info, label
	if len(lines) <= headerRows {
		t.Fatalf("expected more than %d lines, got %d", headerRows, len(lines))
	}

	// Every line in the mipmap section must be non-empty.
	for i := headerRows; i < len(lines); i++ {
		if lines[i] == "" {
			t.Errorf("arch mipmap lines[%d] is empty; expected pre-rendered pixel art content", i)
		}
	}
}

// TestTuiAqueductRow_MipmapArchLinesHaveExpectedCount verifies the arch section
// has exactly the expected number of lines. The mipmap is now selected by
// archPillarW (36), not terminal width — so the 36x12 mipmap is always used,
// giving 12 visual lines. Total = 3 header + 12 mipmap = 15.
func TestTuiAqueductRow_MipmapArchLinesHaveExpectedCount(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement"}
	ch := CataractaeInfo{Name: "virgo", Steps: steps}
	lines := m.tuiAqueductRow(ch, 0)

	const headerRows = 3
	const mipmapLines = 12 // 36x12 mipmap — selected by archPillarW=36
	const wantTotal = headerRows + mipmapLines
	if len(lines) != wantTotal {
		t.Errorf("tuiAqueductRow() returned %d lines, want %d (3 header + 12 mipmap)", len(lines), wantTotal)
	}
}


// TestViewAqueductArches_AllIdleState_ShowsIdleArchs verifies that when all
// aqueducts are idle, viewAqueductArches renders each aqueduct with its arch
// mipmap and an "idle" label — no longer shows a single "drought" arch.
func TestViewAqueductArches_AllIdleState_ShowsIdleArchs(t *testing.T) {
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
		t.Fatal("viewAqueductArches() returned no lines in all-idle state")
	}
	allText := strings.Join(lines, "\n")
	cleanText := stripANSITest(allText)

	// Both aqueduct names must be visible (each gets its own arch block).
	if !strings.Contains(cleanText, "virgo") {
		t.Error("all-idle display should show arch for 'virgo'")
	}
	if !strings.Contains(cleanText, "marcia") {
		t.Error("all-idle display should show arch for 'marcia'")
	}
	// Each idle aqueduct shows an "idle" label.
	if !strings.Contains(cleanText, "idle") {
		t.Error("all-idle display should contain 'idle' label for each idle aqueduct")
	}
	// Must produce enough lines to contain mipmap rows (more than just 2 name lines).
	if len(lines) < 10 {
		t.Errorf("all-idle display returned only %d lines; expected arch rows", len(lines))
	}
}

// TestViewAqueductArches_ActiveAqueductDoesNotShowDrought verifies that when at
// least one aqueduct is active, both aqueducts are shown as arch blocks (no drought).
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
	// Idle aqueduct "marcia" must appear — now as an arch block, not compact text.
	if !strings.Contains(cleanText, "marcia") {
		t.Error("idle aqueduct 'marcia' should appear in arch output")
	}
	// The idle arch must have an "idle" label.
	if !strings.Contains(cleanText, "idle") {
		t.Error("idle aqueduct should show 'idle' label below its arch")
	}
}

// TestTuiAqueductRow_ActiveVsIdle_MipmapTopRowDiffers verifies that when a
// droplet is active the first mipmap row (rows[3]) differs from the idle case
// because it shows animated wave characters. The static mipmap rows deeper in
// the arch (rows[5:]) are identical for both active and idle aqueducts.
//
// Forces TrueColor rendering so lipgloss emits ANSI escape codes in the test context.
func TestTuiAqueductRow_ActiveVsIdle_MipmapTopRowDiffers(t *testing.T) {
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

	// First mipmap row is rows[3]. Active (animated wave) must differ from idle (static).
	if active[3] == idle[3] {
		t.Error("first mipmap row (rows[3]) should differ between active and idle aqueduct")
	}

	// The mipmap arch (rows[5]) is beyond the animated trough — same for active and idle.
	if active[5] != idle[5] {
		t.Errorf("mipmap arch rows[5] should be identical for active and idle (static image)\nactive: %q\nidle:   %q",
			active[5], idle[5])
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

// TestTuiAqueductRow_MipmapWaveRow_NoDropletInfo verifies that the animated mipmap
// trough rows (lines[3] and lines[4]) do not contain the droplet ID or elapsed
// time — those belong on the info line (lines[1]) only.
func TestTuiAqueductRow_MipmapWaveRow_NoDropletInfo(t *testing.T) {
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
	for _, rowIdx := range []int{3, 4} {
		row := stripANSITest(lines[rowIdx])
		if strings.Contains(row, "ci-abc12") {
			t.Errorf("mipmap trough row[%d] should not contain droplet ID, got %q", rowIdx, row)
		}
		if strings.Contains(row, "3m") {
			t.Errorf("mipmap trough row[%d] should not contain elapsed time, got %q", rowIdx, row)
		}
	}
}

// TestTuiAqueductRow_WaterfallOnlyOnLastStep verifies that the waterfall animation
// (wfExit appended to the last mipmap row) is only rendered when the active step
// is the final step in the pipeline. For mid-pipeline steps and idle aqueducts
// the waterfall must be absent.
func TestTuiAqueductRow_WaterfallOnlyOnLastStep(t *testing.T) {
	m := newDashboardTUIModel("", "")
	steps := []string{"implement", "review", "merge"}

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

			if len(lines) == 0 {
				t.Fatal("tuiAqueductRow returned no lines")
			}

			// wfExit ("░▒▓▓") is appended to the last mipmap row when on the final step.
			lastRow := stripANSITest(lines[len(lines)-1])
			hasWF := strings.Contains(lastRow, "▓▓")
			if hasWF != tc.wantWF {
				t.Errorf("last mipmap row waterfall presence: got %v, want %v (wantWF=%v)\nrow: %q",
					hasWF, tc.wantWF, tc.wantWF, lastRow)
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
	// Force non-tmux path so the inline peek overlay is used (not new-window).
	origInsideTmux := insideTmux
	insideTmux = func() bool { return false }
	defer func() { insideTmux = origInsideTmux }()

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
	// Force non-tmux path so the inline peek overlay is used (not new-window).
	origInsideTmux := insideTmux
	insideTmux = func() bool { return false }
	defer func() { insideTmux = origInsideTmux }()

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

// --- TestDashboardStateHash ---

// TestDashboardStateHash_StableForSameState verifies that identical data produces
// the same hash on repeated calls.
//
// Given: a DashboardData with known counts and cataractae
// When:  dashboardStateHash is called twice
// Then:  both calls return the same string
func TestDashboardStateHash_StableForSameState(t *testing.T) {
	data := &DashboardData{
		FlowingCount: 1,
		QueuedCount:  2,
		DoneCount:    3,
		Cataractae: []CataractaeInfo{
			{Name: "virgo", DropletID: "ci-abc", Step: "implement"},
		},
	}
	h1 := dashboardStateHash(data)
	h2 := dashboardStateHash(data)
	if h1 != h2 {
		t.Errorf("same data should produce same hash; got %q then %q", h1, h2)
	}
	if h1 == "" {
		t.Error("hash of non-nil data should not be empty")
	}
}

// TestDashboardStateHash_ChangesWhenFlowingCountChanges verifies that a
// change in FlowingCount produces a different hash.
//
// Given: two DashboardData structs with different FlowingCount
// When:  dashboardStateHash is called on each
// Then:  the hashes are different
func TestDashboardStateHash_ChangesWhenFlowingCountChanges(t *testing.T) {
	d1 := &DashboardData{FlowingCount: 0}
	d2 := &DashboardData{FlowingCount: 1}
	if dashboardStateHash(d1) == dashboardStateHash(d2) {
		t.Error("different FlowingCount should produce different hashes")
	}
}

// TestDashboardStateHash_ChangesWhenDropletAssigned verifies that assigning a
// droplet to an aqueduct produces a different hash.
//
// Given: two DashboardData structs where one has a droplet assigned
// When:  dashboardStateHash is called on each
// Then:  the hashes are different
func TestDashboardStateHash_ChangesWhenDropletAssigned(t *testing.T) {
	d1 := &DashboardData{Cataractae: []CataractaeInfo{{Name: "virgo", DropletID: ""}}}
	d2 := &DashboardData{Cataractae: []CataractaeInfo{{Name: "virgo", DropletID: "ci-abc"}}}
	if dashboardStateHash(d1) == dashboardStateHash(d2) {
		t.Error("droplet assignment change should produce different hashes")
	}
}

// TestDashboardStateHash_NilSafe verifies that nil input returns a consistent
// empty string without panicking.
//
// Given: nil DashboardData
// When:  dashboardStateHash is called
// Then:  returns "" without panic, and two nil calls return equal values
func TestDashboardStateHash_NilSafe(t *testing.T) {
	h := dashboardStateHash(nil)
	if h != "" {
		t.Errorf("nil data should return empty string, got %q", h)
	}
	if dashboardStateHash(nil) != dashboardStateHash(nil) {
		t.Error("two nil calls must return equal values")
	}
}

// TestDashboardStateHash_ChangesWhenFarmRunningChanges verifies that a change
// in FarmRunning produces a different hash. Without this, a Castellarius
// start/stop while FlowingCount==0 is invisible to the idle detector and the
// dashboard stays in the slow 5s backoff mode instead of switching back fast.
//
// Given: two DashboardData structs with identical counts but different FarmRunning
// When:  dashboardStateHash is called on each
// Then:  the hashes are different
func TestDashboardStateHash_ChangesWhenFarmRunningChanges(t *testing.T) {
	d1 := &DashboardData{FlowingCount: 0, FarmRunning: false}
	d2 := &DashboardData{FlowingCount: 0, FarmRunning: true}
	if dashboardStateHash(d1) == dashboardStateHash(d2) {
		t.Error("FarmRunning change should produce different hashes")
	}
}

// --- TestRunDashboard_AdaptiveRate ---

// TestRunDashboard_AdaptiveRate_PollCountDropsWhenIdle asserts that the polling
// loop backs off to the slow interval after observing a consistently idle state.
// Without adaptive backoff, poll count over the test window would equal
// window/fastInterval. With backoff it should be significantly lower.
//
// Given: a fetcher that always returns empty/idle state
// When:  runDashboardWith runs for ~600ms with fast=50ms, slow=250ms
// Then:  total poll count is well below window/fastInterval
func TestRunDashboard_AdaptiveRate_PollCountDropsWhenIdle(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	var callCount int32
	idleFetcher := func(cfg, db string) *DashboardData {
		atomic.AddInt32(&callCount, 1)
		return &DashboardData{FlowingCount: 0, FetchedAt: time.Now()}
	}

	inputCh := make(chan byte, 1)
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- runDashboardWith(cfgPath, dbPath, inputCh, &out, idleFetcher,
			50*time.Millisecond, 250*time.Millisecond)
	}()

	const window = 600 * time.Millisecond
	time.Sleep(window)
	inputCh <- 'q'
	if err := <-done; err != nil {
		t.Fatalf("runDashboardWith returned error: %v", err)
	}

	n := int(atomic.LoadInt32(&callCount))
	// Without backoff: ~600/50 = 12 polls (plus initial = 13).
	// With backoff: initial + 1 fast + ~2 slow = ~4 polls.
	// Assert strictly fewer than half the "no-backoff" count.
	maxFastPolls := int(window/(50*time.Millisecond)) + 1 // 13
	halfMax := maxFastPolls / 2                          // 6
	if n >= halfMax {
		t.Errorf("poll count = %d, want < %d (adaptive backoff not working)", n, halfMax)
	}
}

// TestRunDashboard_AdaptiveRate_StaysFastWhenActive verifies that the loop does
// not back off when droplets are actively flowing.
//
// Given: a fetcher that always returns FlowingCount=1
// When:  runDashboardWith runs for ~300ms with fast=50ms, slow=250ms
// Then:  poll count is at least window/fastInterval/2 (stayed in fast mode)
func TestRunDashboard_AdaptiveRate_StaysFastWhenActive(t *testing.T) {
	cfgPath := tempCfg(t)
	dbPath := tempDB(t)

	var callCount int32
	activeFetcher := func(cfg, db string) *DashboardData {
		atomic.AddInt32(&callCount, 1)
		return &DashboardData{FlowingCount: 1, FetchedAt: time.Now()}
	}

	inputCh := make(chan byte, 1)
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- runDashboardWith(cfgPath, dbPath, inputCh, &out, activeFetcher,
			50*time.Millisecond, 250*time.Millisecond)
	}()

	const window = 300 * time.Millisecond
	time.Sleep(window)
	inputCh <- 'q'
	if err := <-done; err != nil {
		t.Fatalf("runDashboardWith returned error: %v", err)
	}

	n := int(atomic.LoadInt32(&callCount))
	// With fast=50ms and 300ms window: expect at least 4 polls (conservative lower bound).
	if n < 4 {
		t.Errorf("poll count = %d, want >= 4 (should stay in fast mode when active)", n)
	}
}
