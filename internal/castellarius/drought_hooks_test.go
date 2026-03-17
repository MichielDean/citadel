package castellarius

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/aqueduct"
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

	cataracta := newMockRunner(client)
	cataracta.outcomes["implement"] = "pass"

	sched := testScheduler(client, cataracta)

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
	if !cataracta.waitCalls(1, time.Second) {
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
	cataracta := newMockRunner(client)

	sched := testScheduler(client, cataracta)

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
    on_pass: done
cataracta_definitions:
  implementer:
    name: Implementer
    description: test
    instructions: old instructions
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

	// Create a workflow YAML file.
	wfPath := filepath.Join(tmpDir, "aqueduct.yaml")
	wfContent := `name: test
cataractae:
  - name: impl
    type: agent
    on_pass: done
cataracta_definitions:
  implementer:
    name: Implementer
    description: test
    instructions: new instructions
`
	os.WriteFile(wfPath, []byte(wfContent), 0o644)

	// Make the YAML file newer than roles.
	future := time.Now().Add(time.Hour)
	os.Chtimes(wfPath, future, future)

	// Create a roles dir with an older CLAUDE.md.
	cataractaeDir := filepath.Join(tmpDir, "cataractae")
	implDir := filepath.Join(cataractaeDir, "implementer")
	os.MkdirAll(implDir, 0o755)
	claudePath := filepath.Join(implDir, "CLAUDE.md")
	os.WriteFile(claudePath, []byte("old content"), 0o644)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(claudePath, past, past)

	// Override home dir to use tmpDir for roles (both HOME and USERPROFILE for Windows).
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	// Create ~/.cistern/cataractae structure.
	cisternRoles := filepath.Join(tmpDir, ".cistern", "cataractae", "implementer")
	os.MkdirAll(cisternRoles, 0o755)
	cisternClaude := filepath.Join(cisternRoles, "CLAUDE.md")
	os.WriteFile(cisternClaude, []byte("old"), 0o644)
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

	// The role file should have been regenerated.
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
