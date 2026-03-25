package castellarius

import (
	"sync"
	"time"
)

const (
	// defaultQuickExitThreshold is the session duration below which a death
	// without an outcome is treated as a provider-side failure (auth failure,
	// binary not found, rate limit, etc.).
	defaultQuickExitThreshold = 30 * time.Second

	// defaultMaxBackoff is the upper bound for per-droplet exponential backoff.
	defaultMaxBackoff = 30 * time.Minute

	// providerDegradedWindow is the rolling window within which quick-exit
	// events are counted when detecting a provider-wide incident.
	providerDegradedWindow = 5 * time.Minute

	// providerDegradedThreshold is the minimum number of quick-exit events
	// that must occur within providerDegradedWindow across multiple aqueducts
	// to trigger provider-wide degradation.
	providerDegradedThreshold = 3

	// providerDegradedMinAqueducts is the minimum number of distinct aqueducts
	// that must report quick exits to confirm a provider-wide incident (as
	// opposed to a single aqueduct with a bad token).
	providerDegradedMinAqueducts = 2

	// providerDegradedLogInterval limits how often the "provider degraded"
	// status message is emitted during an ongoing incident, to avoid flooding
	// the log with per-droplet noise.
	providerDegradedLogInterval = 1 * time.Minute
)

// providerEvent records a single quick-exit for a provider.
type providerEvent struct {
	at           time.Time
	aqueductName string
}

// quickExitTracker tracks per-droplet exponential backoff state and
// provider-level degradation detection.
//
// All public methods are safe for concurrent use.
type quickExitTracker struct {
	mu sync.Mutex

	// Per-droplet.
	dropletExits        map[string]int       // dropletID → consecutive quick-exit count
	dropletBackoffUntil map[string]time.Time // dropletID → when backoff expires

	// Per-provider event log for degradation detection.
	providerEvents   map[string][]providerEvent // provider → recent events (within window)
	providerDegraded map[string]bool            // provider → currently degraded
	providerLastLog  map[string]time.Time       // provider → last degraded-log timestamp

	// Configuration.
	quickExitThreshold time.Duration
	maxBackoff         time.Duration
}

// newQuickExitTracker returns an initialised tracker. Zero values for threshold
// or maxBackoff fall back to the defaults.
func newQuickExitTracker(quickExitThreshold, maxBackoff time.Duration) *quickExitTracker {
	if quickExitThreshold <= 0 {
		quickExitThreshold = defaultQuickExitThreshold
	}
	if maxBackoff <= 0 {
		maxBackoff = defaultMaxBackoff
	}
	return &quickExitTracker{
		dropletExits:        make(map[string]int),
		dropletBackoffUntil: make(map[string]time.Time),
		providerEvents:      make(map[string][]providerEvent),
		providerDegraded:    make(map[string]bool),
		providerLastLog:     make(map[string]time.Time),
		quickExitThreshold:  quickExitThreshold,
		maxBackoff:          maxBackoff,
	}
}

// isQuickExit reports whether a session duration qualifies as a quick exit.
func (t *quickExitTracker) isQuickExit(d time.Duration) bool {
	return d <= t.quickExitThreshold
}

// computeBackoff returns the exponential backoff delay for n consecutive quick
// exits: threshold * 2^(n-1), capped at maxBackoff.
//   - n=1 → threshold (e.g. 30s)
//   - n=2 → 2×threshold (e.g. 60s)
//   - n=3 → 4×threshold (e.g. 2m)
//   - …
func (t *quickExitTracker) computeBackoff(n int) time.Duration {
	if n <= 0 {
		n = 1
	}
	// Cap the shift at 62 to avoid int64 overflow in Duration arithmetic.
	shift := n - 1
	if shift > 62 {
		shift = 62
	}
	delay := t.quickExitThreshold * time.Duration(uint64(1)<<shift)
	if delay > t.maxBackoff || delay <= 0 { // <= 0 catches both negative and zero-wrap overflow
		delay = t.maxBackoff
	}
	return delay
}

// pruneProviderEvents removes events outside providerDegradedWindow from the
// provider's event slice. Must be called with t.mu held.
func (t *quickExitTracker) pruneProviderEvents(provider string) {
	cutoff := time.Now().Add(-providerDegradedWindow)
	events := t.providerEvents[provider]
	n := 0
	for _, e := range events {
		if e.at.After(cutoff) {
			events[n] = e
			n++
		}
	}
	t.providerEvents[provider] = events[:n]
}

// checkProviderDegradedLocked returns true if the provider's recent event log
// meets the degradation criteria. Must be called with t.mu held.
func (t *quickExitTracker) checkProviderDegradedLocked(provider string) bool {
	events := t.providerEvents[provider]
	if len(events) < providerDegradedThreshold {
		return false
	}
	aqueducts := make(map[string]bool, len(events))
	for _, e := range events {
		aqueducts[e.aqueductName] = true
	}
	return len(aqueducts) >= providerDegradedMinAqueducts
}

// recordQuickExit records a quick-exit for dropletID on provider/aqueductName.
// Returns:
//   - backoffDelay: the new per-droplet backoff duration
//   - justDegraded: true when the provider just crossed the degradation threshold
//
// When the provider is already degraded (or just became so), maxBackoff is
// applied to the droplet instead of the normal exponential ramp.
func (t *quickExitTracker) recordQuickExit(dropletID, provider, aqueductName string) (backoffDelay time.Duration, justDegraded bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Update the per-droplet consecutive exit count.
	t.dropletExits[dropletID]++
	n := t.dropletExits[dropletID]

	// Update the provider event log (prune stale entries first, then append).
	t.pruneProviderEvents(provider)
	t.providerEvents[provider] = append(t.providerEvents[provider], providerEvent{
		at:           time.Now(),
		aqueductName: aqueductName,
	})

	// Detect provider degradation on this event (newly degraded?).
	if !t.providerDegraded[provider] && t.checkProviderDegradedLocked(provider) {
		t.providerDegraded[provider] = true
		justDegraded = true
	}

	// Apply backoff: max if provider is degraded, exponential otherwise.
	if t.providerDegraded[provider] {
		backoffDelay = t.maxBackoff
	} else {
		backoffDelay = t.computeBackoff(n)
	}
	t.dropletBackoffUntil[dropletID] = time.Now().Add(backoffDelay)

	return backoffDelay, justDegraded
}

// consecutiveExits returns the number of consecutive quick exits recorded for
// the droplet. Returns 0 for unknown droplets.
func (t *quickExitTracker) consecutiveExits(dropletID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropletExits[dropletID]
}

// currentBackoff returns the remaining backoff duration for dropletID.
// Returns 0 when no backoff is active or the backoff has already expired.
func (t *quickExitTracker) currentBackoff(dropletID string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	remaining := time.Until(t.dropletBackoffUntil[dropletID])
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// isProviderDegraded reports whether the provider is currently flagged as
// degraded (i.e. the degradation threshold was recently met and no successful
// session has cleared it yet).
func (t *quickExitTracker) isProviderDegraded(provider string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.providerDegraded[provider]
}

// fastForwardToMaxBackoff sets the droplet's backoff expiry to maxBackoff from
// now. Called when a provider-wide incident is detected so that a droplet
// already in a short backoff window is immediately promoted to max, skipping
// the incremental ramp-up.
func (t *quickExitTracker) fastForwardToMaxBackoff(dropletID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	maxUntil := time.Now().Add(t.maxBackoff)
	if t.dropletBackoffUntil[dropletID].Before(maxUntil) {
		t.dropletBackoffUntil[dropletID] = maxUntil
	}
}

// resetDroplet clears all per-droplet backoff state on successful session
// completion. If the provider was degraded, the first successful reset clears
// the global degradation state and returns true (signals provider recovery to
// the caller for logging).
func (t *quickExitTracker) resetDroplet(dropletID, provider string) (providerRecovered bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.dropletExits, dropletID)
	delete(t.dropletBackoffUntil, dropletID)
	if t.providerDegraded[provider] {
		delete(t.providerDegraded, provider)
		delete(t.providerEvents, provider)
		delete(t.providerLastLog, provider)
		return true
	}
	return false
}

// shouldLogAndMarkProviderDegraded returns true if a "provider degraded" log
// line should be emitted for this provider and, if so, atomically records the
// current time as the last-log timestamp. Subsequent calls within
// providerDegradedLogInterval return false, preventing per-droplet noise.
func (t *quickExitTracker) shouldLogAndMarkProviderDegraded(provider string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.providerLastLog[provider]) < providerDegradedLogInterval {
		return false
	}
	t.providerLastLog[provider] = time.Now()
	return true
}
