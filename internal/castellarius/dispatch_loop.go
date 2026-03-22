package castellarius

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

const (
	dispatchLoopWindow    = 2 * time.Minute
	dispatchLoopThreshold = 5
	dispatchMaxSelfFix    = 3
)

// dispatchLoopTracker tracks dispatch failures per droplet to detect and recover
// from tight dispatch loops where a droplet repeatedly fails without spawning an agent.
type dispatchLoopTracker struct {
	mu       sync.Mutex
	failures map[string][]time.Time // dropletID → recent failure timestamps
	fixes    map[string]int         // dropletID → number of self-fix attempts
}

func newDispatchLoopTracker() *dispatchLoopTracker {
	return &dispatchLoopTracker{
		failures: make(map[string][]time.Time),
		fixes:    make(map[string]int),
	}
}

// recordFailure records a dispatch failure for the given droplet.
func (t *dispatchLoopTracker) recordFailure(dropletID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures[dropletID] = append(t.failures[dropletID], time.Now())
}

// reset clears all tracking state for a droplet. Called on successful agent spawn.
func (t *dispatchLoopTracker) reset(dropletID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, dropletID)
	delete(t.fixes, dropletID)
}

// resetFailures clears failure history while preserving the fix count.
// Called after a recovery attempt so the loop can be re-detected if recovery did not help.
func (t *dispatchLoopTracker) resetFailures(dropletID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, dropletID)
}

// recentFailureCount returns the number of failures recorded within dispatchLoopWindow.
func (t *dispatchLoopTracker) recentFailureCount(dropletID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-dispatchLoopWindow)
	var n int
	for _, ts := range t.failures[dropletID] {
		if ts.After(cutoff) {
			n++
		}
	}
	return n
}

// incrementFix increments and returns the self-fix attempt count for a droplet.
func (t *dispatchLoopTracker) incrementFix(dropletID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fixes[dropletID]++
	return t.fixes[dropletID]
}

// recoverDispatchLoop attempts ordered recovery for a droplet stuck in a dispatch loop.
// Recovery is ordered by invasiveness:
//
//  1. Dirty worktree → hard-reset + clean
//  2. Missing or corrupt worktree → remove + recreate
//  3. Persistent failure after dispatchMaxSelfFix attempts → escalate to stagnant
func (s *Castellarius) recoverDispatchLoop(client CisternClient, item *cistern.Droplet, repo aqueduct.RepoConfig) {
	if s.sandboxRoot == "" {
		return
	}

	fixAttempt := s.dispatchLoop.incrementFix(item.ID)

	primaryDir := filepath.Join(s.sandboxRoot, repo.Name, "_primary")
	worktreePath := filepath.Join(s.sandboxRoot, repo.Name, item.ID)

	if fixAttempt > dispatchMaxSelfFix {
		reason := fmt.Sprintf("dispatch-loop: stuck after %d self-fix attempts — manual intervention required", dispatchMaxSelfFix)
		s.logger.Error("dispatch-loop recovery: escalating after max self-fix attempts",
			"droplet", item.ID,
		)
		_ = client.AddNote(item.ID, "dispatch-loop", reason)
		if err := client.Escalate(item.ID, reason); err != nil {
			s.logger.Error("dispatch-loop recovery: escalate failed", "droplet", item.ID, "error", err)
		}
		s.dispatchLoop.reset(item.ID)
		return
	}

	// After any non-escalation recovery path, clear the failure window so the
	// loop can be re-detected if recovery did not help.
	defer s.dispatchLoop.resetFailures(item.ID)

	attempt := fmt.Sprintf("%d/%d", fixAttempt, dispatchMaxSelfFix)

	// Recovery 1: dirty worktree — reset and clean.
	if _, err := os.Stat(worktreePath); err == nil {
		dirtyFiles, dirtyCheckErr := dirtyNonContextFiles(worktreePath)
		if dirtyCheckErr != nil {
			s.logger.Warn("dispatch-loop recovery: dirty check failed — skipping dirty recovery",
				"droplet", item.ID, "error", dirtyCheckErr)
		} else if len(dirtyFiles) > 0 {
			s.logger.Info("dispatch-loop recovery: dirty worktree — resetting",
				"droplet", item.ID,
				"attempt", attempt,
			)
			reset := exec.Command("git", "reset", "--hard", "HEAD")
			reset.Dir = worktreePath
			_ = reset.Run()
			clean := exec.Command("git", "clean", "-fd")
			clean.Dir = worktreePath
			_ = clean.Run()
			_ = client.AddNote(item.ID, "dispatch-loop",
				fmt.Sprintf("dispatch-loop recovery: %s — dirty worktree reset (attempt %s)",
					item.ID, attempt))
			return
		}
	}

	// Recovery 2: missing or corrupt worktree — remove and recreate.
	if !worktreeRegistered(primaryDir, worktreePath) {
		s.logger.Info("dispatch-loop recovery: worktree missing/corrupt — recreating",
			"droplet", item.ID,
			"attempt", attempt,
		)
		removeDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID)
		if _, err := prepareDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID); err != nil {
			s.logger.Error("dispatch-loop recovery: recreate worktree failed",
				"droplet", item.ID, "error", err)
		}
		_ = client.AddNote(item.ID, "dispatch-loop",
			fmt.Sprintf("dispatch-loop recovery: %s — worktree recreated (attempt %s)",
				item.ID, attempt))
		return
	}

	// No applicable recovery — if the loop persists, fixAttempt will eventually
	// exceed dispatchMaxSelfFix and escalate.
	s.logger.Warn("dispatch-loop recovery: no applicable recovery found",
		"droplet", item.ID,
		"attempt", attempt,
	)
	_ = client.AddNote(item.ID, "dispatch-loop",
		fmt.Sprintf("dispatch-loop recovery: %s — no applicable recovery (attempt %s), will retry",
			item.ID, attempt))
}

// worktreeRegistered returns true if worktreePath is listed in git worktree list for primaryDir.
func worktreeRegistered(primaryDir, worktreePath string) bool {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = primaryDir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), worktreePath)
}
