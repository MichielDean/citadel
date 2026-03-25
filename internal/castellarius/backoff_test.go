package castellarius

import (
	"fmt"
	"testing"
	"time"
)

// TestQuickExitTracker_ComputeBackoff_ExponentialRamp verifies the doubling
// sequence: threshold, 2×, 4×, 8×… up to maxBackoff.
func TestQuickExitTracker_ComputeBackoff_ExponentialRamp(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	tests := []struct {
		n    int
		want time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 2 * time.Minute},
		{4, 4 * time.Minute},
		{5, 8 * time.Minute},
		{6, 16 * time.Minute},
		{7, 30 * time.Minute}, // 32m would exceed max — capped
	}

	for _, tt := range tests {
		got := tracker.computeBackoff(tt.n)
		if got != tt.want {
			t.Errorf("computeBackoff(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

// TestQuickExitTracker_ComputeBackoff_CapsAtMax confirms that all values beyond
// the cap always return maxBackoff.
func TestQuickExitTracker_ComputeBackoff_CapsAtMax(t *testing.T) {
	maxBackoff := 10 * time.Minute
	tracker := newQuickExitTracker(30*time.Second, maxBackoff)

	for n := 7; n <= 10; n++ {
		got := tracker.computeBackoff(n)
		if got != maxBackoff {
			t.Errorf("computeBackoff(%d) = %v, want maxBackoff %v", n, got, maxBackoff)
		}
	}
}

// TestQuickExitTracker_IsQuickExit verifies the threshold boundary.
func TestQuickExitTracker_IsQuickExit_ThresholdBoundary(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	if !tracker.isQuickExit(0) {
		t.Error("0 duration should be a quick exit")
	}
	if !tracker.isQuickExit(29 * time.Second) {
		t.Error("29s should be a quick exit")
	}
	if !tracker.isQuickExit(30 * time.Second) {
		t.Error("exactly at threshold (30s) should be a quick exit")
	}
	if tracker.isQuickExit(31 * time.Second) {
		t.Error("31s should not be a quick exit")
	}
}

// TestQuickExitTracker_RecordQuickExit_SetsExponentialBackoff verifies that
// each successive quick exit doubles the backoff delay.
func TestQuickExitTracker_RecordQuickExit_SetsExponentialBackoff(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// First exit: 30s backoff.
	delay1, justDegraded1 := tracker.recordQuickExit("drop1", "claude", "alpha")
	if delay1 != 30*time.Second {
		t.Errorf("exit 1: want 30s delay, got %v", delay1)
	}
	if justDegraded1 {
		t.Error("single exit on single aqueduct should not trigger provider degradation")
	}

	// Second exit: 60s backoff.
	delay2, _ := tracker.recordQuickExit("drop1", "claude", "alpha")
	if delay2 != 60*time.Second {
		t.Errorf("exit 2: want 60s delay, got %v", delay2)
	}

	// Third exit: 2m backoff.
	delay3, _ := tracker.recordQuickExit("drop1", "claude", "alpha")
	if delay3 != 2*time.Minute {
		t.Errorf("exit 3: want 2m delay, got %v", delay3)
	}
}

// TestQuickExitTracker_CurrentBackoff_ReflectsActiveWindow confirms that
// currentBackoff returns a positive value right after a quick exit, and zero
// for droplets with no recorded exits.
func TestQuickExitTracker_CurrentBackoff_ReflectsActiveWindow(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	if remaining := tracker.currentBackoff("unknown"); remaining != 0 {
		t.Errorf("unknown droplet: want 0, got %v", remaining)
	}

	tracker.recordQuickExit("drop1", "claude", "alpha")
	remaining := tracker.currentBackoff("drop1")
	if remaining <= 0 || remaining > 30*time.Second {
		t.Errorf("after first exit: expected backoff in (0, 30s], got %v", remaining)
	}
}

// TestQuickExitTracker_CurrentBackoff_ReturnsZeroWhenExpired verifies that an
// expired backoff is treated as inactive.
func TestQuickExitTracker_CurrentBackoff_ReturnsZeroWhenExpired(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Inject an already-expired backoff directly.
	tracker.mu.Lock()
	tracker.dropletExits["drop1"] = 1
	tracker.dropletBackoffUntil["drop1"] = time.Now().Add(-1 * time.Second)
	tracker.mu.Unlock()

	if remaining := tracker.currentBackoff("drop1"); remaining != 0 {
		t.Errorf("expired backoff should return 0, got %v", remaining)
	}
}

// TestQuickExitTracker_ConsecutiveExits checks the exit counter.
func TestQuickExitTracker_ConsecutiveExits_TracksCount(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	if n := tracker.consecutiveExits("drop1"); n != 0 {
		t.Errorf("unknown droplet: want 0, got %d", n)
	}

	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop1", "claude", "alpha")
	if n := tracker.consecutiveExits("drop1"); n != 2 {
		t.Errorf("after 2 exits: want 2, got %d", n)
	}
}

// TestQuickExitTracker_ResetDroplet_ClearsPerDropletState verifies reset.
func TestQuickExitTracker_ResetDroplet_ClearsPerDropletState(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop1", "claude", "alpha")

	recovered := tracker.resetDroplet("drop1", "claude")
	if recovered {
		t.Error("no provider degradation — recovered should be false")
	}
	if n := tracker.consecutiveExits("drop1"); n != 0 {
		t.Errorf("expected 0 consecutive exits after reset, got %d", n)
	}
	if remaining := tracker.currentBackoff("drop1"); remaining != 0 {
		t.Errorf("expected 0 backoff after reset, got %v", remaining)
	}
}

// TestQuickExitTracker_IndependentDroplets confirms that per-droplet state is
// isolated — resetting one droplet does not affect others.
func TestQuickExitTracker_IndependentDroplets(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop2", "claude", "beta")

	if n := tracker.consecutiveExits("drop1"); n != 2 {
		t.Errorf("drop1: expected 2 exits, got %d", n)
	}
	if n := tracker.consecutiveExits("drop2"); n != 1 {
		t.Errorf("drop2: expected 1 exit, got %d", n)
	}

	tracker.resetDroplet("drop1", "claude")
	if n := tracker.consecutiveExits("drop1"); n != 0 {
		t.Errorf("drop1: expected 0 after reset, got %d", n)
	}
	if n := tracker.consecutiveExits("drop2"); n != 1 {
		t.Errorf("drop2: should be unaffected by drop1 reset, got %d", n)
	}
}

// TestQuickExitTracker_ProviderDegradation_DetectedAtThreshold verifies that
// the provider is flagged when ≥3 quick exits come from ≥2 distinct aqueducts.
func TestQuickExitTracker_ProviderDegradation_DetectedAtThreshold(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Two exits from the same aqueduct — not enough distinct aqueducts.
	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop2", "claude", "alpha")
	if tracker.isProviderDegraded("claude") {
		t.Error("same-aqueduct exits should not trigger degradation")
	}

	// Third exit from a different aqueduct — meets criteria.
	_, justDegraded := tracker.recordQuickExit("drop3", "claude", "beta")
	if !tracker.isProviderDegraded("claude") {
		t.Error("should be degraded after 3 events across 2 aqueducts")
	}
	if !justDegraded {
		t.Error("justDegraded should be true on first detection")
	}
}

// TestQuickExitTracker_ProviderDegradation_RequiresMultipleAqueducts confirms
// that three exits from a single aqueduct alone do not trigger degradation.
func TestQuickExitTracker_ProviderDegradation_RequiresMultipleAqueducts(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	for i := range 3 {
		_, justDegraded := tracker.recordQuickExit(fmt.Sprintf("drop%d", i), "claude", "alpha")
		if justDegraded {
			t.Errorf("exit %d: single-aqueduct pattern should not trigger degradation", i+1)
		}
	}
	if tracker.isProviderDegraded("claude") {
		t.Error("single-aqueduct exits should not cause provider degradation")
	}
}

// TestQuickExitTracker_ProviderDegradation_MaxBackoffApplied verifies that
// once the provider degrades, the droplet that triggered it receives maxBackoff.
func TestQuickExitTracker_ProviderDegradation_MaxBackoffApplied(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// First two exits from alpha — not yet degraded.
	delay1, _ := tracker.recordQuickExit("drop1", "claude", "alpha")
	if delay1 != 30*time.Second {
		t.Errorf("pre-degradation exit 1: want 30s, got %v", delay1)
	}
	tracker.recordQuickExit("drop2", "claude", "alpha")

	// Third exit from beta — triggers degradation, max backoff applied.
	delay3, justDegraded := tracker.recordQuickExit("drop3", "claude", "beta")
	if !justDegraded {
		t.Fatal("expected justDegraded=true on triggering exit")
	}
	if delay3 != 30*time.Minute {
		t.Errorf("on degradation: want maxBackoff 30m, got %v", delay3)
	}
}

// TestQuickExitTracker_ProviderDegradation_AlreadyDegradedUsesMaxBackoff
// verifies that new exits when provider is already degraded receive maxBackoff.
func TestQuickExitTracker_ProviderDegradation_AlreadyDegradedUsesMaxBackoff(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Trigger degradation.
	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop2", "claude", "alpha")
	tracker.recordQuickExit("drop3", "claude", "beta")

	// New exit when already degraded — should return max immediately.
	delay, justDegraded := tracker.recordQuickExit("drop4", "claude", "gamma")
	if justDegraded {
		t.Error("provider was already degraded — justDegraded should be false")
	}
	if delay != 30*time.Minute {
		t.Errorf("already-degraded: want maxBackoff 30m, got %v", delay)
	}
}

// TestQuickExitTracker_FastForwardToMaxBackoff verifies that fast-forward
// extends a short backoff to the full max.
func TestQuickExitTracker_FastForwardToMaxBackoff_ExtendsShortBackoff(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	tracker.recordQuickExit("drop1", "claude", "alpha")
	initial := tracker.currentBackoff("drop1")
	if initial > 30*time.Second {
		t.Fatalf("expected ≤30s initial backoff, got %v", initial)
	}

	tracker.fastForwardToMaxBackoff("drop1")
	remaining := tracker.currentBackoff("drop1")
	if remaining < 29*time.Minute {
		t.Errorf("after fast-forward: expected ~30m remaining, got %v", remaining)
	}
}

// TestQuickExitTracker_FastForwardToMaxBackoff_NoOpWhenAlreadyAtMax confirms
// that a droplet already at max backoff is not modified.
func TestQuickExitTracker_FastForwardToMaxBackoff_NoOpWhenAlreadyAtMax(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	tracker.mu.Lock()
	maxUntil := time.Now().Add(30 * time.Minute)
	tracker.dropletBackoffUntil["drop1"] = maxUntil
	tracker.mu.Unlock()

	tracker.fastForwardToMaxBackoff("drop1")

	remaining := tracker.currentBackoff("drop1")
	// Should still be ≤ maxBackoff (not extended beyond it).
	if remaining > 30*time.Minute {
		t.Errorf("fast-forward should not exceed maxBackoff, got %v", remaining)
	}
}

// TestQuickExitTracker_ProviderRecovery_ClearsOnFirstSuccess verifies that the
// first successful session completion clears provider degradation.
func TestQuickExitTracker_ProviderRecovery_ClearsOnFirstSuccess(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Trigger degradation.
	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop2", "claude", "alpha")
	tracker.recordQuickExit("drop3", "claude", "beta")

	if !tracker.isProviderDegraded("claude") {
		t.Fatal("provider should be degraded after threshold")
	}

	// Successful session clears degradation.
	recovered := tracker.resetDroplet("drop3", "claude")
	if !recovered {
		t.Error("resetDroplet should return true (providerRecovered) on first success")
	}
	if tracker.isProviderDegraded("claude") {
		t.Error("provider should be recovered after successful session")
	}
}

// TestQuickExitTracker_ProviderRecovery_ReturnsOtherProviderUntouched checks
// that recovering one provider does not affect others.
func TestQuickExitTracker_ProviderRecovery_ReturnsOtherProviderUntouched(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Degrade both providers.
	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop2", "claude", "alpha")
	tracker.recordQuickExit("drop3", "claude", "beta")

	tracker.recordQuickExit("drop4", "codex", "alpha")
	tracker.recordQuickExit("drop5", "codex", "alpha")
	tracker.recordQuickExit("drop6", "codex", "beta")

	// Recover claude only.
	tracker.resetDroplet("drop3", "claude")

	if tracker.isProviderDegraded("claude") {
		t.Error("claude should be recovered")
	}
	if !tracker.isProviderDegraded("codex") {
		t.Error("codex should still be degraded")
	}
}

// TestQuickExitTracker_ProviderDegradation_StaleEventsIgnored verifies that
// events outside providerDegradedWindow are pruned and do not count.
func TestQuickExitTracker_ProviderDegradation_StaleEventsIgnored(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Inject stale events (outside providerDegradedWindow) directly.
	staleTime := time.Now().Add(-10 * time.Minute)
	tracker.mu.Lock()
	tracker.providerEvents["claude"] = []providerEvent{
		{at: staleTime, aqueductName: "alpha"},
		{at: staleTime, aqueductName: "alpha"},
		{at: staleTime, aqueductName: "beta"},
	}
	tracker.mu.Unlock()

	// A single fresh event — stale events are pruned first, so threshold is not met.
	_, justDegraded := tracker.recordQuickExit("drop1", "claude", "gamma")
	if justDegraded {
		t.Error("stale events should not count toward degradation threshold")
	}
	if tracker.isProviderDegraded("claude") {
		t.Error("stale events should not cause provider degradation")
	}
}

// TestQuickExitTracker_ShouldLogAndMarkProviderDegraded_RateLimits verifies
// that the log-rate guard allows exactly one message per interval.
func TestQuickExitTracker_ShouldLogAndMarkProviderDegraded_RateLimits(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// First call with no prior entry: should permit logging.
	if !tracker.shouldLogAndMarkProviderDegraded("claude") {
		t.Error("first call: expected true (no prior log)")
	}

	// Immediate second call: within interval, should suppress.
	if tracker.shouldLogAndMarkProviderDegraded("claude") {
		t.Error("second call within interval: expected false (rate-limited)")
	}
}

// TestQuickExitTracker_DifferentProviders_Isolated confirms that events for
// one provider do not affect another provider's state.
func TestQuickExitTracker_DifferentProviders_Isolated(t *testing.T) {
	tracker := newQuickExitTracker(30*time.Second, 30*time.Minute)

	// Degrade claude.
	tracker.recordQuickExit("drop1", "claude", "alpha")
	tracker.recordQuickExit("drop2", "claude", "alpha")
	tracker.recordQuickExit("drop3", "claude", "beta")

	if !tracker.isProviderDegraded("claude") {
		t.Error("claude should be degraded")
	}
	if tracker.isProviderDegraded("codex") {
		t.Error("codex should be unaffected by claude events")
	}
	if tracker.isProviderDegraded("gemini") {
		t.Error("gemini should be unaffected by claude events")
	}
}

// TestQuickExitTracker_NewQuickExitTracker_DefaultsFallback verifies that zero
// threshold/maxBackoff resolve to the package defaults.
func TestQuickExitTracker_NewQuickExitTracker_DefaultsFallback(t *testing.T) {
	tracker := newQuickExitTracker(0, 0)

	if tracker.quickExitThreshold != defaultQuickExitThreshold {
		t.Errorf("want default threshold %v, got %v", defaultQuickExitThreshold, tracker.quickExitThreshold)
	}
	if tracker.maxBackoff != defaultMaxBackoff {
		t.Errorf("want default max backoff %v, got %v", defaultMaxBackoff, tracker.maxBackoff)
	}
}
