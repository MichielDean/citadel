package castellarius

import (
	"testing"
	"time"
)

func TestDispatchLoopTracker_RecentFailureCount(t *testing.T) {
	tracker := newDispatchLoopTracker()

	// No failures yet.
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}

	// Record failures below threshold.
	for range 4 {
		tracker.recordFailure("drop1")
	}
	if n := tracker.recentFailureCount("drop1"); n != 4 {
		t.Fatalf("expected 4, got %d", n)
	}

	// Record one more — now at threshold.
	tracker.recordFailure("drop1")
	if n := tracker.recentFailureCount("drop1"); n != 5 {
		t.Fatalf("expected 5, got %d", n)
	}
}

func TestDispatchLoopTracker_Reset(t *testing.T) {
	tracker := newDispatchLoopTracker()

	tracker.recordFailure("drop1")
	tracker.recordFailure("drop1")
	tracker.incrementFix("drop1")

	tracker.reset("drop1")

	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 failures after reset, got %d", n)
	}
	// Fix count should also be gone — incrementFix should return 1 again.
	if n := tracker.incrementFix("drop1"); n != 1 {
		t.Fatalf("expected fix count 1 after reset, got %d", n)
	}
}

func TestDispatchLoopTracker_ResetFailuresKeepsFixCount(t *testing.T) {
	tracker := newDispatchLoopTracker()

	tracker.recordFailure("drop1")
	tracker.incrementFix("drop1") // fix count = 1

	tracker.resetFailures("drop1")

	// Failures cleared.
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 failures after resetFailures, got %d", n)
	}
	// Fix count preserved — next increment should return 2.
	if n := tracker.incrementFix("drop1"); n != 2 {
		t.Fatalf("expected fix count 2 after resetFailures + increment, got %d", n)
	}
}

func TestDispatchLoopTracker_StaleFailuresIgnored(t *testing.T) {
	tracker := newDispatchLoopTracker()

	// Inject a stale failure by directly appending to the map.
	tracker.mu.Lock()
	tracker.failures["drop1"] = []time.Time{
		time.Now().Add(-3 * time.Minute),
	}
	tracker.mu.Unlock()

	// The stale failure is outside the window — should not count.
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("expected 0 (stale failure outside window), got %d", n)
	}
}

func TestDispatchLoopTracker_IncrementFix(t *testing.T) {
	tracker := newDispatchLoopTracker()

	if n := tracker.incrementFix("drop1"); n != 1 {
		t.Fatalf("expected 1, got %d", n)
	}
	if n := tracker.incrementFix("drop1"); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
	if n := tracker.incrementFix("drop1"); n != 3 {
		t.Fatalf("expected 3, got %d", n)
	}
}

func TestDispatchLoopTracker_IndependentDroplets(t *testing.T) {
	tracker := newDispatchLoopTracker()

	for range 5 {
		tracker.recordFailure("drop1")
	}
	tracker.recordFailure("drop2")

	if n := tracker.recentFailureCount("drop1"); n != 5 {
		t.Fatalf("drop1: expected 5, got %d", n)
	}
	if n := tracker.recentFailureCount("drop2"); n != 1 {
		t.Fatalf("drop2: expected 1, got %d", n)
	}

	tracker.reset("drop1")
	if n := tracker.recentFailureCount("drop1"); n != 0 {
		t.Fatalf("drop1: expected 0 after reset, got %d", n)
	}
	if n := tracker.recentFailureCount("drop2"); n != 1 {
		t.Fatalf("drop2: should be unaffected by drop1 reset, got %d", n)
	}
}
