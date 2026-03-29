package castellarius

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// newDrainScheduler creates a Castellarius configured for fast drain tests.
func newDrainScheduler(client *mockClient, runner CataractaeRunner, drainTimeout time.Duration, buf *bytes.Buffer) *Castellarius {
	config := testConfig()
	workflows := map[string]*aqueduct.Workflow{"test-repo": testWorkflow()}
	clients := map[string]CisternClient{"test-repo": client}
	opts := []Option{
		WithPollInterval(5 * time.Millisecond),
		WithDrainTimeout(drainTimeout),
	}
	if buf != nil {
		opts = append(opts, WithLogger(newTestLogger(buf)))
	}
	return NewFromParts(config, workflows, clients, runner, opts...)
}

// waitBlockingCall waits until the blockingRunner has been entered at least once
// (meaning a session has been dispatched and is now in_progress).
func waitBlockingCall(br *blockingRunner, timeout time.Duration) bool {
	select {
	case <-br.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// TestGracefulShutdown_ListError_TreatAsInFlight verifies that when client.List
// returns an error, drainInFlight conservatively assumes sessions are still
// running (instead of treating the empty result as "no in-flight sessions") and
// continues draining until the timeout fires.
func TestGracefulShutdown_ListError_TreatAsInFlight(t *testing.T) {
	client := newMockClient()
	client.listErr = errors.New("DB unreachable")
	runner := newMockRunner(client)
	var buf bytes.Buffer
	sched := newDrainScheduler(client, runner, 60*time.Millisecond, &buf)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Let the scheduler start its ticker loop, then trigger shutdown.
	time.Sleep(15 * time.Millisecond)
	cancel()

	// With a persistent list error the drain must wait until timeout — not exit
	// immediately as if there are zero in-flight sessions.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out — drain did not exit after timeout with persistent list error")
	}

	output := buf.String()
	if strings.Contains(output, "Aqueducts closed") {
		t.Errorf("should not log 'Aqueducts closed' when list query fails: %q", output)
	}
	if !strings.Contains(output, "drain timeout") {
		t.Errorf("expected drain timeout log when list error persists, got: %q", output)
	}
}

// TestGracefulShutdown_TimeoutWithListError_LogsUnknownCount verifies that when
// client.List returns an error during the drain timeout path, the timeout log
// reports sessions as unknown rather than zero — preventing operators from being
// misled into thinking no sessions were running.
func TestGracefulShutdown_TimeoutWithListError_LogsUnknownCount(t *testing.T) {
	client := newMockClient()
	client.listErr = errors.New("DB unreachable")
	runner := newMockRunner(client)
	var buf bytes.Buffer
	sched := newDrainScheduler(client, runner, 60*time.Millisecond, &buf)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	time.Sleep(15 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out — drain did not exit after timeout with persistent list error")
	}

	output := buf.String()
	if !strings.Contains(output, "drain timeout") {
		t.Errorf("expected drain timeout log, got: %q", output)
	}
	if !strings.Contains(output, "unknown (query error)") {
		t.Errorf("expected 'unknown (query error)' in drain timeout log, got: %q", output)
	}
	if strings.Contains(output, "sessions=0") {
		t.Errorf("drain timeout log must not report sessions=0 when query failed, got: %q", output)
	}
}

// TestGracefulShutdown_ZeroInFlight_ExitsImmediately verifies that when there
// are no in-progress droplets at shutdown time, the Castellarius exits without
// logging a drain message.
func TestGracefulShutdown_ZeroInFlight_ExitsImmediately(t *testing.T) {
	client := newMockClient()
	runner := newMockRunner(client)
	var buf bytes.Buffer
	sched := newDrainScheduler(client, runner, 200*time.Millisecond, &buf)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Give Run a moment to start its ticker loop.
	time.Sleep(15 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out — Run did not exit after context cancel with no in-flight sessions")
	}

	output := buf.String()
	if strings.Contains(output, "draining") {
		t.Errorf("unexpected drain log when there are no in-flight sessions: %q", output)
	}
}

// TestGracefulShutdown_CleanDrain_CompletesBeforeTimeout verifies that when
// in-flight sessions signal outcomes before the drain timeout, the drain
// completes cleanly and logs the expected messages.
func TestGracefulShutdown_CleanDrain_CompletesBeforeTimeout(t *testing.T) {
	client := newMockClient()
	// Use a blocking runner so the dispatch keeps the item in_progress.
	br := newBlockingRunner()
	var buf bytes.Buffer
	sched := newDrainScheduler(client, br, 2*time.Second, &buf)

	// Queue one item for dispatch.
	client.readyItems = []*cistern.Droplet{{ID: "d1", Title: "drain-test"}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Wait until the blocking runner has Spawn called (item is now in_progress).
	if !waitBlockingCall(br, time.Second) {
		t.Fatal("timed out waiting for dispatch to pick up item")
	}

	cancel() // SIGTERM arrives — drain phase begins

	// After a brief pause, the session signals its outcome (simulating `ct droplet pass`).
	time.Sleep(30 * time.Millisecond)
	client.SetOutcome("d1", "pass")

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out — drain did not complete after session signaled outcome")
	}

	output := buf.String()
	if !strings.Contains(output, "draining") {
		t.Errorf("expected drain start log, got: %q", output)
	}
	if !strings.Contains(output, "drain complete") {
		t.Errorf("expected 'drain complete' log, got: %q", output)
	}
	if strings.Contains(output, "drain timeout") {
		t.Errorf("unexpected drain timeout log in clean drain path: %q", output)
	}
}

// TestGracefulShutdown_Timeout_ForcesExit verifies that when sessions never
// signal outcomes within the drain timeout, the Castellarius forces exit and
// logs the IDs of stuck sessions.
func TestGracefulShutdown_Timeout_ForcesExit(t *testing.T) {
	client := newMockClient()
	br := newBlockingRunner()
	var buf bytes.Buffer
	sched := newDrainScheduler(client, br, 60*time.Millisecond, &buf)

	// Queue one item that will never signal an outcome.
	client.readyItems = []*cistern.Droplet{{ID: "stuck-42", Title: "stuck-session"}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Wait until the item is dispatched (in_progress, no outcome).
	if !waitBlockingCall(br, time.Second) {
		t.Fatal("timed out waiting for dispatch")
	}

	cancel()

	// Never signal outcome — drain timeout should fire.

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out — drain timeout did not force exit")
	}

	output := buf.String()
	if !strings.Contains(output, "drain timeout") {
		t.Errorf("expected drain timeout log, got: %q", output)
	}
	if !strings.Contains(output, "stuck-42") {
		t.Errorf("expected stuck droplet ID 'stuck-42' in drain timeout log, got: %q", output)
	}
	if strings.Contains(output, "drain complete") {
		t.Errorf("unexpected 'drain complete' log in timeout path: %q", output)
	}
}

// TestDrainInFlight_SessionDrainUsesRemainingBudget verifies that the session
// drain timer uses the time remaining from drainTimeout after the architecti
// drain completes, rather than a fresh drainTimeout.  Without the fix, the two
// drains each consume a full drainTimeout and total shutdown can reach
// 2×drainTimeout.  With the fix the total is bounded to approximately one
// drainTimeout.
//
// drainTimeout = 3s, architecti goroutine takes 1.2s → remaining = 1.8s.
// The test asserts total elapsed < drainTimeout + 500ms (3.5s).  Without the
// fix the total would be ~4.2s (1.4×), which exceeds the threshold.
func TestDrainInFlight_SessionDrainUsesRemainingBudget(t *testing.T) {
	const (
		drainTimeout      = 3 * time.Second
		architectiDelay   = 1200 * time.Millisecond
		maxAllowedElapsed = drainTimeout + 500*time.Millisecond
	)

	client := newMockClient()
	br := newBlockingRunner()
	// No log buffer needed; we are only measuring elapsed time.
	sched := newDrainScheduler(client, br, drainTimeout, nil)

	// Queue one session that will never signal an outcome.
	client.readyItems = []*cistern.Droplet{{ID: "stuck-budget-test", Title: "stuck"}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Wait until the item is dispatched and in_progress.
	if !waitBlockingCall(br, time.Second) {
		t.Fatal("timed out waiting for dispatch")
	}

	// Inject an in-flight architecti goroutine that holds the WaitGroup for
	// architectiDelay.  This simulates the architecti drain consuming part of
	// the shared budget before the session drain starts.
	sched.architectiWg.Add(1)
	go func() {
		time.Sleep(architectiDelay)
		sched.architectiWg.Done()
	}()

	start := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		// With fix: total ≈ drainTimeout (architecti drain + remaining session drain).
		// Without fix: total ≈ architectiDelay + drainTimeout ≈ 1.4×drainTimeout.
		if elapsed > maxAllowedElapsed {
			t.Errorf("drain took %v, exceeds %v — architecti and session drains appear to double-count drainTimeout",
				elapsed.Round(time.Millisecond), maxAllowedElapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for drain to complete")
	}
}
