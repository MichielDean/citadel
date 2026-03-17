package cataracta

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureSharedClone guarantees a single shared clone of the repo exists at dir.
// All workers share this clone's object store via git worktrees.
// On first call it clones the repo; on subsequent calls it fetches updates.
func EnsureSharedClone(dir, repoURL string) error {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale clone dir: %w", err)
		}
		return cloneSandbox(dir, repoURL)
	}
	return fetchSandbox(dir)
}

// EnsureWorktree guarantees a git worktree for a worker exists at worktreeDir,
// rooted in the shared clone at cloneDir. The worktree is created detached;
// callers must call PrepareBranch to check out the correct branch.
func EnsureWorktree(worktreeDir, cloneDir string) error {
	// Prune stale worktree registrations (entries whose directories no longer exist).
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = cloneDir
	_ = pruneCmd.Run() // best-effort; ignore errors

	// A worktree has a .git file (not dir) pointing back to the main repo.
	gitPath := filepath.Join(worktreeDir, ".git")
	if _, err := os.Stat(gitPath); err == nil {
		return nil // Already exists and valid.
	}

	// Remove any stale non-git debris before adding the worktree.
	if err := os.RemoveAll(worktreeDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale worktree dir: %w", err)
	}

	// Use --force in case the path is still registered (prune may not always catch it).
	cmd := exec.Command("git", "worktree", "add", "--detach", "--force", worktreeDir, "HEAD")
	cmd.Dir = cloneDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add %s: %w: %s", worktreeDir, err, out)
	}
	return nil
}

// EnsureSandbox is kept for backward compatibility with tests.
// New code should call EnsureSharedClone + EnsureWorktree.
func EnsureSandbox(dir, repoURL string) error {
	return EnsureSharedClone(dir, repoURL)
}

// PrepareBranch positions the sandbox on a per-item feature branch.
//
// If the branch already exists locally (e.g., resuming after review feedback),
// it checks out the existing branch and rebases onto origin/main. This
// preserves the implementer's previous commits so revision is incremental.
//
// If the branch does not yet exist, it is created from origin/main.
func PrepareBranch(dir, itemID string) error {
	branch := "feat/" + itemID

	// Configure git identity so commits don't fail.
	if err := configureGitIdentity(dir); err != nil {
		return err
	}

	// Check whether the branch already exists locally.
	exists, err := branchExists(dir, branch)
	if err != nil {
		return err
	}

	if exists {
		// Resume existing work — check out the branch.
		// Use -f in case git thinks the branch is in use by a now-pruned worktree.
		checkout := exec.Command("git", "checkout", "-f", branch)
		checkout.Dir = dir
		if out, err := checkout.CombinedOutput(); err != nil {
			return fmt.Errorf("git checkout %s in %s: %w: %s", branch, dir, err, out)
		}
		// Clean stale runner artifacts from the working tree (not committed code).
		cleanArtifacts(dir)
		return nil
	}

	// New branch — start from a clean origin/main.
	reset := exec.Command("git", "reset", "--hard", "origin/main")
	reset.Dir = dir
	if out, err := reset.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset in %s: %w: %s", dir, err, out)
	}

	cleanAll := exec.Command("git", "clean", "-fdx")
	cleanAll.Dir = dir
	if out, err := cleanAll.CombinedOutput(); err != nil {
		return fmt.Errorf("git clean in %s: %w: %s", dir, err, out)
	}

	createBranch := exec.Command("git", "checkout", "-b", branch)
	createBranch.Dir = dir
	if out, err := createBranch.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b %s in %s: %w: %s", branch, dir, err, out)
	}

	return nil
}

// cloneSandbox performs a fresh git clone into dir.
func cloneSandbox(dir, repoURL string) error {
	if repoURL == "" {
		return fmt.Errorf("repo URL is required for initial clone")
	}
	cmd := exec.Command("git", "clone", repoURL, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", repoURL, err)
	}
	return nil
}

// fetchSandbox fetches latest remote refs without touching the working tree.
func fetchSandbox(dir string) error {
	fetch := exec.Command("git", "fetch", "origin")
	fetch.Dir = dir
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch in %s: %w: %s", dir, err, out)
	}
	return nil
}

// branchExists reports whether a local branch with the given name exists.
func branchExists(dir, branch string) (bool, error) {
	cmd := exec.Command("git", "branch", "--list", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git branch --list in %s: %w", dir, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// configureGitIdentity sets user.name and user.email in the repo config so
// commits don't fail due to missing identity.
func configureGitIdentity(dir string) error {
	cmds := [][]string{
		{"git", "config", "user.name", "Cistern Agent"},
		{"git", "config", "user.email", "agent@cistern.local"},
	}
	for _, args := range cmds {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%v in %s: %w: %s", args, dir, err, out)
		}
	}
	return nil
}

// ParkWorktree detaches HEAD in the worktree so no worktree holds a feature
// branch between steps. The shared clone owns the main branch; workers must
// use detached HEAD as their "parked" state so any worker can check out any
// feature branch on the next step.
func ParkWorktree(dir string) {
	cmd := exec.Command("git", "checkout", "--detach", "HEAD")
	cmd.Dir = dir
	_ = cmd.Run() // best-effort; failure just means the next checkout may conflict
}

// cleanArtifacts removes runner-written files from the working tree so they
// don't appear in diffs or confuse the agent.
func cleanArtifacts(dir string) {
	for _, f := range []string{"CONTEXT.md", "handoff.md"} {
		_ = os.Remove(dir + "/" + f)
	}
}
