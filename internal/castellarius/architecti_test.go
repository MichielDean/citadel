package castellarius

import (
	"context"
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
