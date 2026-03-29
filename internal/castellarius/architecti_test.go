package castellarius

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// architectiConfig returns a valid, enabled ArchitectiConfig for test use.
func architectiConfig(thresholdMinutes, maxFiles int) *aqueduct.ArchitectiConfig {
	return &aqueduct.ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: thresholdMinutes,
		MaxFilesPerRun:   maxFiles,
	}
}

// stagnantDroplet creates a droplet in stagnant state whose UpdatedAt is
// updatedAgo in the past.
func stagnantDroplet(id string, updatedAgo time.Duration) *cistern.Droplet {
	return &cistern.Droplet{
		ID:        id,
		Repo:      "test-repo",
		Status:    "stagnant",
		UpdatedAt: time.Now().Add(-updatedAgo),
	}
}

// blockedDroplet creates a droplet in blocked state whose UpdatedAt is
// updatedAgo in the past.
func blockedDroplet(id string, updatedAgo time.Duration) *cistern.Droplet {
	return &cistern.Droplet{
		ID:        id,
		Repo:      "test-repo",
		Status:    "blocked",
		UpdatedAt: time.Now().Add(-updatedAgo),
	}
}

// --- heartbeatArchitecti tests ---

func TestHeartbeatArchitecti_WhenNilConfig_DoesNotSpawn(t *testing.T) {
	// Given: Architecti config is nil (disabled at config level)
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 90*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	// s.config.Architecti is nil (not set)

	var called int32
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		atomic.AddInt32(&called, 1)
		return nil
	}

	// When: heartbeatArchitecti runs
	s.heartbeatArchitecti(context.Background())

	// Then: runArchitectiFn is never called
	if n := atomic.LoadInt32(&called); n != 0 {
		t.Errorf("runArchitectiFn called %d times, want 0", n)
	}
}

func TestHeartbeatArchitecti_WhenDisabled_DoesNotSpawn(t *testing.T) {
	// Given: Architecti is present but Enabled=false
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 90*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = &aqueduct.ArchitectiConfig{
		Enabled:          false,
		ThresholdMinutes: 30,
		MaxFilesPerRun:   10,
	}

	var called int32
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		atomic.AddInt32(&called, 1)
		return nil
	}

	s.heartbeatArchitecti(context.Background())

	if n := atomic.LoadInt32(&called); n != 0 {
		t.Errorf("runArchitectiFn called %d times, want 0", n)
	}
}

func TestHeartbeatArchitecti_StagnantDropletPastThreshold_SpawnsArchitecti(t *testing.T) {
	// Given: architecti enabled, stagnant droplet idle > threshold
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(30, 10)

	spawned := make(chan string, 4)
	s.runArchitectiFn = func(_ context.Context, d *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		spawned <- d.ID
		return nil
	}

	// When: heartbeatArchitecti runs
	s.heartbeatArchitecti(context.Background())

	// Then: runArchitectiFn is called for the droplet
	select {
	case id := <-spawned:
		if id != "d-001" {
			t.Errorf("got droplet ID %q, want %q", id, "d-001")
		}
	case <-time.After(time.Second):
		t.Fatal("runArchitectiFn was not called within 1s")
	}
}

func TestHeartbeatArchitecti_BlockedDropletPastThreshold_SpawnsArchitecti(t *testing.T) {
	// Given: architecti enabled, blocked droplet idle > threshold
	client := newMockClient()
	client.items["d-002"] = blockedDroplet("d-002", 45*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(30, 10)

	spawned := make(chan string, 4)
	s.runArchitectiFn = func(_ context.Context, d *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		spawned <- d.ID
		return nil
	}

	s.heartbeatArchitecti(context.Background())

	select {
	case id := <-spawned:
		if id != "d-002" {
			t.Errorf("got droplet ID %q, want %q", id, "d-002")
		}
	case <-time.After(time.Second):
		t.Fatal("runArchitectiFn was not called within 1s")
	}
}

func TestHeartbeatArchitecti_DropletBelowThreshold_DoesNotSpawn(t *testing.T) {
	// Given: architecti enabled but droplet is recent (below threshold)
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 5*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(30, 10) // 30-minute threshold

	var called int32
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		atomic.AddInt32(&called, 1)
		return nil
	}

	s.heartbeatArchitecti(context.Background())
	// Give goroutines a moment to run (none should be spawned)
	time.Sleep(20 * time.Millisecond)

	if n := atomic.LoadInt32(&called); n != 0 {
		t.Errorf("runArchitectiFn called %d times, want 0", n)
	}
}

func TestHeartbeatArchitecti_PassesCorrectConfigToFn(t *testing.T) {
	// Given: architecti enabled with specific config values
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = &aqueduct.ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: 30,
		MaxFilesPerRun:   99,
	}

	cfgCh := make(chan aqueduct.ArchitectiConfig, 1)
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, cfg aqueduct.ArchitectiConfig) error {
		cfgCh <- cfg
		return nil
	}

	s.heartbeatArchitecti(context.Background())

	select {
	case cfg := <-cfgCh:
		if cfg.MaxFilesPerRun != 99 {
			t.Errorf("MaxFilesPerRun = %d, want 99", cfg.MaxFilesPerRun)
		}
		if cfg.ThresholdMinutes != 30 {
			t.Errorf("ThresholdMinutes = %d, want 30", cfg.ThresholdMinutes)
		}
	case <-time.After(time.Second):
		t.Fatal("runArchitectiFn was not called within 1s")
	}
}

func TestHeartbeatArchitecti_WhenAlreadyInFlight_DoesNotDoubleSpawn(t *testing.T) {
	// Given: architecti enabled, droplet past threshold
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(30, 10)

	started := make(chan struct{})
	unblock := make(chan struct{})

	var called int32
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		atomic.AddInt32(&called, 1)
		close(started) // signal that goroutine is running
		<-unblock      // block until test releases it
		return nil
	}

	// When: first heartbeat spawns the goroutine
	s.heartbeatArchitecti(context.Background())

	// Wait until the goroutine is actually running
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("goroutine did not start within 1s")
	}

	// When: second heartbeat fires while first goroutine is still running
	s.heartbeatArchitecti(context.Background())

	// Release the blocked goroutine
	close(unblock)

	// Wait for in-flight map cleanup
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.architectiInFlightMu.Lock()
		_, still := s.architectiInFlight["d-001"]
		s.architectiInFlightMu.Unlock()
		if !still {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Then: runArchitectiFn was called exactly once
	if n := atomic.LoadInt32(&called); n != 1 {
		t.Errorf("runArchitectiFn called %d times, want 1", n)
	}
}

func TestHeartbeatArchitecti_AfterInFlightCompletes_AllowsRespawn(t *testing.T) {
	// Given: architecti enabled, droplet past threshold
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)

	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(30, 10)

	var wg sync.WaitGroup
	var called int32
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		atomic.AddInt32(&called, 1)
		wg.Done()
		return nil
	}

	// First heartbeat
	wg.Add(1)
	s.heartbeatArchitecti(context.Background())
	wg.Wait() // wait for first goroutine to complete

	// Ensure in-flight map is cleared before second heartbeat
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.architectiInFlightMu.Lock()
		_, still := s.architectiInFlight["d-001"]
		s.architectiInFlightMu.Unlock()
		if !still {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Second heartbeat — should spawn again since in-flight is clear
	wg.Add(1)
	s.heartbeatArchitecti(context.Background())
	wg.Wait()

	if n := atomic.LoadInt32(&called); n != 2 {
		t.Errorf("runArchitectiFn called %d times, want 2", n)
	}
}

// --- runArchitecti method tests ---

// testSchedulerWithArchitecti returns a Castellarius configured for architecti tests.
// It injects a no-op exec function so tests don't need a real claude or system prompt.
func testSchedulerWithArchitecti(client *mockClient) *Castellarius {
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(30, 3)
	// Default exec: return empty array (no actions).
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("[]"), nil
	}
	s.architectiRestartCastellariusFn = func() error { return nil }
	return s
}

func TestRunArchitecti_GlobalSingletonGuard_SkipsWhenRunning(t *testing.T) {
	// Given: architectiRunning is already set (another goroutine is running)
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	var execCalled int32
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		atomic.AddInt32(&execCalled, 1)
		return []byte("[]"), nil
	}
	s.architectiRunning.Store(true)

	// When: runArchitecti is called
	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	// Then: no error, exec not called (singleton guard fired)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&execCalled); n != 0 {
		t.Errorf("architectiExecFn called %d times, want 0", n)
	}
	// architectiRunning remains true (we didn't own it)
	if !s.architectiRunning.Load() {
		t.Error("architectiRunning was cleared by non-owner")
	}
}

func TestRunArchitecti_GlobalSingletonGuard_ClearsAfterRun(t *testing.T) {
	// Given: architectiRunning starts false
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	// When: runArchitecti completes normally
	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	// Then: no error, architectiRunning is cleared
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if s.architectiRunning.Load() {
		t.Error("architectiRunning not cleared after run")
	}
}

func TestRunArchitecti_EmptyArray_LogsNoAction(t *testing.T) {
	// Given: agent returns empty action array
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("[]"), nil
	}

	// When: runArchitecti runs
	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	// Then: no error, no client mutations
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	assigns := client.assignCalls
	cancels := len(client.cancelled)
	filed := len(client.filed)
	client.mu.Unlock()
	if assigns != 0 || cancels != 0 || filed != 0 {
		t.Errorf("expected no client actions, got assigns=%d cancels=%d filed=%d", assigns, cancels, filed)
	}
}

func TestRunArchitecti_RestartAction_ResetsDropletToNamedCataractae(t *testing.T) {
	// Given: agent returns a restart action for d-001 → implement
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"transient failure"}]`), nil
	}

	// When: runArchitecti runs
	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	// Then: Assign called with empty worker and "implement" step
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	step := client.steps["d-001"]
	client.mu.Unlock()
	if step != "implement" {
		t.Errorf("step = %q, want %q", step, "implement")
	}
}

func TestRunArchitecti_RestartRateLimit_BlocksSecondRestartWithin24h(t *testing.T) {
	// Given: agent returns a restart action for d-001
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	restartJSON := `[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(restartJSON), nil
	}

	d := stagnantDroplet("d-001", 60*time.Minute)

	// First run: restart should be executed
	if err := s.runArchitecti(context.Background(), d, *s.config.Architecti); err != nil {
		t.Fatalf("first run error: %v", err)
	}
	client.mu.Lock()
	firstAssigns := client.assignCalls
	client.mu.Unlock()
	if firstAssigns != 1 {
		t.Errorf("after first run: assignCalls = %d, want 1", firstAssigns)
	}

	// Second run within 24h: restart should be rate-limited
	if err := s.runArchitecti(context.Background(), d, *s.config.Architecti); err != nil {
		t.Fatalf("second run error: %v", err)
	}
	client.mu.Lock()
	secondAssigns := client.assignCalls
	client.mu.Unlock()
	if secondAssigns != 1 {
		t.Errorf("after second run: assignCalls = %d, want 1 (rate limited)", secondAssigns)
	}
}

func TestRunArchitecti_CancelAction_CancelsDroplet(t *testing.T) {
	// Given: agent returns a cancel action for d-001
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"cancel","droplet_id":"d-001","reason":"irrecoverable"}]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	cancelReason := client.cancelled["d-001"]
	client.mu.Unlock()
	if cancelReason == "" {
		t.Error("expected d-001 to be cancelled, but cancelled map is empty")
	}
}

func TestRunArchitecti_FileAction_CreatesNewDroplet(t *testing.T) {
	// Given: agent returns a file action for test-repo
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"file","repo":"test-repo","title":"Fix the thing","description":"details","complexity":"standard","reason":"structural bug"}]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	filedCount := len(client.filed)
	var filedItem filedDroplet
	if filedCount > 0 {
		filedItem = client.filed[0]
	}
	client.mu.Unlock()
	if filedCount != 1 {
		t.Errorf("filed count = %d, want 1", filedCount)
	}
	if filedItem.Title != "Fix the thing" {
		t.Errorf("filed title = %q, want %q", filedItem.Title, "Fix the thing")
	}
}

func TestRunArchitecti_FileAction_MaxFilesPerRun_LimitsActions(t *testing.T) {
	// Given: agent returns 5 file actions but MaxFilesPerRun = 3
	client := newMockClient()
	s := testSchedulerWithArchitecti(client) // config has MaxFilesPerRun=3

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[
			{"action":"file","repo":"test-repo","title":"Fix 1","complexity":"standard","reason":"r"},
			{"action":"file","repo":"test-repo","title":"Fix 2","complexity":"standard","reason":"r"},
			{"action":"file","repo":"test-repo","title":"Fix 3","complexity":"standard","reason":"r"},
			{"action":"file","repo":"test-repo","title":"Fix 4","complexity":"standard","reason":"r"},
			{"action":"file","repo":"test-repo","title":"Fix 5","complexity":"standard","reason":"r"}
		]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	filedCount := len(client.filed)
	client.mu.Unlock()
	if filedCount != 3 {
		t.Errorf("filed count = %d, want 3 (MaxFilesPerRun enforced)", filedCount)
	}
}

func TestRunArchitecti_NoteAction_AddsNoteToDroplet(t *testing.T) {
	// Given: agent returns a note action for d-001
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"note","droplet_id":"d-001","body":"looks like a known transient","reason":"r"}]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	notes := client.notes["d-001"]
	client.mu.Unlock()
	var found bool
	for _, n := range notes {
		if n.CataractaeName == "architecti" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected note from 'architecti', found none")
	}
}

func TestRunArchitecti_RestartCastellarius_WhenSchedulerHung(t *testing.T) {
	// Given: agent returns restart_castellarius; health file shows scheduler hung
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	// Write a health file showing the scheduler has not ticked recently.
	tmpDir := t.TempDir()
	s.dbPath = tmpDir + "/cistern.db"
	s.pollInterval = 10 * time.Second
	// LastTickAt 60s ago > 5×10s = 50s threshold → scheduler is hung.
	hf := HealthFile{
		LastTickAt:      time.Now().Add(-60 * time.Second),
		PollIntervalSec: 10,
	}
	writeTestHealthFile(t, tmpDir, hf)

	var restartCalled int32
	s.architectiRestartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart_castellarius","reason":"scheduler appears hung"}]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 1 {
		t.Errorf("restartCastellariusFn called %d times, want 1", n)
	}
}

func TestRunArchitecti_RestartCastellarius_SkipsWhenSchedulerHealthy(t *testing.T) {
	// Given: health file shows scheduler ticked recently
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	// Write a fresh health file to a temp dir so the guard reads it.
	tmpDir := t.TempDir()
	s.dbPath = tmpDir + "/cistern.db"
	hf := HealthFile{
		LastTickAt:      time.Now(), // just ticked
		PollIntervalSec: 10,
	}
	writeTestHealthFile(t, tmpDir, hf)

	var restartCalled int32
	s.architectiRestartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart_castellarius","reason":"just testing"}]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 0 {
		t.Errorf("restartCastellariusFn called %d times, want 0 (scheduler healthy)", n)
	}
}

// writeTestHealthFile writes a HealthFile JSON to {dir}/castellarius.health for testing.
func writeTestHealthFile(t *testing.T, dir string, hf HealthFile) {
	t.Helper()
	path := dir + "/castellarius.health"
	b, err := json.Marshal(hf)
	if err != nil {
		t.Fatalf("marshal health file: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}
}

// --- Issue 1: fail-closed restart_castellarius guard ---

func TestRunArchitecti_RestartCastellarius_RefusesWhenDbPathEmpty(t *testing.T) {
	// Given: dbPath is empty — health file cannot be read
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)
	// s.dbPath is "" (default) — health file is unavailable

	var restartCalled int32
	s.architectiRestartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart_castellarius","reason":"test"}]`), nil
	}

	// When: runArchitecti dispatches the restart_castellarius action
	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	// Then: restart refused — cannot verify hung state without health file
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 0 {
		t.Errorf("restartCastellariusFn called %d times, want 0 (fail-closed: no health file)", n)
	}
}

func TestRunArchitecti_RestartCastellarius_RefusesWhenHealthFileUnreadable(t *testing.T) {
	// Given: dbPath is set but the health file does not exist
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	tmpDir := t.TempDir()
	s.dbPath = tmpDir + "/cistern.db"
	// Deliberately do NOT write a health file — ReadHealthFile will fail.

	var restartCalled int32
	s.architectiRestartCastellariusFn = func() error {
		atomic.AddInt32(&restartCalled, 1)
		return nil
	}
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart_castellarius","reason":"test"}]`), nil
	}

	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n := atomic.LoadInt32(&restartCalled); n != 0 {
		t.Errorf("restartCastellariusFn called %d times, want 0 (fail-closed: health file unreadable)", n)
	}
}

// --- Issue 3: missing cataractae validation on restart ---

func TestRunArchitecti_RestartAction_MissingCataractae_NoAssignCalled(t *testing.T) {
	// Given: agent outputs a restart action with no cataractae field
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart","droplet_id":"d-001","reason":"missing cataractae"}]`), nil
	}

	// When: runArchitecti dispatches the action
	err := s.runArchitecti(context.Background(), stagnantDroplet("d-001", 60*time.Minute), *s.config.Architecti)

	// Then: no error propagated (dispatcher logs and continues), but Assign never called
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (missing cataractae must be rejected before Assign)", assigns)
	}
}

// --- Issue 2: rate limit not recorded when Assign fails ---

func TestRunArchitecti_RestartRateLimit_NotRecordedWhenAssignFails(t *testing.T) {
	// Given: Assign will fail on the first call
	client := newMockClient()
	client.items["d-001"] = stagnantDroplet("d-001", 60*time.Minute)
	client.assignErr = errors.New("assign failed")
	s := testSchedulerWithArchitecti(client)

	restartJSON := `[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(restartJSON), nil
	}

	d := stagnantDroplet("d-001", 60*time.Minute)

	// First run: Assign fails — rate limit must NOT be recorded
	if err := s.runArchitecti(context.Background(), d, *s.config.Architecti); err != nil {
		t.Fatalf("first run error: %v", err)
	}
	client.mu.Lock()
	firstAssigns := client.assignCalls
	client.mu.Unlock()
	if firstAssigns != 1 {
		t.Errorf("after first run: assignCalls = %d, want 1", firstAssigns)
	}

	// Clear the error so the second Assign can succeed
	client.mu.Lock()
	client.assignErr = nil
	client.mu.Unlock()

	// Second run: must NOT be rate-limited (first Assign failed, no timestamp recorded)
	if err := s.runArchitecti(context.Background(), d, *s.config.Architecti); err != nil {
		t.Fatalf("second run error: %v", err)
	}
	client.mu.Lock()
	secondAssigns := client.assignCalls
	client.mu.Unlock()
	if secondAssigns != 2 {
		t.Errorf("after second run: assignCalls = %d, want 2 (rate limit must not block retry after failed assign)", secondAssigns)
	}
}
