// Package delivery provides an HTTP handler for the droplet ingestion endpoint
// with configurable per-IP and per-token rate limiting.
package delivery

import (
	"sync"
	"time"
)

// Config holds thresholds for the rate limiter.
// Zero values use the defaults listed in each field comment.
type Config struct {
	PerIPRequests    int           // max requests per window per IP (default: 60)
	PerTokenRequests int           // max requests per window per token (default: 120)
	Window           time.Duration // sliding window duration (default: 1 minute)
}

func (c *Config) applyDefaults() {
	if c.PerIPRequests <= 0 {
		c.PerIPRequests = 60
	}
	if c.PerTokenRequests <= 0 {
		c.PerTokenRequests = 120
	}
	if c.Window <= 0 {
		c.Window = time.Minute
	}
}

// RateLimiter enforces per-IP and per-token sliding-window request limits.
// It is safe for concurrent use. Call Close when the limiter is no longer
// needed to stop the background eviction goroutine.
type RateLimiter struct {
	mu          sync.Mutex
	ipCounters  map[string]*windowCounter
	tokCounters map[string]*windowCounter
	cfg         Config
	now         func() time.Time // injectable for testing
	done        chan struct{}
}

// NewRateLimiter returns a RateLimiter with the given config.
// Zero-value fields in cfg receive their defaults.
// Call Close to stop the background eviction goroutine.
func NewRateLimiter(cfg Config) *RateLimiter {
	cfg.applyDefaults()
	rl := &RateLimiter{
		ipCounters:  make(map[string]*windowCounter),
		tokCounters: make(map[string]*windowCounter),
		cfg:         cfg,
		now:         time.Now,
		done:        make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Close stops the background eviction goroutine.
func (rl *RateLimiter) Close() {
	close(rl.done)
}

// cleanupLoop periodically evicts stale counters to bound memory usage.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cfg.Window)
	defer ticker.Stop()
	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			rl.evictExpired()
		}
	}
}

// evictExpired removes counters whose sliding window contains no active
// timestamps. This bounds map growth from rotating IPs/tokens that sent
// requests in a prior window but have since gone quiet.
func (rl *RateLimiter) evictExpired() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	w := rl.cfg.Window
	for ip, c := range rl.ipCounters {
		c.pruneAndCount(now, w)
		if len(c.times) == 0 {
			delete(rl.ipCounters, ip)
		}
	}
	for tok, c := range rl.tokCounters {
		c.pruneAndCount(now, w)
		if len(c.times) == 0 {
			delete(rl.tokCounters, tok)
		}
	}
}

// Allow reports whether the request from ip using token is within both limits.
// When true, counters for ip and token are incremented atomically — i.e., only
// when both checks pass, so a rejected request never partially increments a counter.
func (rl *RateLimiter) Allow(ip, token string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	w := rl.cfg.Window

	ipC := rl.counter(rl.ipCounters, ip)
	tokC := rl.counter(rl.tokCounters, token)

	ipCount := ipC.pruneAndCount(now, w)
	tokCount := tokC.pruneAndCount(now, w)

	if ipCount >= rl.cfg.PerIPRequests || tokCount >= rl.cfg.PerTokenRequests {
		// Evict entries whose window has expired so that rotating-IP/token
		// adversaries cannot cause unbounded map growth.
		if len(ipC.times) == 0 {
			delete(rl.ipCounters, ip)
		}
		if len(tokC.times) == 0 {
			delete(rl.tokCounters, token)
		}
		return false
	}

	ipC.record(now)
	tokC.record(now)
	return true
}

// Window returns the configured sliding window duration.
func (rl *RateLimiter) Window() time.Duration {
	return rl.cfg.Window
}

// counter returns an existing windowCounter for key, creating one if absent.
func (rl *RateLimiter) counter(m map[string]*windowCounter, key string) *windowCounter {
	c, ok := m[key]
	if !ok {
		c = &windowCounter{}
		m[key] = c
	}
	return c
}

// windowCounter tracks request timestamps within a sliding time window.
type windowCounter struct {
	times []time.Time
}

// pruneAndCount removes timestamps older than window and returns the remaining count.
// It does not record a new timestamp; call record separately.
func (c *windowCounter) pruneAndCount(now time.Time, window time.Duration) int {
	cutoff := now.Add(-window)
	i := 0
	for i < len(c.times) && c.times[i].Before(cutoff) {
		i++
	}
	c.times = c.times[i:]
	return len(c.times)
}

// record appends now to the counter.
func (c *windowCounter) record(now time.Time) {
	c.times = append(c.times, now)
}
