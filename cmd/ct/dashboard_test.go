package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/citadel/internal/queue"
	"github.com/MichielDean/citadel/internal/workflow"
)

// --- helpers ---

// tempDB creates a temporary SQLite database and returns its path and a cleanup func.
func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

// tempCfg writes a minimal citadel.yaml referencing a feature.yaml stub.
// Returns the path to the config file.
func tempCfg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Minimal workflow YAML.
	wfContent := `name: test
steps:
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

	// Config referencing two workers named "furiosa" and "nux".
	cfgContent := `repos:
  - name: myrepo
    url: https://example.com/repo
    workflow_path: feature.yaml
    workers: 2
    names:
      - furiosa
      - nux
    prefix: mr
max_total_workers: 4
`
	cfgPath := filepath.Join(dir, "citadel.yaml")
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
	c, err := queue.New(dbPath, "mr")
	if err != nil {
		t.Fatal(err)
	}

	// Add: 1 flowing assigned to "furiosa", 1 queued, 1 closed.
	flowing, _ := c.Add("myrepo", "Feature A", "", 1, 2)
	c.GetReady("myrepo") // marks it in_progress
	c.Assign(flowing.ID, "furiosa", "implement")

	_, _ = c.Add("myrepo", "Feature B", "", 2, 2) // stays open/queued

	closed, _ := c.Add("myrepo", "Feature C", "", 1, 2)
	c.CloseItem(closed.ID)
	c.Close()

	data := fetchDashboardData(cfgPath, dbPath)

	if data.ChannelCount != 2 {
		t.Errorf("ChannelCount = %d, want 2", data.ChannelCount)
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

	// Channel "furiosa" should be assigned to the flowing item.
	var furiosa *ChannelInfo
	for i := range data.Channels {
		if data.Channels[i].Name == "furiosa" {
			furiosa = &data.Channels[i]
		}
	}
	if furiosa == nil {
		t.Fatal("channel furiosa not found in data.Channels")
	}
	if furiosa.ItemID != flowing.ID {
		t.Errorf("furiosa.ItemID = %q, want %q", furiosa.ItemID, flowing.ID)
	}
	if furiosa.Step != "implement" {
		t.Errorf("furiosa.Step = %q, want %q", furiosa.Step, "implement")
	}
	if furiosa.StepIndex != 1 {
		t.Errorf("furiosa.StepIndex = %d, want 1", furiosa.StepIndex)
	}
	if furiosa.TotalSteps != 3 {
		t.Errorf("furiosa.TotalSteps = %d, want 3", furiosa.TotalSteps)
	}

	// Channel "nux" should be idle.
	var nux *ChannelInfo
	for i := range data.Channels {
		if data.Channels[i].Name == "nux" {
			nux = &data.Channels[i]
		}
	}
	if nux == nil {
		t.Fatal("channel nux not found in data.Channels")
	}
	if nux.ItemID != "" {
		t.Errorf("nux.ItemID = %q, want empty (idle)", nux.ItemID)
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

// --- TestFetchDashboardData_FarmNotRunning_ShowsIdleState ---

func TestFetchDashboardData_FarmNotRunning_ShowsIdleState(t *testing.T) {
	t.Run("missing config returns empty data", func(t *testing.T) {
		data := fetchDashboardData("/nonexistent/citadel.yaml", "/nonexistent/queue.db")

		if data == nil {
			t.Fatal("expected non-nil DashboardData even on error")
		}
		if data.FarmRunning {
			t.Error("FarmRunning should be false when config is missing")
		}
		if data.ChannelCount != 0 {
			t.Errorf("ChannelCount = %d, want 0", data.ChannelCount)
		}
		if data.FetchedAt.IsZero() {
			t.Error("FetchedAt should always be set")
		}
	})

	t.Run("valid config but missing DB shows channels idle", func(t *testing.T) {
		cfgPath := tempCfg(t)
		dbPath := filepath.Join(t.TempDir(), "nonexistent.db")
		// Don't create the DB — remove it if it exists.
		os.Remove(dbPath)

		// queue.New creates the DB if missing, so we can't test a truly missing DB
		// at the queue level without making the path unwritable. Instead, test
		// that a fresh empty DB yields all-idle channels and zero counts.
		data := fetchDashboardData(cfgPath, dbPath)

		if data.ChannelCount != 2 {
			t.Errorf("ChannelCount = %d, want 2 (from config)", data.ChannelCount)
		}
		if data.FlowingCount != 0 {
			t.Errorf("FlowingCount = %d, want 0 for empty DB", data.FlowingCount)
		}
		if data.QueuedCount != 0 {
			t.Errorf("QueuedCount = %d, want 0 for empty DB", data.QueuedCount)
		}
		for _, ch := range data.Channels {
			if ch.ItemID != "" {
				t.Errorf("channel %q should be idle (empty ItemID), got %q", ch.Name, ch.ItemID)
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
	data := &DashboardData{
		ChannelCount: 2,
		FlowingCount: 1,
		QueuedCount:  1,
		DoneCount:    3,
		Channels: []ChannelInfo{
			{Name: "furiosa", ItemID: "ci-abc12", Step: "implement", StepIndex: 1, TotalSteps: 6, Elapsed: 2*time.Minute + 14*time.Second},
			{Name: "nux"},
		},
		CisternItems: []*queue.WorkItem{
			{ID: "ci-abc12", Repo: "citadel", Status: "in_progress", CurrentStep: "implement", Complexity: 2},
		},
		RecentItems: []*queue.WorkItem{
			{ID: "ci-xyz99", Status: "closed", CurrentStep: "merge", UpdatedAt: time.Now()},
		},
		FarmRunning: true,
		FetchedAt:   time.Date(2026, 3, 14, 15, 7, 42, 0, time.UTC),
	}

	out := renderDashboard(data)

	sections := []string{"CITADEL", "CHANNELS", "CISTERN", "RECENT FLOW"}
	for _, s := range sections {
		if !strings.Contains(out, s) {
			t.Errorf("output missing section %q", s)
		}
	}
	if !strings.Contains(out, "furiosa") {
		t.Error("output missing channel name furiosa")
	}
	if !strings.Contains(out, "nux") {
		t.Error("output missing channel name nux")
	}
	if !strings.Contains(out, "15:07:42") {
		t.Error("output missing last update timestamp")
	}
	if !strings.Contains(out, "q to quit") {
		t.Error("output missing footer hint")
	}
}

func TestRenderDashboard_AqueductsClosedWhenNoChannels(t *testing.T) {
	data := &DashboardData{
		Channels:  []ChannelInfo{},
		FetchedAt: time.Now(),
	}
	out := renderDashboard(data)
	if !strings.Contains(out, "Aqueducts closed") {
		t.Error("expected 'Aqueducts closed' when no channels configured")
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
	steps := []workflow.WorkflowStep{
		{Name: "implement"},
		{Name: "review"},
		{Name: "merge"},
	}

	if idx := stepIndexInWorkflow("implement", steps); idx != 1 {
		t.Errorf("stepIndex(implement) = %d, want 1", idx)
	}
	if idx := stepIndexInWorkflow("merge", steps); idx != 3 {
		t.Errorf("stepIndex(merge) = %d, want 3", idx)
	}
	if idx := stepIndexInWorkflow("unknown", steps); idx != 0 {
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
