package castellarius

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/skills"

	_ "github.com/mattn/go-sqlite3"
)

// DroughtHookParams bundles the context needed by RunDroughtHooks.
// Using a struct avoids a long positional parameter list and makes call sites
// self-documenting — callers only set the fields they need.
type DroughtHookParams struct {
	Hooks              []aqueduct.DroughtHook
	Config             *aqueduct.AqueductConfig
	DBPath             string
	SandboxRoot        string
	Logger             *slog.Logger
	StartupBinaryMtime time.Time
	CfgPath            string
	StartupCfgMtime    time.Time
	Supervised         bool
	OnReload           func()
}

// RunDroughtHooks executes all configured drought hooks sequentially.
// Errors are logged but do not crash the flow.
//
// Restart behaviour depends on whether the process is supervised:
//   - supervised (systemd/supervisord/etc.): os.Exit(0) — supervisor restarts cleanly.
//   - unsupervised (manual run): workflow changes trigger an in-process hot-reload via
//     OnReload(); binary or cistern.yaml updates log a warning but do NOT exit
//     (Castellarius stays alive).
//
// This ensures the Castellarius never dies without something to bring it back.
func RunDroughtHooks(p DroughtHookParams) {
	logger := p.Logger
	needsRestart := false
	workflowChanged := false
	restartReason := ""

	for _, hook := range p.Hooks {
		logger.Info("drought hook starting", "hook", hook.Name, "action", hook.Action)
		var err error
		switch hook.Action {
		case "git_sync":
			var changed bool
			changed, err = hookGitSync(p.Config, p.SandboxRoot, logger)
			if changed {
				workflowChanged = true
				if hook.RestartIfUpdated {
					needsRestart = true
					restartReason = "workflow YAML updated by git_sync"
				}
			}
		case "cataractae_generate":
			err = hookCataractaeGenerate(p.Config, logger)
		case "worktree_prune":
			err = hookWorktreePrune(p.Config, p.SandboxRoot, logger)
		case "db_vacuum":
			err = hookDBVacuum(p.DBPath, logger)
		case "events_prune":
			err = hookEventsPrune(p.DBPath, hook, logger)
		case "tmp_cleanup":
			err = hookTmpCleanup(logger)
		case "restart_self":
			// Always restart — useful for "restart after every drought" or to pick
			// up a new binary after a manual deploy.
			needsRestart = true
			restartReason = "restart_self hook"
		case "shell":
			err = hookShell(hook, logger)
		default:
			logger.Warn("drought hook: unknown action", "hook", hook.Name, "action", hook.Action)
			continue
		}
		if err != nil {
			logger.Error("drought hook failed", "hook", hook.Name, "error", err)
		} else {
			logger.Info("drought hook completed", "hook", hook.Name)
		}
	}

	// Binary-update detection: if the on-disk binary is newer than when we started,
	// a restart is needed to run the new code.
	binaryUpdated := false
	if exe, err := os.Executable(); err == nil && mtimeAdvanced(exe, p.StartupBinaryMtime) {
		binaryUpdated = true
		needsRestart = true
		restartReason = "binary updated on disk"
	}

	// Config-update detection: if cistern.yaml is newer than when we started,
	// a restart is needed to pick up the new config.
	cfgUpdated := mtimeAdvanced(p.CfgPath, p.StartupCfgMtime)
	if cfgUpdated {
		needsRestart = true
		restartReason = "cistern.yaml updated on disk"
	}

	if !needsRestart {
		return
	}

	if p.Supervised {
		// Supervisor (systemd, supervisord, etc.) will restart us.
		logger.Info("Castellarius exiting for clean restart", "reason", restartReason)
		os.Exit(0)
	}

	// Unsupervised — never exit on our own. Apply what we can in-process.
	if workflowChanged && !binaryUpdated && !cfgUpdated && p.OnReload != nil {
		// Workflow change: hot-reload the parsed YAML in the main goroutine.
		logger.Info("Workflow updated — applying hot-reload (no supervisor)",
			"hint", "For automatic restarts, run under systemd: systemctl --user start cistern-castellarius")
		p.OnReload()
	} else if binaryUpdated {
		// Binary change: in-process reload is impossible. Warn and keep running.
		logger.Warn("Binary updated on disk — manual restart required to apply new code",
			"hint", "Run: systemctl --user restart cistern-castellarius  (or Ctrl-C and restart manually)")
	} else if cfgUpdated {
		// Config change: in-process reload is impossible. Warn and keep running.
		logger.Warn("cistern.yaml updated on disk — manual restart required to apply new config",
			"workflow_also_changed", workflowChanged,
			"hint", "Run: systemctl --user restart cistern-castellarius  (or Ctrl-C and restart manually)")
	} else {
		logger.Warn("Restart requested but no supervisor detected — skipping",
			"reason", restartReason,
			"hint", "Set CT_SUPERVISED=1 or run under systemd to enable automatic restarts")
	}
}

// mtimeAdvanced reports whether the file at path has been modified after baseline.
// Returns false when path is empty, baseline is zero, or the file cannot be stat'd.
func mtimeAdvanced(path string, baseline time.Time) bool {
	if path == "" || baseline.IsZero() {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.ModTime().After(baseline)
}

// hookGitSync fetches the latest workflow YAML and skills from origin/main for
// each repo and deploys them locally. Workflow changes are tracked for restart
// purposes; skills sync is independent and never triggers a restart.
// Uses `git fetch` + `git show origin/main:<path>` — safe to run while agents are
// on feature branches because it never touches the working tree.
// Must run before cataractae_generate so roles are rebuilt from the freshest YAML.
func hookGitSync(cfg *aqueduct.AqueductConfig, sandboxRoot string, logger *slog.Logger) (changed bool, err error) {
	home, hErr := os.UserHomeDir()
	if hErr != nil {
		return false, fmt.Errorf("git_sync: home dir: %w", hErr)
	}
	for _, repo := range cfg.Repos {
		if repo.WorkflowPath == "" {
			continue
		}

		// Find any sandbox clone for this repo (first one with a .git dir).
		repoSandboxDir := filepath.Join(sandboxRoot, repo.Name)
		entries, err := os.ReadDir(repoSandboxDir)
		if err != nil {
			logger.Warn("git_sync: no sandbox dir", "repo", repo.Name, "error", err)
			continue
		}
		var cloneDir string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			candidate := filepath.Join(repoSandboxDir, e.Name())
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				cloneDir = candidate
				break
			}
		}
		if cloneDir == "" {
			logger.Warn("git_sync: no clone found", "repo", repo.Name)
			continue
		}

		// Fetch latest refs without touching the working tree.
		if out, err := exec.Command("git", "-C", cloneDir, "fetch", "origin").CombinedOutput(); err != nil {
			logger.Warn("git_sync: fetch failed", "repo", repo.Name, "error", err, "output", string(out))
			continue
		}

		// Repo-relative workflow path: aqueduct/<filename>.
		wfFilename := filepath.Base(repo.WorkflowPath)
		repoRelPath := "aqueduct/" + wfFilename

		// Extract the file from origin/main — does NOT modify the working tree.
		newContent, err := exec.Command("git", "-C", cloneDir, "show", "origin/main:"+repoRelPath).Output()
		if err != nil {
			logger.Warn("git_sync: cannot read from origin/main", "repo", repo.Name, "path", repoRelPath, "error", err)
			// Don't continue — still attempt skills sync below.
		} else {
			// Resolve deployed path (may be absolute or relative to ~/.cistern/).
			deployedPath := repo.WorkflowPath
			if !filepath.IsAbs(deployedPath) {
				deployedPath = filepath.Join(home, ".cistern", deployedPath)
			}

			// Skip write if content is identical.
			existing, _ := os.ReadFile(deployedPath)
			if !bytes.Equal(existing, newContent) {
				if err := os.MkdirAll(filepath.Dir(deployedPath), 0o755); err != nil {
					logger.Warn("git_sync: mkdir failed", "path", deployedPath, "error", err)
				} else if err := os.WriteFile(deployedPath, newContent, 0o644); err != nil {
					logger.Warn("git_sync: write failed", "path", deployedPath, "error", err)
				} else {
					logger.Info("git_sync: workflow updated", "repo", repo.Name, "path", deployedPath)
					changed = true
				}
			} else {
				logger.Info("git_sync: workflow up to date", "repo", repo.Name)
			}
		}

		// Deploy cataractae source files for each role defined in the workflow.
		if newContent != nil {
			syncCataractaeFiles(cloneDir, home, repo.Name, newContent, logger)
		}

		// Sync skills from the skills/ tree in origin/main into ~/.cistern/skills/.
		// This runs independently of the workflow sync — skills are deployed even if
		// the workflow YAML is missing or unchanged.
		syncSkillsFromRepo(cloneDir, repo.Name, logger)
	}
	return changed, nil
}

// syncSkillsFromRepo deploys all skills from the skills/ tree in origin/main into
// ~/.cistern/skills/ via skills.Deploy. Errors are logged but not fatal.
func syncSkillsFromRepo(cloneDir, repoName string, logger *slog.Logger) {
	// List skill names directly inside origin/main:skills (colon syntax lists tree contents).
	out, err := exec.Command("git", "-C", cloneDir, "ls-tree", "--name-only", "origin/main:skills").Output()
	if err != nil {
		// skills/ directory may not exist in origin/main — silently skip.
		return
	}

	for _, name := range strings.Fields(strings.TrimSpace(string(out))) {
		skillPath := "skills/" + name + "/SKILL.md"
		content, err := exec.Command("git", "-C", cloneDir, "show", "origin/main:"+skillPath).Output()
		if err != nil {
			logger.Warn("git_sync: skill SKILL.md not found", "repo", repoName, "skill", name)
			continue
		}

		changed, err := skills.Deploy(name, content)
		if err != nil {
			logger.Warn("git_sync: deploy skill failed", "repo", repoName, "skill", name, "error", err)
			continue
		}
		if changed {
			logger.Info("git_sync: skill deployed", "repo", repoName, "skill", name)
		}
	}
}

// syncCataractaeFiles deploys PERSONA.md and INSTRUCTIONS.md for each role
// defined in wfContent from origin/main (via cloneDir) to ~/.cistern/cataractae/<roleKey>/.
// Missing files and parse errors are logged but do not halt the sync.
func syncCataractaeFiles(cloneDir, home, repoName string, wfContent []byte, logger *slog.Logger) {
	w, err := aqueduct.ParseWorkflowBytes(wfContent)
	if err != nil {
		logger.Warn("git_sync: cannot parse workflow for cataractae extraction", "repo", repoName, "error", err)
		return
	}

	cataractaeDeployDir := filepath.Join(home, ".cistern", "cataractae")
	seen := map[string]bool{}
	for _, step := range w.Cataractae {
		roleKey := step.Identity
		if roleKey == "" || seen[roleKey] {
			continue
		}
		seen[roleKey] = true
		roleDir := filepath.Join(cataractaeDeployDir, roleKey)
		for _, fname := range []string{"PERSONA.md", "INSTRUCTIONS.md"} {
			relPath := "cataractae/" + roleKey + "/" + fname
			content, err := exec.Command("git", "-C", cloneDir, "show", "origin/main:"+relPath).Output()
			if err != nil {
				logger.Info("git_sync: cataractae file not in origin/main", "repo", repoName, "path", relPath)
				continue
			}
			destPath := filepath.Join(roleDir, fname)
			old, _ := os.ReadFile(destPath)
			if bytes.Equal(old, content) {
				continue
			}
			if err := os.MkdirAll(roleDir, 0o755); err != nil {
				logger.Warn("git_sync: mkdir failed", "path", destPath, "error", err)
				continue
			}
			if err := os.WriteFile(destPath, content, 0o644); err != nil {
				logger.Warn("git_sync: write failed", "path", destPath, "error", err)
				continue
			}
			logger.Info("git_sync: cataractae file updated", "repo", repoName, "path", destPath)
		}
	}
}

// hookCataractaeGenerate checks if any workflow YAML mtime is newer than the oldest
// role file in ~/.cistern/cataractae/ and regenerates if needed.
func hookCataractaeGenerate(cfg *aqueduct.AqueductConfig, logger *slog.Logger) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}
	cataractaeDir := filepath.Join(home, ".cistern", "cataractae")

	// Find the oldest role file mtime.
	oldestRole := time.Now()
	hasRoles := false
	entries, _ := os.ReadDir(cataractaeDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		claudePath := filepath.Join(cataractaeDir, e.Name(), "CLAUDE.md")
		info, err := os.Stat(claudePath)
		if err != nil {
			continue
		}
		hasRoles = true
		if info.ModTime().Before(oldestRole) {
			oldestRole = info.ModTime()
		}
	}

	regenerated := false
	for _, repo := range cfg.Repos {
		if repo.WorkflowPath == "" {
			continue
		}
		wfPath := repo.WorkflowPath
		info, err := os.Stat(wfPath)
		if err != nil {
			logger.Warn("cataractae_generate: cannot stat workflow", "path", wfPath, "error", err)
			continue
		}

		if !hasRoles || info.ModTime().After(oldestRole) {
			w, err := aqueduct.ParseWorkflow(wfPath)
			if err != nil {
				logger.Warn("cataractae_generate: parse workflow failed", "path", wfPath, "error", err)
				continue
			}
			written, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir)
			if err != nil {
				return fmt.Errorf("generate role files: %w", err)
			}
			for _, path := range written {
				logger.Info("cataractae_generate: regenerated", "path", path)
			}
			regenerated = true
		}
	}

	if !regenerated {
		logger.Info("cataractae_generate: roles up to date")
	}
	return nil
}

// hookWorktreePrune runs `git worktree prune` for each repo's sandbox directory.
func hookWorktreePrune(cfg *aqueduct.AqueductConfig, sandboxRoot string, logger *slog.Logger) error {
	for _, repo := range cfg.Repos {
		dir := filepath.Join(sandboxRoot, repo.Name)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		// git worktree prune must run in the _primary clone, not the repo sandbox root.
		primaryDir := filepath.Join(dir, "_primary")
		if _, err := os.Stat(primaryDir); os.IsNotExist(err) {
			continue // _primary not yet cloned — skip silently
		}
		cmd := exec.Command("git", "worktree", "prune")
		cmd.Dir = primaryDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			logger.Warn("worktree_prune: error", "repo", repo.Name, "error", err, "output", string(out))
			continue
		}
		logger.Info("worktree_prune: pruned", "repo", repo.Name, "output", string(out))
	}
	return nil
}

// hookDBVacuum runs VACUUM on the SQLite queue database.
func hookDBVacuum(dbPath string, logger *slog.Logger) error {
	if dbPath == "" {
		return fmt.Errorf("db_vacuum: no database path configured")
	}

	beforeInfo, _ := os.Stat(dbPath)
	var beforeSize int64
	if beforeInfo != nil {
		beforeSize = beforeInfo.Size()
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("db_vacuum: open: %w", err)
	}
	defer db.Close()

	if _, err := db.Exec("VACUUM"); err != nil {
		return fmt.Errorf("db_vacuum: %w", err)
	}

	afterInfo, _ := os.Stat(dbPath)
	var afterSize int64
	if afterInfo != nil {
		afterSize = afterInfo.Size()
	}

	logger.Info("db_vacuum: completed",
		"before_bytes", beforeSize,
		"after_bytes", afterSize,
		"freed_bytes", beforeSize-afterSize,
	)
	return nil
}

// hookEventsPrune removes events older than keep_days (default 30) from the events table.
// This prevents unbounded growth of the events log over time.
func hookEventsPrune(dbPath string, hook aqueduct.DroughtHook, logger *slog.Logger) error {
	if dbPath == "" {
		return fmt.Errorf("events_prune: no database path configured")
	}
	keepDays := hook.KeepDays
	if keepDays <= 0 {
		keepDays = 30
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("events_prune: open: %w", err)
	}
	defer db.Close()

	cutoff := time.Now().UTC().AddDate(0, 0, -keepDays).Format("2006-01-02T15:04:05Z")
	res, err := db.Exec(`DELETE FROM events WHERE created_at < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("events_prune: %w", err)
	}
	n, _ := res.RowsAffected()
	logger.Info("events_prune: completed", "deleted_rows", n, "keep_days", keepDays)
	return nil
}

// hookTmpCleanup removes stale ct-diff-* and ct-review-* temp directories left
// by agent sessions that exited without cleanup.
func hookTmpCleanup(logger *slog.Logger) error {
	patterns := []string{"/tmp/ct-diff-*", "/tmp/ct-review-*", "/tmp/ct-qa-*"}
	total := 0
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			// Only remove directories older than 2 hours.
			if time.Since(info.ModTime()) < 2*time.Hour {
				continue
			}
			if err := os.RemoveAll(m); err != nil {
				logger.Warn("tmp_cleanup: remove failed", "path", m, "error", err)
			} else {
				total++
			}
		}
	}
	logger.Info("tmp_cleanup: completed", "removed", total)
	return nil
}

// hookShell runs a shell command with a timeout.
func hookShell(hook aqueduct.DroughtHook, logger *slog.Logger) error {
	if hook.Command == "" {
		return fmt.Errorf("shell hook %q: command is empty", hook.Name)
	}

	timeout := time.Duration(hook.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", hook.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", hook.Command)
	}
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		logger.Info("shell hook output", "hook", hook.Name, "output", string(out))
	}
	if err != nil {
		return fmt.Errorf("shell hook %q: %w", hook.Name, err)
	}
	return nil
}
