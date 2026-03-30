package castellarius

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// architectiConfig returns a valid ArchitectiConfig for test use.
func architectiConfig(maxFiles int) *aqueduct.ArchitectiConfig {
	return &aqueduct.ArchitectiConfig{
		MaxFilesPerRun: maxFiles,
	}
}

// pooledDroplet creates a droplet in pooled state whose UpdatedAt is
// updatedAgo in the past.
func pooledDroplet(id string, updatedAgo time.Duration) *cistern.Droplet {
	return &cistern.Droplet{
		ID:        id,
		Repo:      "test-repo",
		Status:    "pooled",
		UpdatedAt: time.Now().Add(-updatedAgo),
	}
}

// --- tryEnqueueArchitecti tests ---

func TestTryEnqueueArchitecti_NoExistingNote_EnqueuesAndWritesNote(t *testing.T) {
	// Given: pooled droplet with no existing [architecti] invocation note
	client := newMockClient()
	droplet := pooledDroplet("d-001", 5*time.Minute)
	client.items["d-001"] = droplet

	s := testScheduler(client, newMockRunner(client))

	// When: tryEnqueueArchitecti is called
	s.tryEnqueueArchitecti(client, droplet)

	// Then: droplet is in the queue
	select {
	case got := <-s.architectiQueue:
		if got.ID != "d-001" {
			t.Errorf("queue got droplet ID %q, want %q", got.ID, "d-001")
		}
	default:
		t.Fatal("expected droplet in queue, but queue was empty")
	}

	// Then: invocation note was written to the client
	client.mu.Lock()
	notes := client.notes["d-001"]
	client.mu.Unlock()
	var found bool
	for _, n := range notes {
		if n.CataractaeName == "architecti" && strings.HasPrefix(n.Content, architectiInvocationNotePrefix) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected invocation note written with prefix [architecti] enqueued:, found none")
	}
}

func TestTryEnqueueArchitecti_ExistingInvocationNote_DoesNotEnqueue(t *testing.T) {
	// Given: droplet already has an [architecti] enqueued: note (prior invocation)
	client := newMockClient()
	droplet := pooledDroplet("d-001", 5*time.Minute)
	client.items["d-001"] = droplet
	client.notes["d-001"] = []cistern.CataractaeNote{
		{
			DropletID:      "d-001",
			CataractaeName: "architecti",
			Content:        architectiInvocationNotePrefix + " pooled",
			CreatedAt:      time.Now(),
		},
	}

	s := testScheduler(client, newMockRunner(client))

	// When: tryEnqueueArchitecti is called
	s.tryEnqueueArchitecti(client, droplet)

	// Then: nothing was enqueued
	select {
	case got := <-s.architectiQueue:
		t.Errorf("expected empty queue, but got droplet %q", got.ID)
	default:
		// correct: queue is empty
	}
}

func TestTryEnqueueArchitecti_OtherArchitectiNote_DoesEnqueue(t *testing.T) {
	// Given: droplet has an architecti action note (e.g., restart) but NOT an
	// invocation note — action notes must not block fresh enqueues.
	client := newMockClient()
	droplet := pooledDroplet("d-001", 5*time.Minute)
	client.items["d-001"] = droplet
	client.notes["d-001"] = []cistern.CataractaeNote{
		{
			DropletID:      "d-001",
			CataractaeName: "architecti",
			Content:        "Architecti restart → implement: transient failure",
			CreatedAt:      time.Now(),
		},
	}

	s := testScheduler(client, newMockRunner(client))

	// When: tryEnqueueArchitecti is called
	s.tryEnqueueArchitecti(client, droplet)

	// Then: droplet is enqueued (action note does not act as dedup guard)
	select {
	case got := <-s.architectiQueue:
		if got.ID != "d-001" {
			t.Errorf("queue got droplet ID %q, want %q", got.ID, "d-001")
		}
	default:
		t.Fatal("expected droplet in queue, but queue was empty")
	}
}

func TestTryEnqueueArchitecti_AddNoteFails_EnqueuesWithoutNote(t *testing.T) {
	// Given: AddNote returns an error — channel send happens first, so the
	// droplet is still queued even though the dedup note could not be written.
	client := newMockClient()
	droplet := pooledDroplet("d-001", 5*time.Minute)
	client.addNoteErr = errors.New("db error")

	s := testScheduler(client, newMockRunner(client))

	// When: tryEnqueueArchitecti sends to channel then attempts note write
	s.tryEnqueueArchitecti(client, droplet)

	// Then: droplet IS in the queue (send-before-note; note failure does not block processing)
	select {
	case got := <-s.architectiQueue:
		if got.ID != "d-001" {
			t.Errorf("queue got droplet ID %q, want %q", got.ID, "d-001")
		}
	default:
		t.Fatal("expected droplet in queue after AddNote failure, but queue was empty")
	}

	// Then: no invocation note was written (AddNote failed)
	client.mu.Lock()
	notes := client.notes["d-001"]
	client.mu.Unlock()
	for _, n := range notes {
		if n.CataractaeName == "architecti" && strings.HasPrefix(n.Content, architectiInvocationNotePrefix) {
			t.Error("unexpected invocation note written despite AddNote error")
		}
	}
}

func TestTryEnqueueArchitecti_PooledDroplet_EnqueuesAndWritesNote(t *testing.T) {
	// Given: pooled droplet with no existing invocation note
	client := newMockClient()
	droplet := pooledDroplet("d-002", 10*time.Minute)
	client.items["d-002"] = droplet

	s := testScheduler(client, newMockRunner(client))

	// When: tryEnqueueArchitecti is called
	s.tryEnqueueArchitecti(client, droplet)

	// Then: droplet is enqueued
	select {
	case got := <-s.architectiQueue:
		if got.ID != "d-002" {
			t.Errorf("queue got droplet ID %q, want %q", got.ID, "d-002")
		}
	default:
		t.Fatal("expected d-002 in queue, but queue was empty")
	}

	// Then: invocation note content encodes the status
	client.mu.Lock()
	notes := client.notes["d-002"]
	client.mu.Unlock()
	var found bool
	for _, n := range notes {
		if n.CataractaeName == "architecti" && strings.HasPrefix(n.Content, architectiInvocationNotePrefix) {
			if strings.Contains(n.Content, "pooled") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected invocation note mentioning 'pooled' status")
	}
}

func TestTryEnqueueArchitecti_QueueFull_DoesNotBlockAndDoesNotWriteNote(t *testing.T) {
	// Given: queue is already at capacity
	client := newMockClient()
	droplet := pooledDroplet("d-001", 5*time.Minute)

	s := testScheduler(client, newMockRunner(client))

	// Fill the queue to capacity
	for i := 0; i < architectiQueueCap; i++ {
		s.architectiQueue <- &cistern.Droplet{ID: "filler"}
	}

	// When: tryEnqueueArchitecti is called (should not block)
	done := make(chan struct{})
	go func() {
		s.tryEnqueueArchitecti(client, droplet)
		close(done)
	}()

	select {
	case <-done:
		// correct: returned without blocking
	case <-time.After(200 * time.Millisecond):
		t.Fatal("tryEnqueueArchitecti blocked on full queue")
	}

	// Then: no invocation note was written — queue-full must not permanently
	// silence the droplet by recording a dedup note without a queued entry.
	client.mu.Lock()
	notes := client.notes["d-001"]
	client.mu.Unlock()
	for _, n := range notes {
		if n.CataractaeName == "architecti" && strings.HasPrefix(n.Content, architectiInvocationNotePrefix) {
			t.Error("invocation note written despite queue-full drop — droplet would be permanently silenced")
		}
	}
}

// --- startArchitectiQueue tests ---

func TestStartArchitectiQueue_SerialDrain_RunsOneAtATime(t *testing.T) {
	// Given: two droplets enqueued; runArchitectiFn blocks until released
	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(10)
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("[]"), nil
	}
	s.architectiRestartCastellariusFn = func() error { return nil }

	unblock := make(chan struct{})
	started := make(chan string, 4)
	var concurrent int32

	s.runArchitectiFn = func(_ context.Context, d *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		cur := atomic.AddInt32(&concurrent, 1)
		started <- d.ID
		if cur > 1 {
			// Signal test that overlap occurred
			started <- "CONCURRENT"
		}
		<-unblock
		atomic.AddInt32(&concurrent, -1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startArchitectiQueue(ctx)

	// Enqueue two droplets with different IDs
	s.architectiQueue <- pooledDroplet("d-001", 5*time.Minute)
	s.architectiQueue <- pooledDroplet("d-002", 5*time.Minute)

	// Wait for first droplet to start
	select {
	case id := <-started:
		if id == "CONCURRENT" {
			t.Fatal("concurrent Architecti runs detected")
		}
	case <-time.After(time.Second):
		t.Fatal("first droplet did not start processing within 1s")
	}

	// While first is processing, confirm second has not started
	select {
	case id := <-started:
		if id == "CONCURRENT" {
			t.Fatal("concurrent Architecti runs detected")
		}
		t.Fatalf("second droplet started processing before first completed (got %q)", id)
	case <-time.After(50 * time.Millisecond):
		// correct: second is waiting
	}

	// Unblock first; second should now run
	close(unblock)
	select {
	case id := <-started:
		if id == "CONCURRENT" {
			t.Fatal("concurrent Architecti runs detected")
		}
		if id != "d-002" {
			t.Errorf("expected d-002, got %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("second droplet did not start after first completed")
	}
}

func TestStartArchitectiQueue_DuplicatesInQueue_ProcessedOnce(t *testing.T) {
	// Given: same droplet ID enqueued twice (race between enqueue and note write)
	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(10)

	var called int32
	s.runArchitectiFn = func(_ context.Context, d *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		atomic.AddInt32(&called, 1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Put the same droplet ID in the queue twice before starting the drainer
	d := pooledDroplet("d-001", 5*time.Minute)
	s.architectiQueue <- d
	s.architectiQueue <- d

	s.startArchitectiQueue(ctx)

	// Allow drainer to process both entries
	time.Sleep(100 * time.Millisecond)
	cancel()
	s.architectiWg.Wait()

	if n := atomic.LoadInt32(&called); n != 1 {
		t.Errorf("runArchitectiFn called %d times, want 1 (duplicate should be discarded)", n)
	}
}

func TestStartArchitectiQueue_UsesConfigFromScheduler(t *testing.T) {
	// Given: scheduler has Architecti config with MaxFilesPerRun=77
	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(77)

	cfgCh := make(chan aqueduct.ArchitectiConfig, 1)
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, cfg aqueduct.ArchitectiConfig) error {
		cfgCh <- cfg
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startArchitectiQueue(ctx)
	s.architectiQueue <- pooledDroplet("d-001", 5*time.Minute)

	select {
	case cfg := <-cfgCh:
		if cfg.MaxFilesPerRun != 77 {
			t.Errorf("MaxFilesPerRun = %d, want 77", cfg.MaxFilesPerRun)
		}
	case <-time.After(time.Second):
		t.Fatal("runArchitectiFn was not called within 1s")
	}
}

func TestStartArchitectiQueue_DefaultConfig_WhenArchitectiNil(t *testing.T) {
	// Given: no ArchitectiConfig set — should use built-in defaults
	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	// s.config.Architecti is nil

	cfgCh := make(chan aqueduct.ArchitectiConfig, 1)
	s.runArchitectiFn = func(_ context.Context, _ *cistern.Droplet, cfg aqueduct.ArchitectiConfig) error {
		cfgCh <- cfg
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startArchitectiQueue(ctx)
	s.architectiQueue <- pooledDroplet("d-001", 5*time.Minute)

	select {
	case cfg := <-cfgCh:
		if cfg.MaxFilesPerRun != architectiDefaultMaxFilesPerRun {
			t.Errorf("MaxFilesPerRun = %d, want %d (default)", cfg.MaxFilesPerRun, architectiDefaultMaxFilesPerRun)
		}
	case <-time.After(time.Second):
		t.Fatal("runArchitectiFn was not called within 1s")
	}
}

func TestTryEnqueueArchitecti_RestartSafe_ExistingNoteBlocksReEnqueue(t *testing.T) {
	// Given: Castellarius restarts; pooled droplet already has an invocation note
	// from the prior run. The note check must prevent re-enqueue.
	client := newMockClient()
	droplet := pooledDroplet("d-001", 120*time.Minute)
	client.items["d-001"] = droplet
	// Simulate: note was written before the restart
	client.notes["d-001"] = []cistern.CataractaeNote{
		{
			DropletID:      "d-001",
			CataractaeName: "architecti",
			Content:        architectiInvocationNotePrefix + " pooled",
			CreatedAt:      time.Now().Add(-90 * time.Minute),
		},
	}

	s := testScheduler(client, newMockRunner(client))

	// When: tryEnqueueArchitecti is called (e.g., on first tick after restart)
	s.tryEnqueueArchitecti(client, droplet)

	// Then: nothing enqueued — restart-safe guarantee holds
	select {
	case got := <-s.architectiQueue:
		t.Errorf("expected empty queue after restart, but got droplet %q", got.ID)
	default:
		// correct
	}
}

func TestTryEnqueueArchitecti_SuccessfulEnqueue_WritesBothNoteAndQueues(t *testing.T) {
	// Given: successful enqueue — channel send happens first, then note write.
	// Verify both the queue entry and the invocation note are present after the call.
	client := newMockClient()
	droplet := pooledDroplet("d-001", 5*time.Minute)

	s := testScheduler(client, newMockRunner(client))

	s.tryEnqueueArchitecti(client, droplet)

	// Then: invocation note was written
	client.mu.Lock()
	notes := client.notes["d-001"]
	client.mu.Unlock()

	var invocationNote bool
	for _, n := range notes {
		if n.CataractaeName == "architecti" && strings.HasPrefix(n.Content, architectiInvocationNotePrefix) {
			invocationNote = true
		}
	}
	if !invocationNote {
		t.Error("invocation note not written after successful channel send")
	}

	// Then: droplet is also in the queue
	select {
	case <-s.architectiQueue:
	default:
		t.Error("droplet was not sent to queue")
	}
}

// --- runArchitecti method tests ---

// testSchedulerWithArchitecti returns a Castellarius configured for architecti tests.
// It injects a no-op exec function so tests don't need a real claude or system prompt.
func testSchedulerWithArchitecti(client *mockClient) *Castellarius {
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(3)
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
	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	// When: runArchitecti completes normally
	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("[]"), nil
	}

	// When: runArchitecti runs
	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"transient failure"}]`), nil
	}

	// When: runArchitecti runs
	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	restartJSON := `[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(restartJSON), nil
	}

	d := pooledDroplet("d-001", 60*time.Minute)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"cancel","droplet_id":"d-001","reason":"irrecoverable"}]`), nil
	}

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"note","droplet_id":"d-001","body":"looks like a known transient","reason":"r"}]`), nil
	}

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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

func TestStartArchitectiQueue_PanicInRunFn_DrainerContinues(t *testing.T) {
	// Given: runArchitectiFn panics on the first droplet. The drainer must
	// recover and continue processing subsequent droplets — a panic must not
	// kill the goroutine permanently.
	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(10)

	processed := make(chan string, 4)
	s.runArchitectiFn = func(_ context.Context, d *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		if d.ID == "d-panic" {
			panic("simulated panic")
		}
		processed <- d.ID
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startArchitectiQueue(ctx)

	// Enqueue the panicking droplet first, then a normal one
	s.architectiQueue <- &cistern.Droplet{ID: "d-panic", Status: "pooled"}
	s.architectiQueue <- pooledDroplet("d-ok", 5*time.Minute)

	// Then: drainer recovers from panic and processes d-ok
	select {
	case id := <-processed:
		if id != "d-ok" {
			t.Errorf("got %q, want d-ok", id)
		}
	case <-time.After(time.Second):
		t.Fatal("d-ok not processed within 1s — drainer goroutine may have died on panic")
	}
}

func TestStartArchitectiQueue_SeenMap_ClearedBetweenBursts(t *testing.T) {
	// Given: the drainer's seen-map is cleared when the channel drains.
	// A droplet processed in one burst must not be blocked by a stale seen-map
	// entry when it appears in a later burst (e.g., re-queued directly for testing).
	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.config.Architecti = architectiConfig(10)

	processed := make(chan string, 4)
	s.runArchitectiFn = func(_ context.Context, d *cistern.Droplet, _ aqueduct.ArchitectiConfig) error {
		processed <- d.ID
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.startArchitectiQueue(ctx)

	// First burst: enqueue d-001 and drain it
	s.architectiQueue <- pooledDroplet("d-001", 5*time.Minute)
	select {
	case id := <-processed:
		if id != "d-001" {
			t.Fatalf("first burst: got %q, want d-001", id)
		}
	case <-time.After(time.Second):
		t.Fatal("first burst: d-001 not processed within 1s")
	}

	// Allow drainer to detect empty channel and clear seen-map
	time.Sleep(20 * time.Millisecond)

	// Second burst: enqueue d-001 again; seen-map should be cleared so it runs
	s.architectiQueue <- pooledDroplet("d-001", 5*time.Minute)
	select {
	case id := <-processed:
		if id != "d-001" {
			t.Fatalf("second burst: got %q, want d-001", id)
		}
	case <-time.After(time.Second):
		t.Fatal("second burst: d-001 not processed within 1s — seen-map not cleared between bursts")
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

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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

	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart","droplet_id":"d-001","reason":"missing cataractae"}]`), nil
	}

	// When: runArchitecti dispatches the action
	err := s.runArchitecti(context.Background(), pooledDroplet("d-001", 60*time.Minute), *s.config.Architecti)

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
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	client.assignErr = errors.New("assign failed")
	s := testSchedulerWithArchitecti(client)

	restartJSON := `[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(restartJSON), nil
	}

	d := pooledDroplet("d-001", 60*time.Minute)

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

// --- RunArchitectiAdHoc tests ---

func TestRunArchitectiAdHoc_DryRun_ReturnsSnapshotAndOutput_WithoutDispatching(t *testing.T) {
	// Given: dry-run mode, agent returns a restart action
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	agentOutput := `[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(agentOutput), nil
	}

	// When: RunArchitectiAdHoc is called with dryRun=true
	snapshot, rawOutput, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		true,
	)

	// Then: no error, snapshot non-empty, raw output matches agent output, actions nil (not parsed in dry-run)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if snapshot == "" {
		t.Error("expected non-empty snapshot")
	}
	if string(rawOutput) != agentOutput {
		t.Errorf("rawOutput = %q, want %q", rawOutput, agentOutput)
	}
	if actions != nil {
		t.Errorf("actions = %v, want nil (dry-run must not parse)", actions)
	}
	// Then: no dispatch — Assign not called
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (dry-run must not dispatch)", assigns)
	}
}

func TestRunArchitectiAdHoc_Normal_DispatchesActions(t *testing.T) {
	// Given: normal mode, agent returns a restart action for d-001
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"test"}]`), nil
	}

	// When: RunArchitectiAdHoc is called with dryRun=false
	snapshot, rawOutput, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		false,
	)

	// Then: no error, dispatch occurred, returned actions match dispatched actions
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if snapshot == "" {
		t.Error("expected non-empty snapshot")
	}
	if len(rawOutput) == 0 {
		t.Error("expected non-empty rawOutput")
	}
	if len(actions) != 1 || actions[0].Action != "restart" || actions[0].DropletID != "d-001" {
		t.Errorf("actions = %v, want [{restart d-001 implement test}]", actions)
	}
	client.mu.Lock()
	step := client.steps["d-001"]
	client.mu.Unlock()
	if step != "implement" {
		t.Errorf("step = %q, want %q (action was dispatched)", step, "implement")
	}
}

func TestRunArchitectiAdHoc_Normal_EmptyActions_NoDispatch(t *testing.T) {
	// Given: agent returns empty action array
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[]`), nil
	}

	_, _, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		false,
	)

	// Then: no error, no dispatch, nil actions
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if actions != nil {
		t.Errorf("actions = %v, want nil (empty actions list)", actions)
	}
	client.mu.Lock()
	assigns := client.assignCalls
	client.mu.Unlock()
	if assigns != 0 {
		t.Errorf("assignCalls = %d, want 0 (empty actions list)", assigns)
	}
}

func TestRunArchitectiAdHoc_ExecError_ReturnsError(t *testing.T) {
	// Given: exec fn returns an error
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	execErr := errors.New("session spawn failed")
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return nil, execErr
	}

	// When: RunArchitectiAdHoc is called
	_, _, _, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		false,
	)

	// Then: error wraps the exec error
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exec") {
		t.Errorf("expected error to mention exec, got: %v", err)
	}
	if !errors.Is(err, execErr) {
		t.Errorf("expected error to wrap execErr, got: %v", err)
	}
}

func TestRunArchitectiAdHoc_SnapshotContainsTriggerDropletID(t *testing.T) {
	// Given: synthetic trigger droplet with a known ID
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`[]`), nil
	}

	trigger := &cistern.Droplet{
		ID:        "my-trigger-droplet",
		Status:    "pooled",
		UpdatedAt: time.Now().Add(-30 * time.Minute),
	}

	// When: RunArchitectiAdHoc is called
	snapshot, _, _, err := s.RunArchitectiAdHoc(
		context.Background(),
		trigger,
		*s.config.Architecti,
		false,
	)

	// Then: snapshot includes the trigger droplet ID
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(snapshot, "my-trigger-droplet") {
		t.Errorf("snapshot does not contain trigger droplet ID; snapshot = %q", snapshot)
	}
}

// --- buildArchitectiSnapshot notes rendering tests ---

// DISABLED: func TestBuildArchitectiSnapshot_Notes_RenderedForStagnantDroplet(t *testing.T) {
// DISABLED: 	// Given: stagnant droplet with two notes
// DISABLED: 	client := newMockClient()
// DISABLED: 	s := testSchedulerWithArchitecti(client)
// DISABLED: 
// DISABLED: 	t0 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
// DISABLED: 	t1 := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
// DISABLED: 
// DISABLED: 	d := &cistern.Droplet{ID: "ci-stag1", Repo: "test-repo", Status: "pooled", UpdatedAt: time.Now().Add(-5 * time.Minute)}
// DISABLED: 	client.items["ci-stag1"] = d
// DISABLED: 	client.notes["ci-stag1"] = []cistern.CataractaeNote{
// DISABLED: 		{DropletID: "ci-stag1", CataractaeName: "qa", Content: "criterion not met", CreatedAt: t0},
// DISABLED: 		{DropletID: "ci-stag1", CataractaeName: "architecti", Content: "Architecti restart → implement", CreatedAt: t1},
// DISABLED: 	}
// DISABLED: 
// DISABLED: 	// When: snapshot is built
// DISABLED: 	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), *s.config.Architecti)
// DISABLED: 
// DISABLED: 	// Then: notes section is present with droplet heading
// DISABLED: 	if !strings.Contains(snapshot, "#### ci-stag1") {
// DISABLED: 		t.Errorf("snapshot missing droplet notes heading; snippet = %q", snapshot)
// DISABLED: 	}
// DISABLED: 	if !strings.Contains(snapshot, "[qa]") || !strings.Contains(snapshot, "criterion not met") {
// DISABLED: 		t.Errorf("snapshot missing qa note content; snapshot = %q", snapshot)
// DISABLED: 	}
// DISABLED: 	if !strings.Contains(snapshot, "[architecti]") || !strings.Contains(snapshot, "Architecti restart") {
// DISABLED: 		t.Errorf("snapshot missing architecti note content; snapshot = %q", snapshot)
// DISABLED: 	}
// DISABLED: }

// DISABLED: func TestBuildArchitectiSnapshot_Notes_ChronologicalOrder(t *testing.T) {
// DISABLED: 	// Given: stagnant droplet with notes out of order
// DISABLED: 	client := newMockClient()
// DISABLED: 	s := testSchedulerWithArchitecti(client)
// DISABLED: 
// DISABLED: 	earlier := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
// DISABLED: 	later := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
// DISABLED: 
// DISABLED: 	d := &cistern.Droplet{ID: "ci-order1", Repo: "test-repo", Status: "pooled", UpdatedAt: time.Now().Add(-5 * time.Minute)}
// DISABLED: 	client.items["ci-order1"] = d
// DISABLED: 	// Intentionally insert later note first.
// DISABLED: 	client.notes["ci-order1"] = []cistern.CataractaeNote{
// DISABLED: 		{DropletID: "ci-order1", CataractaeName: "qa", Content: "second note", CreatedAt: later},
// DISABLED: 		{DropletID: "ci-order1", CataractaeName: "implement", Content: "first note", CreatedAt: earlier},
// DISABLED: 	}
// DISABLED: 
// DISABLED: 	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), *s.config.Architecti)
// DISABLED: 
// DISABLED: 	// Then: "first note" appears before "second note" in the snapshot
// DISABLED: 	firstPos := strings.Index(snapshot, "first note")
// DISABLED: 	secondPos := strings.Index(snapshot, "second note")
// DISABLED: 	if firstPos < 0 || secondPos < 0 {
// DISABLED: 		t.Fatalf("notes not found in snapshot; snapshot = %q", snapshot)
// DISABLED: 	}
// DISABLED: 	if firstPos > secondPos {
// DISABLED: 		t.Errorf("notes not in chronological order: 'first note' at %d, 'second note' at %d", firstPos, secondPos)
// DISABLED: 	}
// DISABLED: }

// DISABLED: func TestBuildArchitectiSnapshot_Notes_Table_DriveGroupCoverage(t *testing.T) {
// DISABLED: 	// Given: one droplet per status group (stagnant, blocked, active in_progress, stuck_routing),
// DISABLED: 	// each with a distinct note. The snapshot must include notes for all four.
// DISABLED: 	tests := []struct {
// DISABLED: 		name     string
// DISABLED: 		status   string
// DISABLED: 		outcome  string // non-empty → stuck_routing
// DISABLED: 		noteText string
// DISABLED: 	}{
// DISABLED: 		{"stagnant", "stagnant", "", "stagnant-note-content"},
// DISABLED: 		{"blocked", "blocked", "", "blocked-note-content"},
// DISABLED: 		{"in_progress_active", "in_progress", "", "active-note-content"},
// DISABLED: 		{"stuck_routing", "in_progress", "pass", "stuck-note-content"},
// DISABLED: 	}
// DISABLED: 
// DISABLED: 	for _, tt := range tests {
// DISABLED: 		t.Run(tt.name, func(t *testing.T) {
// DISABLED: 			client := newMockClient()
// DISABLED: 			s := testSchedulerWithArchitecti(client)
// DISABLED: 
// DISABLED: 			d := &cistern.Droplet{
// DISABLED: 				ID:      "ci-" + tt.name,
// DISABLED: 				Repo:    "test-repo",
// DISABLED: 				Status:  tt.status,
// DISABLED: 				Outcome: tt.outcome,
// DISABLED: 				UpdatedAt: time.Now().Add(-5 * time.Minute),
// DISABLED: 			}
// DISABLED: 			client.items[d.ID] = d
// DISABLED: 			client.notes[d.ID] = []cistern.CataractaeNote{
// DISABLED: 				{DropletID: d.ID, CataractaeName: "implement", Content: tt.noteText, CreatedAt: time.Now().Add(-1 * time.Minute)},
// DISABLED: 			}
// DISABLED: 
// DISABLED: 			snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), *s.config.Architecti)
// DISABLED: 
// DISABLED: 			if !strings.Contains(snapshot, tt.noteText) {
// DISABLED: 				t.Errorf("snapshot missing note %q for %s droplet; snapshot = %q", tt.noteText, tt.name, snapshot)
// DISABLED: 			}
// DISABLED: 		})
// DISABLED: 	}
// DISABLED: }

// DISABLED: func TestBuildArchitectiSnapshot_Notes_OmittedWhenDropletHasNoNotes(t *testing.T) {
// DISABLED: 	// Given: two stagnant droplets; only one has notes
// DISABLED: 	client := newMockClient()
// DISABLED: 	s := testSchedulerWithArchitecti(client)
// DISABLED: 
// DISABLED: 	d1 := &cistern.Droplet{ID: "ci-with-notes", Repo: "test-repo", Status: "pooled", UpdatedAt: time.Now().Add(-5 * time.Minute)}
// DISABLED: 	d2 := &cistern.Droplet{ID: "ci-no-notes", Repo: "test-repo", Status: "pooled", UpdatedAt: time.Now().Add(-5 * time.Minute)}
// DISABLED: 	client.items[d1.ID] = d1
// DISABLED: 	client.items[d2.ID] = d2
// DISABLED: 	client.notes[d1.ID] = []cistern.CataractaeNote{
// DISABLED: 		{DropletID: d1.ID, CataractaeName: "implement", Content: "has a note", CreatedAt: time.Now()},
// DISABLED: 	}
// DISABLED: 	// d2 has no notes (nothing in client.notes["ci-no-notes"])
// DISABLED: 
// DISABLED: 	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), *s.config.Architecti)
// DISABLED: 
// DISABLED: 	// Then: ci-with-notes has a heading; ci-no-notes does not
// DISABLED: 	if !strings.Contains(snapshot, "#### ci-with-notes") {
// DISABLED: 		t.Errorf("snapshot missing heading for droplet with notes")
// DISABLED: 	}
// DISABLED: 	if strings.Contains(snapshot, "#### ci-no-notes") {
// DISABLED: 		t.Errorf("snapshot unexpectedly contains heading for droplet with no notes")
// DISABLED: 	}
// DISABLED: }

// DISABLED: func TestBuildArchitectiSnapshot_Notes_GetNotesError_LoggedAndContinued(t *testing.T) {
// DISABLED: 	// Given: GetNotes returns an error for all calls; snapshot must still complete.
// DISABLED: 	var logBuf bytes.Buffer
// DISABLED: 	client := newMockClient()
// DISABLED: 	client.getNotesErr = errors.New("db read failure")
// DISABLED: 
// DISABLED: 	s := testSchedulerWithArchitecti(client)
// DISABLED: 	s.logger = newTestLogger(&logBuf)
// DISABLED: 
// DISABLED: 	d := &cistern.Droplet{ID: "ci-err1", Repo: "test-repo", Status: "pooled", UpdatedAt: time.Now().Add(-5 * time.Minute)}
// DISABLED: 	client.items[d.ID] = d
// DISABLED: 
// DISABLED: 	// When: snapshot is built despite GetNotes errors
// DISABLED: 	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), *s.config.Architecti)
// DISABLED: 
// DISABLED: 	// Then: snapshot is non-empty (not aborted by note error)
// DISABLED: 	if snapshot == "" {
// DISABLED: 		t.Error("expected non-empty snapshot when GetNotes errors are encountered")
// DISABLED: 	}
// DISABLED: 	// Then: warning was logged
// DISABLED: 	if !strings.Contains(logBuf.String(), "get notes failed") {
// DISABLED: 		t.Errorf("expected warning logged for GetNotes failure; log = %q", logBuf.String())
// DISABLED: 	}
// DISABLED: 	// Then: no notes section for the erroring droplet
// DISABLED: 	if strings.Contains(snapshot, "#### ci-err1") {
// DISABLED: 		t.Errorf("unexpected notes section for droplet with GetNotes error")
// DISABLED: 	}
// DISABLED: }

// DISABLED: func TestBuildArchitectiSnapshot_Notes_TimestampAndCataractaeNameIncluded(t *testing.T) {
// DISABLED: 	// Given: a note with a specific timestamp and cataractae name
// DISABLED: 	client := newMockClient()
// DISABLED: 	s := testSchedulerWithArchitecti(client)
// DISABLED: 
// DISABLED: 	noteTime := time.Date(2024, 6, 1, 14, 30, 0, 0, time.UTC)
// DISABLED: 	d := &cistern.Droplet{ID: "ci-ts1", Repo: "test-repo", Status: "pooled", UpdatedAt: time.Now().Add(-5 * time.Minute)}
// DISABLED: 	client.items[d.ID] = d
// DISABLED: 	client.notes[d.ID] = []cistern.CataractaeNote{
// DISABLED: 		{DropletID: d.ID, CataractaeName: "reviewer", Content: "looks good", CreatedAt: noteTime},
// DISABLED: 	}
// DISABLED: 
// DISABLED: 	snapshot, _ := s.buildArchitectiSnapshot(context.Background(), pooledDroplet("trigger", 1*time.Minute), *s.config.Architecti)
// DISABLED: 
// DISABLED: 	// Then: snapshot contains formatted timestamp and cataractae name
// DISABLED: 	if !strings.Contains(snapshot, "2024-06-01T14:30:00Z") {
// DISABLED: 		t.Errorf("snapshot missing RFC3339 timestamp; snapshot = %q", snapshot)
// DISABLED: 	}
// DISABLED: 	if !strings.Contains(snapshot, "[reviewer]") {
// DISABLED: 		t.Errorf("snapshot missing cataractae name; snapshot = %q", snapshot)
// DISABLED: 	}
// DISABLED: }

func TestRunArchitectiAdHoc_Normal_MarkdownWrappedJSON_ReturnsParsedActions(t *testing.T) {
	// Given: LLM output wraps JSON in markdown code block (typical LLM output)
	client := newMockClient()
	client.items["d-001"] = pooledDroplet("d-001", 60*time.Minute)
	s := testSchedulerWithArchitecti(client)

	// LLM commonly wraps JSON in a markdown fenced code block
	agentOutput := "Here are my proposed actions:\n\n```json\n" +
		`[{"action":"restart","droplet_id":"d-001","cataractae":"implement","reason":"pooled"}]` +
		"\n```\n"
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(agentOutput), nil
	}

	// When: RunArchitectiAdHoc dispatches (non-dry-run)
	_, _, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		false,
	)

	// Then: actions are parsed correctly despite markdown wrapping
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("len(actions) = %d, want 1", len(actions))
	}
	if actions[0].Action != "restart" || actions[0].DropletID != "d-001" {
		t.Errorf("actions[0] = %+v, want {action:restart droplet_id:d-001}", actions[0])
	}
}

func TestRunArchitectiAdHoc_ParseError_ReturnsError(t *testing.T) {
	// Given: exec fn returns plain text with no JSON array
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("The agent could not determine any actions at this time."), nil
	}

	// When: RunArchitectiAdHoc is called with dryRun=false
	_, _, _, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		false,
	)

	// Then: error is non-nil and wraps 'parse'
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("expected error to mention parse, got: %v", err)
	}
}

func TestResolveArchitectiSystemPrompt_NotFound_ReturnsError(t *testing.T) {
	// Given: HOME points to a temp dir (no SYSTEM_PROMPT.md there),
	// and sandboxRoot also points to a temp dir with no SYSTEM_PROMPT.md.
	t.Setenv("HOME", t.TempDir())

	client := newMockClient()
	s := testScheduler(client, newMockRunner(client))
	s.sandboxRoot = t.TempDir()

	// When: resolveArchitectiSystemPrompt is called
	path, err := s.resolveArchitectiSystemPrompt()

	// Then: error is non-nil and mentions SYSTEM_PROMPT.md not found; path is empty
	if err == nil {
		t.Fatalf("expected error, got nil (path=%q)", path)
	}
	if !strings.Contains(err.Error(), "SYSTEM_PROMPT.md not found") {
		t.Errorf("error %q does not contain \"SYSTEM_PROMPT.md not found\"", err.Error())
	}
}


func TestRunArchitectiAdHoc_Normal_ReturnsFilteredActions_MaxFilesPerRun(t *testing.T) {
	// Given: LLM returns more file actions than MaxFilesPerRun allows
	client := newMockClient()
	s := testSchedulerWithArchitecti(client)

	// Build output with 4 file actions; MaxFilesPerRun in testSchedulerWithArchitecti is 3
	agentOutput := `[` +
		`{"action":"file","repo":"r","title":"t1","reason":"r1"},` +
		`{"action":"file","repo":"r","title":"t2","reason":"r2"},` +
		`{"action":"file","repo":"r","title":"t3","reason":"r3"},` +
		`{"action":"file","repo":"r","title":"t4","reason":"r4"}` +
		`]`
	s.architectiExecFn = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(agentOutput), nil
	}

	// When: RunArchitectiAdHoc dispatches (non-dry-run)
	_, rawOutput, actions, err := s.RunArchitectiAdHoc(
		context.Background(),
		pooledDroplet("d-001", 60*time.Minute),
		*s.config.Architecti,
		false,
	)

	// Then: rawOutput is unfiltered (4 actions), returned actions are filtered (≤MaxFilesPerRun)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rawOutput) == 0 {
		t.Fatal("expected non-empty rawOutput")
	}
	maxFiles := s.config.Architecti.MaxFilesPerRun
	if len(actions) != maxFiles {
		t.Errorf("len(actions) = %d, want %d (capped by MaxFilesPerRun; rawOutput had 4)", len(actions), maxFiles)
	}
}
