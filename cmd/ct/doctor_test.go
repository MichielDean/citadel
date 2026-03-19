package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
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

	// Workflow with cataracta_definitions so GenerateCataractaFiles can write CLAUDE.md.
	workflowWithDefs := `name: test
cataracta_definitions:
  tester:
    name: Tester
    description: "Test role."
    instructions: |
      ct droplet pass <id> --notes "done"
cataractae:
  - name: implement
    type: agent
    identity: tester
    on_pass: done
`
	wfPath := filepath.Join(aqueductDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(workflowWithDefs), 0o644); err != nil {
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

	doctorFix = true
	defer func() { doctorFix = false }()

	// CLAUDE.md is absent — fix should regenerate it, then check should pass.
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
