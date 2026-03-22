package castellarius

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// platformCreateFile returns a shell command that creates an empty file.
func platformCreateFile(path string) string {
	if runtime.GOOS == "windows" {
		return "copy nul " + path
	}
	return "touch " + path
}

// platformAppendLine returns a shell command that appends text to a file.
func platformAppendLine(text, path string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("echo %s>> %s", text, path)
	}
	return fmt.Sprintf("echo %s >> %s", text, path)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// --- Drought edge detection tests ---

func TestDroughtHook_FiresOnDroughtTransition(t *testing.T) {
	// When scheduler transitions from busy to drought (wasDrought false→true),
	// hooks should fire.
	client := newMockClient()
	client.readyItems = []*cistern.Droplet{{ID: "b1"}}

	runner := newMockRunner(client)
	runner.outcomes["implement"] = "pass"

	sched := testScheduler(client, runner)

	// Create a temp file for shell hook to write to, proving it ran.
	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "drought-fired")
	sched.config.DroughtHooks = []aqueduct.DroughtHook{
		{
			Name:    "test-marker",
			Action:  "shell",
			Command: platformCreateFile(markerFile),
			Timeout: 5,
		},
	}
	sched.logger = discardLogger()

	// First tick: picks up work (busy). wasDrought starts false.
	sched.Tick(context.Background())
	if !runner.waitCalls(1, time.Second) {
		t.Fatal("timed out waiting for runner")
	}
	// Allow routing to complete.
	time.Sleep(100 * time.Millisecond)

	// At this point the work is done and workers released.
	// Next tick: no work available → drought transition fires hooks (in goroutine).
	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond) // Allow goroutine to complete.

	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Error("drought hook did not fire on drought transition")
	}
}

func TestDroughtHook_DoesNotFireWhenAlreadyDrought(t *testing.T) {
	// When scheduler is already drought (wasDrought true→true), hooks should NOT fire again.
	client := newMockClient()
	runner := newMockRunner(client)

	sched := testScheduler(client, runner)

	tmpDir := t.TempDir()
	counterFile := filepath.Join(tmpDir, "counter")
	sched.config.DroughtHooks = []aqueduct.DroughtHook{
		{
			Name:    "counter",
			Action:  "shell",
			Command: platformAppendLine("x", counterFile),
			Timeout: 5,
		},
	}
	sched.logger = discardLogger()

	// First tick: no work → enters drought, hooks fire (in goroutine).
	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond) // Allow goroutine to complete.

	// Second tick: still no work → stays drought, hooks should NOT fire.
	sched.Tick(context.Background())

	// Third tick: same.
	sched.Tick(context.Background())

	data, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatal("counter file should exist:", err)
	}
	// Should have exactly one line (one "x\n") from the first drought transition.
	lines := 0
	for _, b := range data {
		if b == 'x' {
			lines++
		}
	}
	if lines != 1 {
		t.Errorf("expected hook to fire exactly once, got %d times", lines)
	}
}

func TestDroughtHook_DoesNotFireWhileWorkInProgress(t *testing.T) {
	// When work is in progress, hooks should not fire.
	client := newMockClient()
	for i := range 3 {
		client.readyItems = append(client.readyItems, &cistern.Droplet{
			ID: fmt.Sprintf("b%d", i),
		})
	}

	blocker := newBlockingRunner()
	sched := testScheduler(client, blocker)

	tmpDir := t.TempDir()
	markerFile := filepath.Join(tmpDir, "should-not-exist")
	sched.config.DroughtHooks = []aqueduct.DroughtHook{
		{
			Name:    "test-marker",
			Action:  "shell",
			Command: platformCreateFile(markerFile),
			Timeout: 5,
		},
	}
	sched.logger = discardLogger()

	// Tick: workers are busy (blocking runner).
	sched.Tick(context.Background())
	time.Sleep(50 * time.Millisecond)

	// Another tick while workers are still busy.
	sched.Tick(context.Background())

	if _, err := os.Stat(markerFile); !os.IsNotExist(err) {
		t.Error("drought hook should not fire while work is in progress")
	}

	close(blocker.ch)
}

// --- Built-in hook tests ---

func TestRolesGenerate_NoOpWhenYAMLOlder(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a workflow YAML file.
	wfPath := filepath.Join(tmpDir, "aqueduct.yaml")
	wfContent := `name: test
cataractae:
  - name: impl
    type: agent
    identity: implementer
    on_pass: done
`
	os.WriteFile(wfPath, []byte(wfContent), 0o644)

	// Create a roles dir with a CLAUDE.md that is newer than the workflow YAML.
	cataractaeDir := filepath.Join(tmpDir, "cataractae")
	implDir := filepath.Join(cataractaeDir, "implementer")
	os.MkdirAll(implDir, 0o755)
	claudePath := filepath.Join(implDir, "CLAUDE.md")
	os.WriteFile(claudePath, []byte("existing role content"), 0o644)

	// Make the CLAUDE.md newer than the workflow YAML by touching it.
	future := time.Now().Add(time.Hour)
	os.Chtimes(claudePath, future, future)

	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test", WorkflowPath: wfPath, Cataractae: 1, Prefix: "t"},
		},
		MaxCataractae: 1,
	}

	logger := discardLogger()
	err := hookCataractaeGenerate(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The role file should NOT have been regenerated (content unchanged).
	data, _ := os.ReadFile(claudePath)
	if string(data) != "existing role content" {
		t.Error("roles_generate should have been a no-op but content changed")
	}
}

func TestRolesGenerate_RegeneratesWhenYAMLNewer(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a workflow YAML file with an agent step that has an identity.
	wfPath := filepath.Join(tmpDir, "aqueduct.yaml")
	wfContent := `name: test
cataractae:
  - name: impl
    type: agent
    identity: implementer
    on_pass: done
`
	os.WriteFile(wfPath, []byte(wfContent), 0o644)

	// Make the YAML file newer than roles.
	future := time.Now().Add(time.Hour)
	os.Chtimes(wfPath, future, future)

	// Override home dir so hookCataractaeGenerate uses tmpDir as HOME.
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	// Create ~/.cistern/cataractae/implementer/ with PERSONA.md, INSTRUCTIONS.md, and an old CLAUDE.md.
	cisternRoles := filepath.Join(tmpDir, ".cistern", "cataractae", "implementer")
	os.MkdirAll(cisternRoles, 0o755)
	os.WriteFile(filepath.Join(cisternRoles, "PERSONA.md"), []byte("# Role: Implementer\n\nA new persona."), 0o644)
	os.WriteFile(filepath.Join(cisternRoles, "INSTRUCTIONS.md"), []byte("New instructions. ct droplet pass <id>"), 0o644)
	cisternClaude := filepath.Join(cisternRoles, "CLAUDE.md")
	os.WriteFile(cisternClaude, []byte("old"), 0o644)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(cisternClaude, past, past)

	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "test", WorkflowPath: wfPath, Cataractae: 1, Prefix: "t"},
		},
		MaxCataractae: 1,
	}

	logger := discardLogger()
	err := hookCataractaeGenerate(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The role file should have been regenerated from PERSONA.md + INSTRUCTIONS.md.
	data, _ := os.ReadFile(cisternClaude)
	content := string(data)
	if content == "old" {
		t.Error("roles_generate should have regenerated but didn't")
	}
	if len(content) == 0 {
		t.Error("regenerated file is empty")
	}
}

func TestWorktreePrune_HandlesErrorGracefully(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a repo dir that is NOT a git repo — git worktree prune will fail.
	repoDir := filepath.Join(tmpDir, "fakerepo")
	os.MkdirAll(repoDir, 0o755)

	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "fakerepo", Cataractae: 1, Prefix: "f"},
		},
		MaxCataractae: 1,
	}

	logger := discardLogger()
	// Should not panic or return error — errors are logged.
	err := hookWorktreePrune(cfg, tmpDir, logger)
	if err != nil {
		t.Fatalf("worktree_prune should not return error, got: %v", err)
	}
}

func TestShellHook_RunsCommand(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "output.txt")

	hook := aqueduct.DroughtHook{
		Name:    "test-shell",
		Action:  "shell",
		Command: "echo hello > " + outFile,
		Timeout: 5,
	}

	logger := discardLogger()
	err := hookShell(hook, logger)
	if err != nil {
		t.Fatalf("shell hook failed: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestShellHook_NonZeroExitIsWarning(t *testing.T) {
	hook := aqueduct.DroughtHook{
		Name:    "failing-hook",
		Action:  "shell",
		Command: "exit 1",
		Timeout: 5,
	}

	logger := discardLogger()
	err := hookShell(hook, logger)
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
	// Error should be returned (logged as warning by RunDroughtHooks), not panic.
}

func TestShellHook_EmptyCommandErrors(t *testing.T) {
	hook := aqueduct.DroughtHook{
		Name:   "empty",
		Action: "shell",
	}

	logger := discardLogger()
	err := hookShell(hook, logger)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// --- hookGitSync skills deployment tests ---

// mustRunGit is a test helper that runs a git command and fatals on error.
func mustRunGit(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setupGitOriginWithSkills creates a bare origin repo with a skills/<name>/SKILL.md
// committed to main. Returns the origin dir path.
func setupGitOriginWithSkills(t *testing.T, tmpDir string, skillName, skillContent string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create bare origin.
	originDir := filepath.Join(tmpDir, "origin.git")
	mustRunGit(t, "init", "--bare", originDir)

	// Clone origin, add skill, commit and push to main.
	workDir := filepath.Join(tmpDir, "work")
	mustRunGit(t, "clone", originDir, workDir)
	mustRunGit(t, "-C", workDir, "config", "user.email", "test@example.com")
	mustRunGit(t, "-C", workDir, "config", "user.name", "Test")

	skillPath := filepath.Join(workDir, "skills", skillName, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, "-C", workDir, "add", "-A")
	mustRunGit(t, "-C", workDir, "commit", "-m", "add skill")
	mustRunGit(t, "-C", workDir, "push", "origin", "HEAD:main")

	return originDir
}

func TestHookGitSync_DeploysSkillsToSkillsDir(t *testing.T) {
	// Given: an origin repo with skills/<name>/SKILL.md committed to main.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	const skillName = "test-skill"
	const skillContent = "# Test Skill\nSkill content.\n"
	originDir := setupGitOriginWithSkills(t, tmpDir, skillName, skillContent)

	// Create a sandbox clone for hookGitSync to find.
	sandboxRoot := filepath.Join(tmpDir, "sandboxes")
	sandboxClone := filepath.Join(sandboxRoot, "myrepo", "worker1")
	mustRunGit(t, "clone", originDir, sandboxClone)

	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "myrepo", WorkflowPath: "aqueduct/workflow.yaml", Cataractae: 1, Prefix: "t"},
		},
	}

	// When: hookGitSync runs.
	logger := discardLogger()
	if _, err := hookGitSync(cfg, sandboxRoot, logger); err != nil {
		t.Fatalf("hookGitSync: %v", err)
	}

	// Then: skill is deployed to ~/.cistern/skills/<name>/SKILL.md.
	deployedPath := filepath.Join(tmpDir, ".cistern", "skills", skillName, "SKILL.md")
	data, err := os.ReadFile(deployedPath)
	if err != nil {
		t.Fatalf("skill not deployed to %s: %v", deployedPath, err)
	}
	if string(data) != skillContent {
		t.Errorf("deployed content = %q, want %q", string(data), skillContent)
	}
}

func TestHookGitSync_SkillsDeployIsIdempotent(t *testing.T) {
	// Given: skills deployed from origin.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	const skillName = "idempotent-skill"
	const skillContent = "# Idempotent\nContent.\n"
	originDir := setupGitOriginWithSkills(t, tmpDir, skillName, skillContent)

	sandboxRoot := filepath.Join(tmpDir, "sandboxes")
	sandboxClone := filepath.Join(sandboxRoot, "repo", "worker1")
	mustRunGit(t, "clone", originDir, sandboxClone)

	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "repo", WorkflowPath: "aqueduct/workflow.yaml", Cataractae: 1, Prefix: "t"},
		},
	}
	logger := discardLogger()

	// First run: deploys skill.
	if _, err := hookGitSync(cfg, sandboxRoot, logger); err != nil {
		t.Fatalf("first hookGitSync: %v", err)
	}

	// When: second run with identical content.
	if _, err := hookGitSync(cfg, sandboxRoot, logger); err != nil {
		t.Fatalf("second hookGitSync: %v", err)
	}

	// Then: skill file still contains expected content (no corruption).
	deployedPath := filepath.Join(tmpDir, ".cistern", "skills", skillName, "SKILL.md")
	data, err := os.ReadFile(deployedPath)
	if err != nil {
		t.Fatalf("skill not found after idempotent run: %v", err)
	}
	if string(data) != skillContent {
		t.Errorf("content = %q, want %q", string(data), skillContent)
	}
}

func TestHookGitSync_SkillsSyncGracefulWhenNoSkillsDir(t *testing.T) {
	// Given: an origin repo with NO skills/ directory.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create origin with just an empty commit (no skills/).
	originDir := filepath.Join(tmpDir, "origin.git")
	mustRunGit(t, "init", "--bare", originDir)
	workDir := filepath.Join(tmpDir, "work")
	mustRunGit(t, "clone", originDir, workDir)
	mustRunGit(t, "-C", workDir, "config", "user.email", "test@example.com")
	mustRunGit(t, "-C", workDir, "config", "user.name", "Test")

	// Add a README so we have something to commit.
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, "-C", workDir, "add", "-A")
	mustRunGit(t, "-C", workDir, "commit", "-m", "init")
	mustRunGit(t, "-C", workDir, "push", "origin", "HEAD:main")

	sandboxRoot := filepath.Join(tmpDir, "sandboxes")
	sandboxClone := filepath.Join(sandboxRoot, "noskills-repo", "worker1")
	mustRunGit(t, "clone", originDir, sandboxClone)

	cfg := &aqueduct.AqueductConfig{
		Repos: []aqueduct.RepoConfig{
			{Name: "noskills-repo", WorkflowPath: "aqueduct/workflow.yaml", Cataractae: 1, Prefix: "t"},
		},
	}

	// When: hookGitSync runs.
	// Then: no error, no crash — gracefully handles missing skills/ directory.
	logger := discardLogger()
	if _, err := hookGitSync(cfg, sandboxRoot, logger); err != nil {
		t.Fatalf("hookGitSync: %v", err)
	}

	// Verify: no skills dir was created (nothing to deploy).
	skillsDir := filepath.Join(tmpDir, ".cistern", "skills")
	entries, _ := os.ReadDir(skillsDir)
	// Filter out manifest.json if it exists.
	var skillDirs int
	for _, e := range entries {
		if e.IsDir() {
			skillDirs++
		}
	}
	if skillDirs > 0 {
		t.Errorf("expected no skill dirs, found %d", skillDirs)
	}
}
