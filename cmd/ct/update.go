package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	updateDryRun   bool
	updateRepoPath string
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Pull latest main and rebuild the ct binary",
	Long: `Update ct to the latest version from main.

Auto-detects the cistern repository location in order:
  1. CT_REPO_PATH environment variable
  2. Sibling of the binary  (e.g. ~/go/bin/ct → ~/cistern)
  3. ~/.cistern/repo

Use --repo-path to override.`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "show what would change without building")
	updateCmd.Flags().StringVar(&updateRepoPath, "repo-path", "", "path to the cistern repository (default: auto-detect)")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	repoPath, err := resolveUpdateRepoPath(updateRepoPath)
	if err != nil {
		return err
	}

	oldCommit, err := gitRevParseUpdate(repoPath, "HEAD")
	if err != nil {
		return fmt.Errorf("reading current commit: %w", err)
	}

	fmt.Printf("repo:    %s\n", repoPath)
	fmt.Printf("current: %s\n", shortSHA(oldCommit))

	if isCastellariusRunning() {
		fmt.Println("warning: the Castellarius is running — it will restart automatically after update")
	}

	if updateDryRun {
		fetchOut, fetchErr := runGitCommand(repoPath, "fetch", "origin", "main")
		if fetchErr != nil {
			return fmt.Errorf("git fetch: %w\n%s", fetchErr, fetchOut)
		}
		remoteCommit, err := gitRevParseUpdate(repoPath, "FETCH_HEAD")
		if err != nil {
			return fmt.Errorf("reading remote commit: %w", err)
		}
		if oldCommit == remoteCommit {
			fmt.Println("already up to date")
		} else {
			fmt.Printf("new:     %s\n", shortSHA(remoteCommit))
			fmt.Println("(dry-run: no changes made)")
		}
		return nil
	}

	// Resolve binary path before pulling (in case the pull takes a moment)
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return fmt.Errorf("resolving binary symlinks: %w", err)
	}

	pullOut, pullErr := runGitCommand(repoPath, "-c", "core.hooksPath=/dev/null", "pull", "--ff-only", "origin", "main")
	if pullErr != nil {
		if isFFOnlyFailure(pullOut) {
			return fmt.Errorf("local repo has commits not in origin/main — rebase or reset manually before updating")
		}
		return fmt.Errorf("git pull --ff-only: %w\n%s", pullErr, pullOut)
	}

	newCommit, err := gitRevParseUpdate(repoPath, "HEAD")
	if err != nil {
		return fmt.Errorf("reading new commit: %w", err)
	}

	if oldCommit == newCommit {
		fmt.Println("already up to date")
		return nil
	}

	fmt.Printf("new:     %s\n", shortSHA(newCommit))

	// Back up current binary before building
	backupPath := binPath + ".bak"
	if err := copyBinary(binPath, backupPath); err != nil {
		return fmt.Errorf("backing up binary: %w", err)
	}

	fmt.Printf("building: go build -o %s ./cmd/ct/\n", binPath)
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/ct/")
	buildCmd.Dir = repoPath
	buildOut, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "build failed:\n%s\n", buildOut)
		if restoreErr := copyBinary(backupPath, binPath); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "error: could not restore backup: %v\n", restoreErr)
			fmt.Fprintf(os.Stderr, "backup preserved at: %s\n", backupPath)
			return fmt.Errorf("build failed and restore failed")
		}
		os.Remove(backupPath)
		fmt.Fprintln(os.Stderr, "previous binary restored")
		return fmt.Errorf("build failed")
	}

	os.Remove(backupPath)
	fmt.Printf("updated: %s → %s\n", shortSHA(oldCommit), shortSHA(newCommit))
	return nil
}

// resolveUpdateRepoPath finds the cistern repo using the priority order from the spec.
func resolveUpdateRepoPath(override string) (string, error) {
	if override != "" {
		return verifyUpdateRepoPath(override)
	}

	// 1. CT_REPO_PATH env var
	if env := os.Getenv("CT_REPO_PATH"); env != "" {
		return verifyUpdateRepoPath(env)
	}

	// 2. Sibling of the binary: search 1–3 levels up from the binary directory.
	//    Handles ~/bin/ct → ~/cistern (1 level) and ~/go/bin/ct → ~/cistern (2 levels).
	if binPath, err := os.Executable(); err == nil {
		if binPath, err = filepath.EvalSymlinks(binPath); err == nil {
			if p, err := findRepoFromBinaryPath(binPath); err == nil {
				return p, nil
			}
		}
	}

	// 3. ~/.cistern/repo
	if home, err := os.UserHomeDir(); err == nil {
		configured := filepath.Join(home, ".cistern", "repo")
		if p, err := verifyUpdateRepoPath(configured); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("cannot find cistern repo — set CT_REPO_PATH or use --repo-path PATH")
}

// findRepoFromBinaryPath searches 1–3 levels above binPath's directory for a
// sibling "cistern" directory that is a valid git+go.mod repo. This handles
// common install locations:
//   - ~/bin/ct          → ~/cistern     (1 level up)
//   - ~/go/bin/ct       → ~/cistern     (2 levels up)
//   - ~/a/b/bin/ct      → ~/cistern     (3 levels up)
func findRepoFromBinaryPath(binPath string) (string, error) {
	dir := filepath.Dir(binPath)
	for levels := 1; levels <= 3; levels++ {
		dir = filepath.Dir(dir)
		if p, err := verifyUpdateRepoPath(filepath.Join(dir, "cistern")); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no cistern repo found relative to binary at %s", binPath)
}

// isFFOnlyFailure reports whether a git pull --ff-only output indicates that
// the local branch has diverged from origin (cannot fast-forward).
func isFFOnlyFailure(output string) bool {
	return strings.Contains(output, "Not possible to fast-forward") ||
		strings.Contains(output, "diverging branches") ||
		strings.Contains(output, "Diverging branches")
}

// verifyUpdateRepoPath checks that path is a git repository with go.mod.
func verifyUpdateRepoPath(path string) (string, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return "", fmt.Errorf("%s: not a git repository", path)
	}
	if _, err := os.Stat(filepath.Join(path, "go.mod")); err != nil {
		return "", fmt.Errorf("%s: no go.mod found", path)
	}
	return path, nil
}

// gitRevParseUpdate returns the full SHA for a ref in the given repo.
func gitRevParseUpdate(repoPath, ref string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", ref).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runGitCommand runs a git command in the given directory and returns combined output.
func runGitCommand(repoPath string, gitArgs ...string) (string, error) {
	args := append([]string{"-C", repoPath}, gitArgs...)
	out, err := exec.Command("git", args...).CombinedOutput()
	return string(out), err
}

// shortSHA returns the first 7 characters of a commit SHA.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// isCastellariusRunning reports whether the Castellarius process is running.
func isCastellariusRunning() bool {
	out, err := exec.Command("pgrep", "-f", "ct castellarius").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// copyBinary copies src to dst preserving file permissions.
func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	if closeErr := out.Close(); closeErr != nil {
		return closeErr
	}
	return copyErr
}
