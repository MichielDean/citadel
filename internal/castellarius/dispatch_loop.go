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
// Old timestamps are pruned in place to prevent unbounded growth.
func (t *dispatchLoopTracker) recentFailureCount(dropletID string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-dispatchLoopWindow)
	all := t.failures[dropletID]
	n := 0
	for _, ts := range all {
		if ts.After(cutoff) {
			all[n] = ts
			n++
		}
	}
	t.failures[dropletID] = all[:n]
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
//  3. Persistent failure after dispatchMaxSelfFix attempts → pool (cannot proceed)
func (s *Castellarius) recoverDispatchLoop(client CisternClient, item *cistern.Droplet, repo aqueduct.RepoConfig) {
	if s.sandboxRoot == "" {
		return
	}

	fixAttempt := s.dispatchLoop.incrementFix(item.ID)

	primaryDir := filepath.Join(s.sandboxRoot, repo.Name, "_primary")
	worktreePath := filepath.Join(s.sandboxRoot, repo.Name, item.ID)

	if fixAttempt > dispatchMaxSelfFix {
		reason := fmt.Sprintf("dispatch-loop: stuck after %d self-fix attempts — manual intervention required", dispatchMaxSelfFix)
		s.logger.Error("dispatch-loop recovery: pooling after max self-fix attempts",
			"droplet", item.ID,
		)
		s.addNote(client, item.ID, "dispatch-loop", reason)
		if err := client.Pool(item.ID, reason); err != nil {
			s.logger.Error("dispatch-loop recovery: pool failed", "droplet", item.ID, "error", err)
		}
		s.dispatchLoop.reset(item.ID)
		return
	}

	// After any non-pool recovery path, clear the failure window so the
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
			resetErr := reset.Run()
			clean := exec.Command("git", "clean", "-fd")
			clean.Dir = worktreePath
			cleanErr := clean.Run()
			if resetErr != nil || cleanErr != nil {
				s.logger.Warn("dispatch-loop recovery: reset/clean failed — falling through to worktree recreation",
					"droplet", item.ID,
					"reset_err", resetErr,
					"clean_err", cleanErr,
				)
				// fall through to worktree-recreation recovery path
			} else {
				s.addNote(client, item.ID, "dispatch-loop",
					fmt.Sprintf("dispatch-loop recovery: %s — dirty worktree reset (attempt %s)",
						item.ID, attempt))
				return
			}
		}
	}

	// Recovery 2: missing or corrupt worktree — remove and recreate.
	if !worktreeRegistered(primaryDir, worktreePath) {
		branch := "feat/" + item.ID
		s.logger.Info("dispatch-loop recovery: worktree missing/corrupt — recreating",
			"droplet", item.ID,
			"attempt", attempt,
		)
		removeDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID)
		if _, err := prepareDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID); err != nil {
			if strings.Contains(err.Error(), "did not match any file(s) known to git") {
				// The feature branch no longer exists in git. Remove any lingering
				// directory left by the failed resume and try again — this time the
				// new-worktree path will create a fresh branch from origin/main.
				s.logger.Warn("dispatch-loop recovery: feature branch missing from git — creating fresh branch from origin/main",
					"droplet", item.ID, "branch", branch)
				if rmErr := os.RemoveAll(worktreePath); rmErr != nil {
					s.logger.Warn("dispatch-loop recovery: os.RemoveAll failed — skipping fresh-branch retry",
						"droplet", item.ID, "branch", branch, "error", rmErr)
					s.poolDroplet(client, item.ID,
						fmt.Sprintf("dispatch-loop recovery: could not remove stale worktree directory: %v", rmErr))
					return
				}
				if _, err2 := prepareDropletWorktree(primaryDir, s.sandboxRoot, repo.Name, item.ID); err2 != nil {
					s.poolDroplet(client, item.ID,
						fmt.Sprintf("dispatch-loop recovery: branch %s missing, fresh-branch creation failed: %v", branch, err2))
				} else {
					s.addNote(client, item.ID, "dispatch-loop",
						fmt.Sprintf("dispatch-loop recovery: %s — fresh branch created from origin/main (branch %s was missing, attempt %s)",
							item.ID, branch, attempt))
				}
			} else {
				s.logger.Error("dispatch-loop recovery: recreate worktree failed",
					"droplet", item.ID, "error", err)
				s.addNote(client, item.ID, "dispatch-loop",
					fmt.Sprintf("dispatch-loop recovery: %s — worktree recreate failed (attempt %s): %v",
						item.ID, attempt, err))
			}
			return
		}
		s.addNote(client, item.ID, "dispatch-loop",
			fmt.Sprintf("dispatch-loop recovery: %s — worktree recreated (attempt %s)",
				item.ID, attempt))
		return
	}

	// No applicable recovery — if the loop persists, fixAttempt will eventually
	// exceed dispatchMaxSelfFix and pool.
	s.logger.Warn("dispatch-loop recovery: no applicable recovery found",
		"droplet", item.ID,
		"attempt", attempt,
	)
	s.addNote(client, item.ID, "dispatch-loop",
		fmt.Sprintf("dispatch-loop recovery: %s — no applicable recovery (attempt %s), will retry",
			item.ID, attempt))
}

// poolDroplet notes, pools the droplet, and resets the
// dispatch tracker. It is a terminal operation — callers must return after it.
func (s *Castellarius) poolDroplet(client CisternClient, dropletID, reason string) {
	s.addNote(client, dropletID, "dispatch-loop", reason)
	if err := client.Pool(dropletID, reason); err != nil {
		s.logger.Error("dispatch-loop recovery: pool failed", "droplet", dropletID, "error", err)
	}
	s.dispatchLoop.reset(dropletID)
}

// worktreeInOutput reports whether out (from "git worktree list --porcelain")
// contains an exact worktree line for path. Substring matching is intentionally
// avoided to prevent false positives with prefix-sharing droplet IDs.
func worktreeInOutput(out []byte, path string) bool {
	target := "worktree " + path
	for _, line := range strings.Split(string(out), "\n") {
		if line == target {
			return true
		}
	}
	return false
}

// worktreeRegistered returns true if worktreePath is listed in git worktree list for primaryDir.
func worktreeRegistered(primaryDir, worktreePath string) bool {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = primaryDir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return worktreeInOutput(out, worktreePath)
}
