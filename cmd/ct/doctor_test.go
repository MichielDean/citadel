package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/oauth"
)

// --- TestDoctorCmd_FixFlagRegistered ---

func TestDoctorCmd_FixFlagRegistered(t *testing.T) {
	f := doctorCmd.Flags().Lookup("fix")
	if f == nil {
		t.Fatal("--fix flag not registered on doctor command")
	}
	if f.DefValue != "false" {
		t.Fatalf("expected default false, got %q", f.DefValue)
	}
}

// --- TestCheckWithFix unit tests ---

func TestCheckWithFix_PassingCheck_DoesNotCallFix(t *testing.T) {
	fixCalled := false
	result := checkWithFix("test", func() error {
		return nil
	}, func() error {
		fixCalled = true
		return nil
	})
	if !result {
		t.Error("expected true for passing check")
	}
	if fixCalled {
		t.Error("fix should not be called when check passes")
	}
}

func TestCheckWithFix_FailingCheck_NilFix_ReturnsFalse(t *testing.T) {
	result := checkWithFix("test", func() error {
		return fmt.Errorf("check failed")
	}, nil)
	if result {
		t.Error("expected false when check fails and no fix available")
	}
}

func TestCheckWithFix_FailingCheck_FixSucceeds_ReturnsTrue(t *testing.T) {
	fixed := false
	result := checkWithFix("test", func() error {
		if fixed {
			return nil
		}
		return fmt.Errorf("not ready")
	}, func() error {
		fixed = true
		return nil
	})
	if !result {
		t.Error("expected true when fix succeeds and check then passes")
	}
}

func TestCheckWithFix_FailingCheck_FixFails_ReturnsFalse(t *testing.T) {
	result := checkWithFix("test", func() error {
		return fmt.Errorf("check failed")
	}, func() error {
		return fmt.Errorf("fix failed too")
	})
	if result {
		t.Error("expected false when fix itself fails")
	}
}

func TestCheckWithFix_FixApplied_ButCheckStillFails_ReturnsFalse(t *testing.T) {
	result := checkWithFix("test", func() error {
		return fmt.Errorf("still broken")
	}, func() error {
		return nil // fix runs successfully but does not resolve the underlying check
	})
	if result {
		t.Error("expected false when check still fails after fix is applied")
	}
}

// --- fixCisternConfig tests ---

func TestFixCisternConfig_CreatesConfigFromTemplate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".cistern", "cistern.yaml")

	if err := fixCisternConfig(cfgPath); err != nil {
		t.Fatalf("fixCisternConfig: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if string(data) != string(defaultCisternConfig) {
		t.Error("config content does not match embedded template")
	}
}

func TestFixCisternConfig_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nested", "dirs", "cistern.yaml")

	if err := fixCisternConfig(cfgPath); err != nil {
		t.Fatalf("fixCisternConfig: %v", err)
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Error("config file was not created")
	}
}

func TestFixCisternConfig_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".cistern", "cistern.yaml")

	for i := 0; i < 2; i++ {
		if err := fixCisternConfig(cfgPath); err != nil {
			t.Fatalf("run %d: fixCisternConfig: %v", i+1, err)
		}
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != string(defaultCisternConfig) {
		t.Error("config content does not match template after idempotent run")
	}
}

// --- fixCisternDB tests ---

func TestFixCisternDB_CreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".cistern", "cistern.db")

	if err := fixCisternDB(dbPath); err != nil {
		t.Fatalf("fixCisternDB: %v", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("cistern.db was not created")
	}
}

func TestFixCisternDB_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "dirs", "cistern.db")

	if err := fixCisternDB(dbPath); err != nil {
		t.Fatalf("fixCisternDB: %v", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("cistern.db was not created in nested dirs")
	}
}

func TestFixCisternDB_DBIsAccessible(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")

	if err := fixCisternDB(dbPath); err != nil {
		t.Fatalf("fixCisternDB: %v", err)
	}

	// The db check in runDoctor opens with O_RDWR — verify the created file passes.
	f, err := os.OpenFile(dbPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("db not accessible after fix: %v", err)
	}
	f.Close()
}

// --- TestDoctor_NoFix_FailsWhenConfigMissing ---

// TestDoctor_NoFix_FailsWhenConfigMissing verifies that without --fix, doctor
// returns an error when cistern.yaml is absent. The gh auth check also fails
// when HOME is redirected to a temp dir; both contribute to the error.
func TestDoctor_NoFix_FailsWhenConfigMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	doctorFix = false

	err := doctorCmd.RunE(doctorCmd, nil)
	if err == nil {
		t.Fatal("expected error when config missing and --fix not set")
	}
}

// --- TestCheckClaudeMdIntegrity ---

func TestCheckClaudeMdIntegrity_MissingFile_ReturnsError(t *testing.T) {
	err := checkClaudeMdIntegrity(filepath.Join(t.TempDir(), "nonexistent", "CLAUDE.md"))
	if err == nil {
		t.Error("expected error for missing CLAUDE.md")
	}
}

func TestCheckClaudeMdIntegrity_FileMissingSentinel_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# Role: Implementer\n\nSome instructions without the sentinel."), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	err := checkClaudeMdIntegrity(path)
	if err == nil {
		t.Error("expected error for CLAUDE.md missing sentinel")
	}
}

func TestCheckClaudeMdIntegrity_FileWithSentinel_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	content := "# Role: Implementer\n\nct droplet pass <id> --notes \"...\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := checkClaudeMdIntegrity(path); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- TestCheckCastellariusProcess ---

func TestCheckCastellariusProcess_NoCrash(t *testing.T) {
	// Just verify the function doesn't panic. The Castellarius is not running
	// in the test environment.
	checkCastellariusProcess()
}

// --- TestCheckStalledDroplets ---

func TestCheckStalledDroplets_NonExistentDB_NoCrash(t *testing.T) {
	checkStalledDroplets(filepath.Join(t.TempDir(), "missing.db"))
}

func TestCheckStalledDroplets_EmptyDB_NoCrash(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	c, err := cistern.New(dbPath, "ct")
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	c.Close()

	// Should not panic or crash with an empty database.
	checkStalledDroplets(dbPath)
}

func TestCheckStalledDroplets_RecentDroplets_NoCrash(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	c, err := cistern.New(dbPath, "ct")
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	// Add a droplet and mark it in_progress (recent — should not be flagged).
	item, err := c.Add("repo", "Test droplet", "desc", 2, 3)
	if err != nil {
		t.Fatalf("add droplet: %v", err)
	}
	if _, err := c.GetReady("repo"); err != nil {
		t.Fatalf("get ready: %v", err)
	}
	_ = item
	c.Close()

	checkStalledDroplets(dbPath)
}

// --- TestRunDoctorExtendedChecks ---

// minimalWorkflowYAML is a valid minimal workflow for testing.
const minimalWorkflowYAML = `name: test
cataractae:
  - name: implement
    type: agent
    identity: tester
    on_pass: done
`

// minimalCisternConfigYAML is a valid config pointing to a test workflow.
const minimalCisternConfigYAML = `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
max_cataractae: 1
`

// minimalCisternConfigWithCodexYAML is a valid config using the codex provider (InstructionsFile=AGENTS.md).
const minimalCisternConfigWithCodexYAML = `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: codex
max_cataractae: 1
`

// minimalCisternConfigWithCustomLLMYAML is a config with llm.provider=custom and no base_url set.
const minimalCisternConfigWithCustomLLMYAML = `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
max_cataractae: 1
llm:
  provider: custom
`

// minimalCisternConfigWithCustomLLMAndBaseURLYAML is a config with llm.provider=custom and base_url set.
const minimalCisternConfigWithCustomLLMAndBaseURLYAML = `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
max_cataractae: 1
llm:
  provider: custom
  base_url: https://llm.example.com
`

// minimalCisternConfigWithMismatchYAML has agent provider=codex but llm.provider=anthropic.
const minimalCisternConfigWithMismatchYAML = `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: codex
llm:
  provider: anthropic
max_cataractae: 1
`

// setupFakeBinAndAPIKey creates a fake binary named binName in a temp dir,
// prepends that dir to PATH, and sets apiKeyEnv to a dummy value.
// It registers cleanup via t.Setenv and t.TempDir.
func setupFakeBinAndAPIKey(t *testing.T, binName, apiKeyEnv string) {
	t.Helper()
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, binName)
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create fake %s binary: %v", binName, err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	if apiKeyEnv != "" {
		t.Setenv(apiKeyEnv, "test-key")
	}
}

func TestRunDoctorExtendedChecks_PassesWithValidSetup(t *testing.T) {
	home := t.TempDir()

	// Set up ~/.cistern directory structure.
	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	skillsDir := filepath.Join(cisternDir, "skills")
	for _, d := range []string{aqueductDir, cataractaeDir, skillsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Provide a fake 'claude' binary and API key so binary+env checks pass.
	setupFakeBinAndAPIKey(t, "claude", "ANTHROPIC_API_KEY")

	// Write workflow.yaml.
	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	// Write config.
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Write CLAUDE.md for the "tester" identity.
	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	claudeContent := "# Role: Tester\n\nct droplet pass <id> --notes \"...\"\n"
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte(claudeContent), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected extended checks to pass with valid setup")
	}
}

func TestRunDoctorExtendedChecks_FailsWhenClaudeMdMissing(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	for _, d := range []string{aqueductDir, filepath.Join(cisternDir, "cataractae"), filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	// CLAUDE.md is NOT written — check should fail.
	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when CLAUDE.md is missing")
	}
}

func TestRunDoctorExtendedChecks_FixRegeneratesClaudeMd(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Provide a fake 'claude' binary and API key so binary+env checks pass.
	setupFakeBinAndAPIKey(t, "claude", "ANTHROPIC_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Provide PERSONA.md and INSTRUCTIONS.md in the cataractae dir so the fix
	// can regenerate CLAUDE.md from them.
	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "PERSONA.md"), []byte("# Role: Tester\n\nA tester."), 0o644); err != nil {
		t.Fatalf("write PERSONA.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "INSTRUCTIONS.md"), []byte(`Do tests. ct droplet pass <id> --notes "done"`), 0o644); err != nil {
		t.Fatalf("write INSTRUCTIONS.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	doctorFix = true
	defer func() { doctorFix = false }()

	// CLAUDE.md is absent — fix should regenerate it from PERSONA.md + INSTRUCTIONS.md.
	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected extended checks to pass after fix regenerates CLAUDE.md")
	}

	// Verify the file was created.
	generatedPath := filepath.Join(cataractaeDir, "tester", "CLAUDE.md")
	if _, err := os.Stat(generatedPath); os.IsNotExist(err) {
		t.Error("CLAUDE.md was not created by fix")
	}
}

func TestRunDoctorExtendedChecks_FailsWhenSkillMissing(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Workflow with a skill reference.
	workflowWithSkill := `name: test
cataractae:
  - name: implement
    type: agent
    identity: tester
    skills:
      - name: missing-skill
    on_pass: done
`
	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(workflowWithSkill), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Write CLAUDE.md for tester so that check passes.
	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	// "missing-skill" is not installed — check should fail.
	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when skill is not installed")
	}
}

// TestRunDoctorExtendedChecks_ProviderInstructionsFile verifies that the doctor
// checks the provider's InstructionsFile (e.g., AGENTS.md for codex) rather than
// always CLAUDE.md.
func TestRunDoctorExtendedChecks_ProviderInstructionsFile(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	setupFakeBinAndAPIKey(t, "codex", "OPENAI_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	// Config specifying codex provider (InstructionsFile = AGENTS.md).
	codexConfigYAML := `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: codex
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(codexConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Write AGENTS.md (codex InstructionsFile) for tester.
	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	agentsContent := "# Role: Tester\n\nct droplet pass <id> --notes \"...\"\n"
	if err := os.WriteFile(filepath.Join(testerDir, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected extended checks to pass when AGENTS.md is present for codex provider")
	}
}

// TestRunDoctorExtendedChecks_ProviderInstructionsFile_MissingFails verifies that the
// doctor fails when the provider's InstructionsFile (e.g., AGENTS.md) is missing,
// even when CLAUDE.md exists.
func TestRunDoctorExtendedChecks_ProviderInstructionsFile_MissingFails(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	codexConfigYAML := `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: codex
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(codexConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Write CLAUDE.md but NOT AGENTS.md — provider is codex so AGENTS.md is checked.
	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when AGENTS.md is missing for codex provider")
	}
}

// TestRunDoctorExtendedChecks_UnknownProvider_FailsProviderCheck verifies that
// when the configured provider name is unknown, the doctor reports a check
// failure instead of silently defaulting to CLAUDE.md.
func TestRunDoctorExtendedChecks_UnknownProvider_FailsProviderCheck(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	for _, d := range []string{aqueductDir, filepath.Join(cisternDir, "cataractae"), filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	unknownProviderConfigYAML := `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: unknownprovider
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(unknownProviderConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when provider name is unknown")
	}
}

func TestRunDoctorExtendedChecks_FailsWhenWorkflowInvalid(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	for _, d := range []string{aqueductDir, filepath.Join(cisternDir, "cataractae"), filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Write an invalid (unparseable) workflow YAML.
	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(":::invalid yaml:::"), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when workflow YAML is invalid")
	}
}

// --- Provider binary checks (check 1) ---

func TestRunDoctorExtendedChecks_ProviderBinaryMissing_Fails(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Redirect PATH so 'claude' won't be found.
	emptyBinDir := t.TempDir()
	t.Setenv("PATH", emptyBinDir)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when provider binary is not in PATH")
	}
}

func TestProviderInstallHint_KnownPreset_ReturnsHint(t *testing.T) {
	tests := []struct {
		name     string
		wantHint bool
	}{
		{"claude", true},
		{"codex", true},
		{"gemini", true},
		{"opencode", false},
		{"copilot", false},
		{"unknown", false},
	}
	for _, tc := range tests {
		got := providerInstallHint(tc.name)
		if tc.wantHint && got == "" {
			t.Errorf("providerInstallHint(%q) = empty, want non-empty hint", tc.name)
		}
		if !tc.wantHint && got != "" {
			t.Errorf("providerInstallHint(%q) = %q, want empty", tc.name, got)
		}
	}
}

// --- Env var checks (check 2) ---

func TestRunDoctorExtendedChecks_EnvVarMissing_Fails(t *testing.T) {
	// Claude authenticates via its own OAuth file — no env var required.
	// Use codex (requires OPENAI_API_KEY) to test the env-var-missing path.
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Fake codex binary present, but OPENAI_API_KEY explicitly unset.
	setupFakeBinAndAPIKey(t, "codex", "")
	t.Setenv("OPENAI_API_KEY", "")

	codexWorkflow := strings.ReplaceAll(minimalWorkflowYAML, "provider: claude", "provider: codex")
	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(codexWorkflow), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	codexConfig := strings.ReplaceAll(minimalCisternConfigYAML, "provider: claude", "provider: codex")
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(codexConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "AGENTS.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when required env var is not set")
	}
}

// --- Agent file mismatch checks (check 3) ---

func TestRunDoctorExtendedChecks_AgentFileMismatch_OnlyClaudeMd_Fails(t *testing.T) {
	// Codex provider wants AGENTS.md, but only CLAUDE.md is present.
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	setupFakeBinAndAPIKey(t, "codex", "OPENAI_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigWithCodexYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	// Only CLAUDE.md — codex needs AGENTS.md.
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when provider needs AGENTS.md but only CLAUDE.md exists")
	}
}

func TestRunDoctorExtendedChecks_AgentFileCorrect_Passes(t *testing.T) {
	// Codex provider with AGENTS.md correctly present.
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	setupFakeBinAndAPIKey(t, "codex", "OPENAI_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigWithCodexYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	// AGENTS.md — correct for codex.
	if err := os.WriteFile(filepath.Join(testerDir, "AGENTS.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected extended checks to pass when correct instructions file is present")
	}
}

// --- LLM block validation (check 4) ---

func TestRunDoctorExtendedChecks_LLMCustomWithoutBaseURL_Fails(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	setupFakeBinAndAPIKey(t, "claude", "ANTHROPIC_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	// llm.provider=custom but no base_url.
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigWithCustomLLMYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if result {
		t.Error("expected extended checks to fail when llm.provider=custom but base_url is not set")
	}
}

func TestRunDoctorExtendedChecks_LLMCustomWithBaseURL_Passes(t *testing.T) {
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	setupFakeBinAndAPIKey(t, "claude", "ANTHROPIC_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	// llm.provider=custom with base_url set.
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigWithCustomLLMAndBaseURLYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "CLAUDE.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected extended checks to pass when llm.provider=custom and base_url is set")
	}
}

// --- Provider + LLM mismatch advisory (check 5) ---

func TestRunDoctorExtendedChecks_ProviderLLMMismatch_Advisory_NoCrash(t *testing.T) {
	// codex agent + anthropic LLM — advisory note, does not fail the check.
	home := t.TempDir()

	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	for _, d := range []string{aqueductDir, cataractaeDir, filepath.Join(cisternDir, "skills")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	setupFakeBinAndAPIKey(t, "codex", "OPENAI_API_KEY")

	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(minimalWorkflowYAML), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigWithMismatchYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	testerDir := filepath.Join(cataractaeDir, "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatalf("mkdir tester: %v", err)
	}
	// codex provider needs AGENTS.md.
	if err := os.WriteFile(filepath.Join(testerDir, "AGENTS.md"), []byte("ct droplet pass"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	// Mismatch advisory must not cause a crash and must not affect the ok result.
	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected provider+LLM mismatch advisory to be informational only (should not fail ok)")
	}
}

func TestInferLLMProviderFromPreset_KnownPresets(t *testing.T) {
	tests := []struct {
		presetName string
		want       string
	}{
		{"claude", "anthropic"},
		{"codex", "openai"},
		{"gemini", "gemini"},
		{"copilot", ""},
		{"opencode", ""},
		{"unknown", ""},
	}
	for _, tc := range tests {
		got := inferLLMProviderFromPreset(tc.presetName)
		if got != tc.want {
			t.Errorf("inferLLMProviderFromPreset(%q) = %q, want %q", tc.presetName, got, tc.want)
		}
	}
}

// --- checkOAuthTokenExpiry tests ---

func writeCredentials(t *testing.T, home string, expiresAtMs int64, accessToken string) {
	t.Helper()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	content := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, accessToken, expiresAtMs)
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

func TestCheckOAuthTokenExpiry_FreshToken_ReturnsTrue(t *testing.T) {
	home := t.TempDir()
	// Token expiring in 48 hours — well within fresh range.
	expiresAt := time.Now().Add(48 * time.Hour).UnixMilli()
	writeCredentials(t, home, expiresAt, "tok-fresh")

	result := checkOAuthTokenExpiry(home)
	if !result {
		t.Error("expected true for a fresh OAuth token")
	}
}

func TestCheckOAuthTokenExpiry_ExpiringSoon_ReturnsTrue(t *testing.T) {
	home := t.TempDir()
	// Token expiring in 12 hours — within 24h warn window but not expired.
	expiresAt := time.Now().Add(12 * time.Hour).UnixMilli()
	writeCredentials(t, home, expiresAt, "tok-soon")

	result := checkOAuthTokenExpiry(home)
	if !result {
		t.Error("expected true (warning only) when token expires within 24h")
	}
}

func TestCheckOAuthTokenExpiry_ExpiredToken_ReturnsFalse(t *testing.T) {
	home := t.TempDir()
	// Token that expired 1 hour ago.
	expiresAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeCredentials(t, home, expiresAt, "tok-expired")

	result := checkOAuthTokenExpiry(home)
	if result {
		t.Error("expected false for an already-expired OAuth token")
	}
}

func TestCheckOAuthTokenExpiry_MissingFile_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	// No .credentials.json — should return true (not a failure).
	result := checkOAuthTokenExpiry(home)
	if !result {
		t.Error("expected true when credentials file is absent (skip silently)")
	}
}

func TestCheckOAuthTokenExpiry_MalformedJSON_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte("not-json{{{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result := checkOAuthTokenExpiry(home)
	if !result {
		t.Error("expected true (skip silently) when credentials JSON is malformed")
	}
}

func TestCheckOAuthTokenExpiry_ZeroExpiresAt_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	writeCredentials(t, home, 0, "tok-noexpiry")

	result := checkOAuthTokenExpiry(home)
	if !result {
		t.Error("expected true (skip silently) when expiresAt is zero")
	}
}

// --- checkServiceTokenFreshness tests ---

func writeEnvConf(t *testing.T, home, apiKey string) {
	t.Helper()
	dropInDir := filepath.Join(home, ".config", "systemd", "user",
		"cistern-castellarius.service.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir drop-in dir: %v", err)
	}
	content := fmt.Sprintf("[Service]\nEnvironment=ANTHROPIC_API_KEY=%s\n", apiKey)
	if err := os.WriteFile(filepath.Join(dropInDir, "env.conf"), []byte(content), 0o644); err != nil {
		t.Fatalf("write env.conf: %v", err)
	}
}

func TestCheckServiceTokenFreshness_MatchingTokens_ReturnsTrue(t *testing.T) {
	home := t.TempDir()
	token := "sk-ant-matching-token"
	writeEnvConf(t, home, token)
	writeCredentials(t, home, time.Now().Add(48*time.Hour).UnixMilli(), token)

	result := checkServiceTokenFreshness(home)
	if !result {
		t.Error("expected true when service env token matches credentials token")
	}
}

func TestCheckServiceTokenFreshness_StaleToken_ReturnsFalse(t *testing.T) {
	home := t.TempDir()
	writeEnvConf(t, home, "sk-ant-old-token")
	writeCredentials(t, home, time.Now().Add(48*time.Hour).UnixMilli(), "sk-ant-new-token")

	result := checkServiceTokenFreshness(home)
	if result {
		t.Error("expected false when service env token differs from credentials token")
	}
}

func TestCheckServiceTokenFreshness_NoEnvConf_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	// No env.conf — skip silently.
	result := checkServiceTokenFreshness(home)
	if !result {
		t.Error("expected true (skip silently) when env.conf is absent")
	}
}

func TestCheckServiceTokenFreshness_NoAPIKeyInConf_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	dropInDir := filepath.Join(home, ".config", "systemd", "user",
		"cistern-castellarius.service.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// env.conf without ANTHROPIC_API_KEY.
	if err := os.WriteFile(filepath.Join(dropInDir, "env.conf"), []byte("[Service]\nEnvironment=PATH=/usr/bin\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	result := checkServiceTokenFreshness(home)
	if !result {
		t.Error("expected true (skip silently) when env.conf has no ANTHROPIC_API_KEY")
	}
}

func TestCheckServiceTokenFreshness_NoCredentialsFile_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	writeEnvConf(t, home, "sk-ant-some-token")
	// No credentials file.

	result := checkServiceTokenFreshness(home)
	if !result {
		t.Error("expected true (skip silently) when credentials file is absent")
	}
}

// --- fixOAuthToken tests ---

// writeCredentialsWithRefresh writes a credentials file that includes a refresh token.
func writeCredentialsWithRefresh(t *testing.T, home, accessToken, refreshToken string, expiresAtMs int64) {
	t.Helper()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	content := fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":%q,"refreshToken":%q,"expiresAt":%d}}`,
		accessToken, refreshToken, expiresAtMs,
	)
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

// startFakeOAuthServer starts an httptest server that returns a successful
// token refresh response with the given new access token and expiresIn seconds.
func startFakeOAuthServer(t *testing.T, newToken string, expiresIn int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d}`, newToken, expiresIn)
	}))
}

func TestFixOAuthToken_Success_WritesNewToken(t *testing.T) {
	home := t.TempDir()
	expiredAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeCredentialsWithRefresh(t, home, "tok-old", "tok-refresh", expiredAt)

	srv := startFakeOAuthServer(t, "tok-new", 3600)
	defer srv.Close()

	// Inject test server.
	origURL := doctorOAuthTokenURL
	origHTTP := doctorOAuthHTTPDo
	t.Cleanup(func() {
		doctorOAuthTokenURL = origURL
		doctorOAuthHTTPDo = origHTTP
	})
	doctorOAuthTokenURL = srv.URL
	doctorOAuthHTTPDo = srv.Client().Do

	if err := fixOAuthToken(home); err != nil {
		t.Fatalf("fixOAuthToken: %v", err)
	}

	// Verify credentials file was updated.
	creds := oauth.Read(home)
	if creds == nil {
		t.Fatal("expected non-nil credentials after fix")
	}
	if creds.AccessToken != "tok-new" {
		t.Errorf("AccessToken = %q, want tok-new", creds.AccessToken)
	}
	if creds.RefreshToken != "tok-refresh" {
		t.Errorf("RefreshToken = %q, want tok-refresh (must be preserved)", creds.RefreshToken)
	}
}

func TestFixOAuthToken_Success_UpdatesEnvConf(t *testing.T) {
	home := t.TempDir()
	expiredAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeCredentialsWithRefresh(t, home, "tok-old", "tok-refresh", expiredAt)
	writeEnvConf(t, home, "tok-old")

	srv := startFakeOAuthServer(t, "tok-new", 3600)
	defer srv.Close()

	origURL := doctorOAuthTokenURL
	origHTTP := doctorOAuthHTTPDo
	t.Cleanup(func() {
		doctorOAuthTokenURL = origURL
		doctorOAuthHTTPDo = origHTTP
	})
	doctorOAuthTokenURL = srv.URL
	doctorOAuthHTTPDo = srv.Client().Do

	// Stub out systemctl so we don't need a real systemd.
	origExecCommand := execCommandFn
	t.Cleanup(func() { execCommandFn = origExecCommand })
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		// Return a no-op command for systemctl calls.
		if name == "systemctl" {
			return exec.Command("true")
		}
		return exec.Command(name, args...)
	}

	if err := fixOAuthToken(home); err != nil {
		t.Fatalf("fixOAuthToken: %v", err)
	}

	envConfPath := filepath.Join(home, ".config", "systemd", "user",
		"cistern-castellarius.service.d", "env.conf")
	data, err := os.ReadFile(envConfPath)
	if err != nil {
		t.Fatalf("read env.conf: %v", err)
	}
	if !strings.Contains(string(data), "tok-new") {
		t.Errorf("env.conf not updated with new token: %s", data)
	}
	if strings.Contains(string(data), "tok-old") {
		t.Errorf("env.conf still contains old token: %s", data)
	}
}

func TestFixOAuthToken_NoCredentials_ReturnsError(t *testing.T) {
	home := t.TempDir()
	// No credentials file.
	if err := fixOAuthToken(home); err == nil {
		t.Error("expected error when credentials file is absent")
	}
}

func TestFixOAuthToken_NoRefreshToken_ReturnsError(t *testing.T) {
	home := t.TempDir()
	// Credentials without refresh token.
	writeCredentials(t, home, time.Now().Add(-1*time.Hour).UnixMilli(), "tok-old")

	if err := fixOAuthToken(home); err == nil {
		t.Error("expected error when refresh token is absent")
	}
}

func TestFixOAuthToken_RefreshHTTPError_ReturnsError(t *testing.T) {
	home := t.TempDir()
	writeCredentialsWithRefresh(t, home, "tok-old", "tok-refresh", time.Now().Add(-1*time.Hour).UnixMilli())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	origURL := doctorOAuthTokenURL
	origHTTP := doctorOAuthHTTPDo
	t.Cleanup(func() {
		doctorOAuthTokenURL = origURL
		doctorOAuthHTTPDo = origHTTP
	})
	doctorOAuthTokenURL = srv.URL
	doctorOAuthHTTPDo = srv.Client().Do

	if err := fixOAuthToken(home); err == nil {
		t.Error("expected error when OAuth refresh returns HTTP 401")
	}
}

// --- checkCisternEnvHasKey unit tests ---

func TestCheckCisternEnvHasKey_KeyPresent_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("ANTHROPIC_API_KEY=sk-ant-abc123\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "ANTHROPIC_API_KEY"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckCisternEnvHasKey_KeyAbsent_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("GH_TOKEN=ghp_abc\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "ANTHROPIC_API_KEY"); err == nil {
		t.Error("expected error when key is absent from env file")
	}
}

func TestCheckCisternEnvHasKey_KeyPresentButEmpty_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("ANTHROPIC_API_KEY=\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "ANTHROPIC_API_KEY"); err == nil {
		t.Error("expected error when key is present but has empty value")
	}
}

func TestCheckCisternEnvHasKey_FileAbsent_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "env")
	if err := checkCisternEnvHasKey(path, "ANTHROPIC_API_KEY"); err == nil {
		t.Error("expected error when env file does not exist")
	}
}

func TestCheckCisternEnvHasKey_CommentsAndBlankLines_Ignored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "# credentials\n\nANTHROPIC_API_KEY=sk-ant-real\nGH_TOKEN=ghp_abc\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "ANTHROPIC_API_KEY"); err != nil {
		t.Errorf("unexpected error with comments and blank lines: %v", err)
	}
}

func TestCheckCisternEnvHasKey_MultipleKeys_FindsCorrectOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "GH_TOKEN=ghp_abc\nANTHROPIC_API_KEY=sk-ant-real\nEXTRA_VAR=value\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "ANTHROPIC_API_KEY"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- fixCisternEnvFile unit tests ---

func TestFixCisternEnvFile_CreatesFileWithRestrictedPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".cistern", "env")

	if err := fixCisternEnvFile(path); err != nil {
		t.Fatalf("fixCisternEnvFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0600, got %04o", perm)
	}
}

func TestFixCisternEnvFile_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dirs", "env")

	if err := fixCisternEnvFile(path); err != nil {
		t.Fatalf("fixCisternEnvFile: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("env file was not created in nested dirs")
	}
}

func TestFixCisternEnvFile_ExistingFile_IsNotModified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")

	existing := []byte("ANTHROPIC_API_KEY=sk-ant-existing\n")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	if err := fixCisternEnvFile(path); err != nil {
		t.Fatalf("fixCisternEnvFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(existing) {
		t.Error("existing env file content was modified")
	}
}

func TestFixCisternEnvFile_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")

	for i := 0; i < 3; i++ {
		if err := fixCisternEnvFile(path); err != nil {
			t.Fatalf("run %d: fixCisternEnvFile: %v", i+1, err)
		}
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("env file does not exist after idempotent runs")
	}
}

func TestFixCisternEnvFile_NewFile_ContainsCommentStub(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")

	if err := fixCisternEnvFile(path); err != nil {
		t.Fatalf("fixCisternEnvFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(data), "ANTHROPIC_API_KEY") {
		t.Error("new env file does not contain ANTHROPIC_API_KEY comment stub")
	}
	if !strings.Contains(string(data), "#") {
		t.Error("new env file does not contain comment lines")
	}
}

// TestFixCisternEnvFile_StatError_ReturnsError verifies that when os.Stat
// returns a non-IsNotExist error (e.g. EACCES), fixCisternEnvFile propagates
// it instead of silently swallowing it.
func TestFixCisternEnvFile_StatError_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")

	origStatFn := osStatFn
	t.Cleanup(func() { osStatFn = origStatFn })
	syntheticErr := fmt.Errorf("permission denied")
	osStatFn = func(name string) (os.FileInfo, error) {
		if name == path {
			return nil, syntheticErr
		}
		return os.Stat(name)
	}

	err := fixCisternEnvFile(path)
	if err == nil {
		t.Fatal("expected error when stat returns a non-IsNotExist error, got nil")
	}
	if !strings.Contains(err.Error(), "stat env file") {
		t.Errorf("expected error to contain 'stat env file', got: %v", err)
	}
}

// --- installSystemdService tests ---

// setupInstallSystemdServiceTest redirects HOME to a temp dir and stubs out
// resolveGoBinFn and execCommandFn so installSystemdService does not need a
// real Go installation or a running systemd. Returns the temp home directory.
func setupInstallSystemdServiceTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	fakeGobin := t.TempDir()
	origResolveGoBinFn := resolveGoBinFn
	t.Cleanup(func() { resolveGoBinFn = origResolveGoBinFn })
	resolveGoBinFn = func() (string, error) { return fakeGobin, nil }

	origExecCommandFn := execCommandFn
	t.Cleanup(func() { execCommandFn = origExecCommandFn })
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if name == "systemctl" {
			return exec.Command("true")
		}
		return exec.Command(name, args...)
	}

	return home
}

func TestInstallSystemdService_WritesWrapperScript(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	wrapperPath := filepath.Join(home, ".cistern", "start-castellarius.sh")
	info, err := os.Stat(wrapperPath)
	if err != nil {
		t.Fatalf("wrapper script not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o111 == 0 {
		t.Errorf("wrapper script not executable: mode %04o", perm)
	}
	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read wrapper script: %v", err)
	}
	if !strings.Contains(string(data), "castellarius start") {
		t.Error("wrapper script does not contain 'castellarius start'")
	}
}

func TestInstallSystemdService_WrapperScriptNotOverwritten(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	// Pre-create the wrapper with custom content.
	cisternDir := filepath.Join(home, ".cistern")
	if err := os.MkdirAll(cisternDir, 0o755); err != nil {
		t.Fatalf("mkdir cistern: %v", err)
	}
	wrapperPath := filepath.Join(cisternDir, "start-castellarius.sh")
	custom := []byte("#!/bin/bash\n# custom wrapper\n")
	if err := os.WriteFile(wrapperPath, custom, 0o755); err != nil {
		t.Fatalf("write custom wrapper: %v", err)
	}

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	data, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if string(data) != string(custom) {
		t.Error("existing wrapper script was overwritten")
	}
}

func TestInstallSystemdService_CreatesEnvStubIfAbsent(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	envPath := filepath.Join(home, ".cistern", "env")
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("env file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("env file has wrong permissions: got %04o, want 0600", perm)
	}
}

func TestInstallSystemdService_PreservesExistingEnvFile(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	cisternDir := filepath.Join(home, ".cistern")
	if err := os.MkdirAll(cisternDir, 0o755); err != nil {
		t.Fatalf("mkdir cistern: %v", err)
	}
	envPath := filepath.Join(cisternDir, "env")
	existing := []byte("ANTHROPIC_API_KEY=sk-ant-existing\n")
	if err := os.WriteFile(envPath, existing, 0o600); err != nil {
		t.Fatalf("write existing env: %v", err)
	}

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if string(data) != string(existing) {
		t.Errorf("existing env file was modified: got %q, want %q", string(data), string(existing))
	}
}

func TestInstallSystemdService_AddsEnvToGitignore(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	gitignorePath := filepath.Join(home, ".cistern", ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "env") {
		t.Error(".gitignore does not contain 'env'")
	}
}

func TestInstallSystemdService_ServiceFileUsesWrapperScript(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	svcPath := filepath.Join(home, ".config", "systemd", "user", "cistern-castellarius.service")
	data, err := os.ReadFile(svcPath)
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	wrapperPath := filepath.Join(home, ".cistern", "start-castellarius.sh")
	want := "ExecStart=" + wrapperPath
	if !strings.Contains(string(data), want) {
		t.Errorf("service file ExecStart does not point to wrapper script; want %q in:\n%s", want, data)
	}
}

func TestInstallSystemdService_ServiceFileHasNoAnthropicAPIKey(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	svcPath := filepath.Join(home, ".config", "systemd", "user", "cistern-castellarius.service")
	data, err := os.ReadFile(svcPath)
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	if strings.Contains(string(data), "ANTHROPIC_API_KEY") {
		t.Error("service file must not contain ANTHROPIC_API_KEY — credentials are loaded by the wrapper script")
	}
}

// TestInstallSystemdService_WrapperStatError_ReturnsError verifies that when
// os.Stat on the wrapper path returns a non-IsNotExist error (e.g. EACCES),
// installSystemdService propagates the error instead of silently continuing.
func TestInstallSystemdService_WrapperStatError_ReturnsError(t *testing.T) {
	setupInstallSystemdServiceTest(t)

	// Inject a stat function that returns a non-IsNotExist error for the wrapper path.
	origStatFn := osStatFn
	t.Cleanup(func() { osStatFn = origStatFn })
	syntheticErr := fmt.Errorf("permission denied")
	osStatFn = func(name string) (os.FileInfo, error) {
		if strings.HasSuffix(name, "start-castellarius.sh") {
			return nil, syntheticErr
		}
		return os.Stat(name)
	}

	err := installSystemdService()
	if err == nil {
		t.Fatal("expected error when stat returns a non-IsNotExist error, got nil")
	}
	if !strings.Contains(err.Error(), "stat wrapper script") {
		t.Errorf("expected error to contain 'stat wrapper script', got: %v", err)
	}
}

// TestCheckSystemdServiceEnv_NoAPIKeyCheck verifies that checkSystemdServiceEnv
// does NOT produce a warning about ANTHROPIC_API_KEY being absent from the
// service environment. ANTHROPIC_API_KEY is now loaded at runtime by the wrapper
// script sourcing ~/.cistern/env, so it will never appear in systemd's
// Environment property — reporting its absence as a failure would be a false positive.
func TestCheckSystemdServiceEnv_NoAPIKeyCheck(t *testing.T) {
	// Inject a fake systemctl that returns a service env with no ANTHROPIC_API_KEY.
	origFn := checkSystemdEnvFn
	t.Cleanup(func() { checkSystemdEnvFn = origFn })
	checkSystemdEnvFn = func(_ string) ([]byte, error) {
		return []byte("Environment=PATH=/usr/local/bin:/usr/bin:/bin\n"), nil
	}

	// Capture stdout to verify no ANTHROPIC_API_KEY warning is emitted.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	checkSystemdServiceEnv("cistern-castellarius", nil)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if strings.Contains(output, "ANTHROPIC_API_KEY") {
		t.Errorf("checkSystemdServiceEnv emitted an ANTHROPIC_API_KEY warning; output:\n%s", output)
	}
}

// --- Provider-aware env file checks ---

// TestDoctorEnvCheck_GeminiProvider_GeminiKeySet_Passes verifies that the doctor
// env file check passes for a gemini-configured setup when GEMINI_API_KEY is
// present in ~/.cistern/env and ANTHROPIC_API_KEY is absent.
func TestDoctorEnvCheck_GeminiProvider_GeminiKeySet_Passes(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "gemini")
	envPath := filepath.Join(filepath.Dir(cfgPath), "env")
	if err := os.WriteFile(envPath, []byte("GEMINI_API_KEY=gemini-test-key\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	requiredVars, _ := startupRequiredEnvVars(cfgPath)
	for _, key := range requiredVars {
		if err := checkCisternEnvHasKey(envPath, key); err != nil {
			t.Errorf("env check for %s failed: %v", key, err)
		}
	}
}

// TestDoctorEnvCheck_GeminiProvider_GeminiKeyMissing_Fails verifies that the
// doctor env file check fails for a gemini setup when GEMINI_API_KEY is absent.
func TestDoctorEnvCheck_GeminiProvider_GeminiKeyMissing_Fails(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "gemini")
	envPath := filepath.Join(filepath.Dir(cfgPath), "env")
	// Env file with only ANTHROPIC_API_KEY — not what gemini needs.
	if err := os.WriteFile(envPath, []byte("ANTHROPIC_API_KEY=sk-ant-test\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	requiredVars, _ := startupRequiredEnvVars(cfgPath)
	for _, key := range requiredVars {
		if err := checkCisternEnvHasKey(envPath, key); err == nil {
			t.Errorf("expected env check for %s to fail when key is absent from env file", key)
		}
	}
}
