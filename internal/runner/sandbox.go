package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureSandbox guarantees a persistent sandbox directory exists for a worker.
// On first call it clones the repo; on subsequent calls it fetches updates.
// The working tree is NOT reset — callers should call PrepareBranch to position
// the sandbox on the correct branch for the item being worked on.
func EnsureSandbox(dir, repoURL string) error {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		// Either dir doesn't exist, or it exists but isn't a git repo.
		// Remove whatever is there and clone fresh.
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale sandbox: %w", err)
		}
		return cloneSandbox(dir, repoURL)
	}
	return fetchSandbox(dir)
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
		checkout := exec.Command("git", "checkout", branch)
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
		{"git", "config", "user.name", "Bullet Farm Agent"},
		{"git", "config", "user.email", "agent@bullet-farm.local"},
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

// cleanArtifacts removes runner-written files (CONTEXT.md, outcome.json) from
// the working tree so they don't appear in diffs or confuse the agent.
func cleanArtifacts(dir string) {
	for _, f := range []string{"CONTEXT.md", "outcome.json", "handoff.md"} {
		_ = os.Remove(dir + "/" + f)
	}
}
