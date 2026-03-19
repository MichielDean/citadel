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

// tempCfg writes a minimal cistern.yaml referencing a feature.yaml stub.
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
	wfPath := filepath.Join(dir, "feature.yaml")
	if err := os.WriteFile(wfPath, []byte(wfContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Config referencing two operators named "virgo" and "marcia".
	cfgContent := `repos:
  - name: myrepo
    url: https://example.com/repo
    workflow_path: feature.yaml
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

	if data.CataractaCount != 2 {
		t.Errorf("CataractaCount = %d, want 2", data.CataractaCount)
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
	var virgo *CataractaInfo
	for i := range data.Cataractae {
		if data.Cataractae[i].Name == "virgo" {
			virgo = &data.Cataractae[i]
		}
	}
	if virgo == nil {
		t.Fatal("cataracta virgo not found in data.Cataractae")
	}
	if virgo.DropletID != flowing.ID {
		t.Errorf("virgo.DropletID = %q, want %q", virgo.DropletID, flowing.ID)
	}
	if virgo.Step != "implement" {
		t.Errorf("virgo.Step = %q, want %q", virgo.Step, "implement")
	}
	if virgo.CataractaIndex != 1 {
		t.Errorf("virgo.CataractaIndex = %d, want 1", virgo.CataractaIndex)
	}
	if virgo.TotalCataractae != 3 {
		t.Errorf("virgo.TotalCataractae = %d, want 3", virgo.TotalCataractae)
	}

	// Cataracta "marcia" should be dry.
	var marcia *CataractaInfo
	for i := range data.Cataractae {
		if data.Cataractae[i].Name == "marcia" {
			marcia = &data.Cataractae[i]
		}
	}
	if marcia == nil {
		t.Fatal("cataracta marcia not found in data.Cataractae")
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
		if data.CataractaCount != 0 {
			t.Errorf("CataractaCount = %d, want 0", data.CataractaCount)
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

		if data.CataractaCount != 2 {
			t.Errorf("CataractaCount = %d, want 2 (from config)", data.CataractaCount)
		}
		if data.FlowingCount != 0 {
			t.Errorf("FlowingCount = %d, want 0 for empty DB", data.FlowingCount)
		}
		if data.QueuedCount != 0 {
			t.Errorf("QueuedCount = %d, want 0 for empty DB", data.QueuedCount)
		}
		for _, ch := range data.Cataractae {
			if ch.DropletID != "" {
				t.Errorf("cataracta %q should be dry (empty DropletID), got %q", ch.Name, ch.DropletID)
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
		CataractaCount: 2,
		FlowingCount:   1,
		QueuedCount:    1,
		DoneCount:      3,
		Cataractae: []CataractaInfo{
			{Name: "virgo", DropletID: "ci-abc12", Step: "implement", Steps: steps, CataractaIndex: 1, TotalCataractae: 3, Elapsed: 2*time.Minute + 14*time.Second},
			{Name: "marcia", Steps: steps},
		},
		CisternItems: []*cistern.Droplet{
			{ID: "ci-abc12", Repo: "cistern", Status: "in_progress", CurrentCataracta: "implement", Complexity: 2},
		},
		RecentItems: []*cistern.Droplet{
			{ID: "ci-xyz99", Status: "delivered", CurrentCataracta: "merge", UpdatedAt: time.Now()},
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
		t.Error("output missing cataracta name virgo")
	}
	if !strings.Contains(out, "marcia") {
		t.Error("output missing cataracta name marcia")
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
	ch := CataractaInfo{
		Name:            "virgo",
		DropletID:       "ci-s76ho",
		Step:            "review",
		Steps:           []string{"implement", "review", "qa"},
		Elapsed:         3*time.Minute + 12*time.Second,
		CataractaIndex:  2,
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
	ch := CataractaInfo{
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
	ch := CataractaInfo{
		Name:            "virgo",
		DropletID:       "ci-abc",
		Step:            "review",
		Steps:           []string{"implement", "review", "qa"},
		CataractaIndex:  2,
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
		Cataractae:   []CataractaInfo{},
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
	steps := []aqueduct.WorkflowCataracta{
		{Name: "implement"},
		{Name: "review"},
		{Name: "merge"},
	}

	if idx := cataractaIndexInWorkflow("implement", steps); idx != 1 {
		t.Errorf("stepIndex(implement) = %d, want 1", idx)
	}
	if idx := cataractaIndexInWorkflow("merge", steps); idx != 3 {
		t.Errorf("stepIndex(merge) = %d, want 3", idx)
	}
	if idx := cataractaIndexInWorkflow("unknown", steps); idx != 0 {
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
