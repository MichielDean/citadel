package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_CreatesDirectoryStructure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cisternDir := filepath.Join(home, ".cistern")
	for _, dir := range []string{
		cisternDir,
		filepath.Join(cisternDir, "aqueduct"),
		filepath.Join(cisternDir, "cataractae"),
	} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("expected directory to exist: %s", dir)
		}
	}
}

func TestInit_WritesCisternYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configFile := filepath.Join(home, ".cistern", "cistern.yaml")
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("cistern.yaml not created: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("cistern.yaml is empty")
	}
	// Verify it matches the embedded template.
	if string(data) != string(defaultCisternConfig) {
		t.Error("cistern.yaml content does not match embedded template")
	}
}

func TestInit_CopiesWorkflowFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	aqueductDir := filepath.Join(home, ".cistern", "aqueduct")
	aqueductYAML := filepath.Join(aqueductDir, "aqueduct.yaml")
	if _, err := os.Stat(aqueductYAML); os.IsNotExist(err) {
		t.Errorf("expected workflow file to exist: aqueduct.yaml")
	}

	// Verify aqueduct.yaml content matches embedded template.
	aqueductData, err := os.ReadFile(aqueductYAML)
	if err != nil {
		t.Fatalf("read aqueduct.yaml: %v", err)
	}
	if string(aqueductData) != string(defaultAqueductWorkflow) {
		t.Error("aqueduct.yaml content does not match embedded template")
	}
}

func TestInit_GeneratesRoles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cataractaeDir := filepath.Join(home, ".cistern", "cataractae")
	entries, err := os.ReadDir(cataractaeDir)
	if err != nil {
		t.Fatalf("read roles dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no roles were generated")
	}

	// Each role should have a CLAUDE.md.
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		claudeMD := filepath.Join(cataractaeDir, entry.Name(), "CLAUDE.md")
		if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
			t.Errorf("missing CLAUDE.md for role %q", entry.Name())
		}
	}
}

func TestInit_SkipsExistingFilesWithoutForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)
	initForce = false

	// First run to create files.
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	// Overwrite cistern.yaml with sentinel content.
	configFile := filepath.Join(home, ".cistern", "cistern.yaml")
	sentinel := []byte("# sentinel — must not be overwritten")
	if err := os.WriteFile(configFile, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Second run without --force must not overwrite.
	initForce = false
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("second run error: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(sentinel) {
		t.Error("cistern.yaml was overwritten without --force")
	}
}

func TestInit_ForceOverwritesExistingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)

	// First run to create files.
	initForce = false
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	// Overwrite cistern.yaml with sentinel content.
	configFile := filepath.Join(home, ".cistern", "cistern.yaml")
	sentinel := []byte("# sentinel — must be overwritten with --force")
	if err := os.WriteFile(configFile, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Run with --force — must overwrite.
	initForce = true
	defer func() { initForce = false }()

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("force run error: %v", err)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == string(sentinel) {
		t.Error("cistern.yaml was not overwritten with --force")
	}
	if string(data) != string(defaultCisternConfig) {
		t.Error("cistern.yaml does not match embedded template after --force")
	}
}

func TestInit_IdempotentWithoutForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
t.Setenv("USERPROFILE", home)
	initForce = false

	// Run twice — both must succeed with no errors.
	for i := 0; i < 2; i++ {
		if err := initCmd.RunE(initCmd, nil); err != nil {
			t.Fatalf("run %d error: %v", i+1, err)
		}
	}
}

// --- ~/.cistern/env credential file tests ---

func TestInit_CreatesEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envFile := filepath.Join(home, ".cistern", "env")
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		t.Error("expected ~/.cistern/env to exist after ct init")
	}
}

func TestInit_EnvFileHasRestrictedPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envFile := filepath.Join(home, ".cistern", "env")
	info, err := os.Stat(envFile)
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0600, got %04o", perm)
	}
}

func TestInit_CreatesGitignoreWithEnvEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gitignorePath := filepath.Join(home, ".cistern", ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "env" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".gitignore does not contain 'env' entry; content: %q", string(data))
	}
}

func TestInit_GitignoreDoesNotDuplicateEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	// Run twice — the 'env' entry must appear exactly once.
	for i := 0; i < 2; i++ {
		if err := initCmd.RunE(initCmd, nil); err != nil {
			t.Fatalf("run %d error: %v", i+1, err)
		}
	}

	gitignorePath := filepath.Join(home, ".cistern", ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "env" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one 'env' entry in .gitignore, found %d", count)
	}
}

func TestInit_WritesStartCastellariusScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scriptPath := filepath.Join(home, ".cistern", "start-castellarius.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("start-castellarius.sh not created: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("start-castellarius.sh is empty")
	}
	// Script must exec ct castellarius start directly (no credential sourcing needed).
	content := string(data)
	if !strings.Contains(content, "ct castellarius start") {
		t.Error("start-castellarius.sh does not exec ct castellarius start")
	}
}

func TestInit_StartScriptIsExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scriptPath := filepath.Join(home, ".cistern", "start-castellarius.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o111 == 0 {
		t.Errorf("expected start-castellarius.sh to be executable, got mode %04o", perm)
	}
}

func TestInit_EnvFileNotOverwrittenWithoutForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	// First run to create env file.
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	// Write sentinel content to env file.
	envFile := filepath.Join(home, ".cistern", "env")
	sentinel := []byte("ANTHROPIC_API_KEY=sk-ant-sentinel\n")
	if err := os.WriteFile(envFile, sentinel, 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Second run without --force must not overwrite the env file.
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("second run error: %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(sentinel) {
		t.Error("~/.cistern/env was overwritten without --force")
	}
}

// --- addLineToGitignore unit tests ---

func TestAddLineToGitignore_CreatesFileWithLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	if err := addLineToGitignore(path, "env"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if !strings.Contains(string(data), "env") {
		t.Error("gitignore does not contain the added line")
	}
}

func TestAddLineToGitignore_DoesNotDuplicateExistingLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	for i := 0; i < 3; i++ {
		if err := addLineToGitignore(path, "env"); err != nil {
			t.Fatalf("run %d error: %v", i+1, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "env" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one 'env' entry, found %d", count)
	}
}

func TestAddLineToGitignore_AppendsToExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	existing := "*.log\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	if err := addLineToGitignore(path, "env"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "*.log") {
		t.Error("existing .gitignore content was lost")
	}
	if !strings.Contains(content, "env") {
		t.Error("new line was not added to .gitignore")
	}
}

// --- ct init next-steps message tests ---

// captureInitOutput runs initCmd.RunE with a fresh temp HOME and returns stdout.
func captureInitOutput(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initForce = false

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	runErr := initCmd.RunE(initCmd, nil)

	w.Close()
	os.Stdout = origStdout

	if runErr != nil {
		t.Fatalf("initCmd.RunE: %v", runErr)
	}

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestInit_NextStepsMessage_DoesNotMentionAnthropicAPIKey(t *testing.T) {
	output := captureInitOutput(t)
	if strings.Contains(output, "ANTHROPIC_API_KEY") {
		t.Errorf("ct init next-steps message must not mention ANTHROPIC_API_KEY — claude uses OAuth, not an API key; output:\n%s", output)
	}
}

func TestInit_NextStepsMessage_MentionsClaudeAuth(t *testing.T) {
	output := captureInitOutput(t)
	if !strings.Contains(output, "claude") {
		t.Errorf("ct init next-steps message should mention running 'claude' for OAuth authentication; output:\n%s", output)
	}
}
