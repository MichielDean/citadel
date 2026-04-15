package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// --- TestCheckCastellariusHealth ---

func TestCheckCastellariusHealth_FileMissing_WarnsMissing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if !strings.Contains(out, "castellarius health file missing") {
		t.Errorf("expected missing-file warning, got: %q", out)
	}
	if !strings.Contains(out, "is castellarius running?") {
		t.Errorf("expected 'is castellarius running?' in output, got: %q", out)
	}
}

func TestCheckCastellariusHealth_FreshFile_Silent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	pollSec := 10
	// Write a health file with lastTickAt = now (well within threshold).
	hPath := filepath.Join(dir, "castellarius.health")
	content := fmt.Sprintf(`{"lastTickAt":%q,"pollIntervalSec":%d,"droughtRunning":false,"droughtStartedAt":null}`,
		time.Now().UTC().Format(time.RFC3339Nano), pollSec)
	if err := os.WriteFile(hPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if out != "" {
		t.Errorf("expected no output for healthy file, got: %q", out)
	}
}

func TestCheckCastellariusHealth_StaleTick_WarnsHung(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	pollSec := 10
	// lastTickAt is 5 minutes ago — well beyond 3×10s = 30s threshold.
	staleTime := time.Now().UTC().Add(-5 * time.Minute)
	hPath := filepath.Join(dir, "castellarius.health")
	content := fmt.Sprintf(`{"lastTickAt":%q,"pollIntervalSec":%d,"droughtRunning":false,"droughtStartedAt":null}`,
		staleTime.Format(time.RFC3339Nano), pollSec)
	if err := os.WriteFile(hPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if !strings.Contains(out, "scheduler may be hung") {
		t.Errorf("expected stale-tick warning, got: %q", out)
	}
	// Verify the expected threshold (3 × pollIntervalSec) appears in the output.
	expected := fmt.Sprintf("<%ds", pollSec*3)
	if !strings.Contains(out, expected) {
		t.Errorf("expected threshold %q in output, got: %q", expected, out)
	}
}

func TestCheckCastellariusHealth_DroughtTooLong_WarnsDroughtHang(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	pollSec := 10
	// Recent tick — passes tick check. Drought running for 15 minutes — exceeds 10m threshold.
	droughtStart := time.Now().UTC().Add(-15 * time.Minute)
	hPath := filepath.Join(dir, "castellarius.health")
	content := fmt.Sprintf(`{"lastTickAt":%q,"pollIntervalSec":%d,"droughtRunning":true,"droughtStartedAt":%q}`,
		time.Now().UTC().Format(time.RFC3339Nano), pollSec, droughtStart.Format(time.RFC3339Nano))
	if err := os.WriteFile(hPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if !strings.Contains(out, "drought goroutine has been running") {
		t.Errorf("expected drought warning, got: %q", out)
	}
	if !strings.Contains(out, "possible hang") {
		t.Errorf("expected 'possible hang' in output, got: %q", out)
	}
}

func TestCheckCastellariusHealth_DroughtRecent_Silent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	pollSec := 10
	// Drought running for only 2 minutes — below 10m threshold.
	droughtStart := time.Now().UTC().Add(-2 * time.Minute)
	hPath := filepath.Join(dir, "castellarius.health")
	content := fmt.Sprintf(`{"lastTickAt":%q,"pollIntervalSec":%d,"droughtRunning":true,"droughtStartedAt":%q}`,
		time.Now().UTC().Format(time.RFC3339Nano), pollSec, droughtStart.Format(time.RFC3339Nano))
	if err := os.WriteFile(hPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if out != "" {
		t.Errorf("expected no output for recent drought, got: %q", out)
	}
}

func TestCheckCastellariusHealth_CorruptFile_WarnsUnreadable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	hPath := filepath.Join(dir, "castellarius.health")
	// Write invalid JSON to simulate a corrupted health file.
	if err := os.WriteFile(hPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if !strings.Contains(out, "castellarius health file unreadable") {
		t.Errorf("expected unreadable warning for corrupt file, got: %q", out)
	}
}

func TestCheckCastellariusHealth_ZeroPollIntervalSec_NoFalsePositive(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	hPath := filepath.Join(dir, "castellarius.health")
	// pollIntervalSec=0 (missing/corrupted field) with a recent tick —
	// must not produce a spurious "scheduler may be hung" warning.
	content := fmt.Sprintf(`{"lastTickAt":%q,"pollIntervalSec":0,"droughtRunning":false,"droughtStartedAt":null}`,
		time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(hPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if strings.Contains(out, "scheduler may be hung") {
		t.Errorf("expected no hung warning for zero pollIntervalSec, got: %q", out)
	}
}

func TestCheckCastellariusHealth_NearThresholdStaleTick_ShowsSeconds(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cistern.db")
	pollSec := 10
	// lastTickAt is 31 seconds ago — just beyond 3×10s = 30s threshold.
	staleTime := time.Now().UTC().Add(-31 * time.Second)
	hPath := filepath.Join(dir, "castellarius.health")
	content := fmt.Sprintf(`{"lastTickAt":%q,"pollIntervalSec":%d,"droughtRunning":false,"droughtStartedAt":null}`,
		staleTime.Format(time.RFC3339Nano), pollSec)
	if err := os.WriteFile(hPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write health file: %v", err)
	}

	out := captureStdout(t, func() { checkCastellariusHealth(dbPath) })

	if !strings.Contains(out, "scheduler may be hung") {
		t.Errorf("expected stale-tick warning, got: %q", out)
	}
	// Near-threshold staleness must show seconds, not the misleading "0m ago".
	if strings.Contains(out, "0m ago") {
		t.Errorf("output must not contain '0m ago' for sub-minute staleness, got: %q", out)
	}
	if !strings.Contains(out, "s ago") {
		t.Errorf("expected seconds unit ('s ago') in stale-tick output, got: %q", out)
	}
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
	t.Setenv("GH_TOKEN", "test-key")

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
		{"opencode", true},
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
		{"copilot", "openai"},
		{"opencode", "ollama"},
		{"unknown", ""},
	}
	for _, tc := range tests {
		got := inferLLMProviderFromPreset(tc.presetName)
		if got != tc.want {
			t.Errorf("inferLLMProviderFromPreset(%q) = %q, want %q", tc.presetName, got, tc.want)
		}
	}
}

// --- runDoctorProviderChecks tests ---

func TestRunDoctorProviderChecks_ClaudeProvider_ChecksClaudeBinaryAndAuth(t *testing.T) {
	home := t.TempDir()
	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	if err := os.MkdirAll(aqueductDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	setupFakeBinAndAPIKey(t, "claude", "")

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	orig := providerAuthStatusFn
	t.Cleanup(func() { providerAuthStatusFn = orig })
	providerAuthStatusFn = func() error { return nil }

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	result := runDoctorProviderChecks(cfg)
	if !result {
		t.Error("expected provider checks to pass for claude provider with binary and auth")
	}
}

func TestRunDoctorProviderChecks_OpencodeProvider_ChecksOpencodeOnly(t *testing.T) {
	home := t.TempDir()
	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	if err := os.MkdirAll(aqueductDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Set up fake opencode binary, but NOT claude.
	setupFakeBinAndAPIKey(t, "opencode", "")

	opencodeConfigYAML := `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: opencode
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(opencodeConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	result := runDoctorProviderChecks(cfg)
	if !result {
		t.Error("expected provider checks to pass for opencode provider without claude")
	}
}

func TestRunDoctorProviderChecks_OpencodeProvider_ClaudeMissingNoFailure(t *testing.T) {
	home := t.TempDir()
	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	if err := os.MkdirAll(aqueductDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// PATH with opencode but no claude.
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create fake opencode: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	opencodeConfigYAML := `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: opencode
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(opencodeConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	result := runDoctorProviderChecks(cfg)
	if !result {
		t.Error("expected provider checks to pass for opencode provider even when claude is missing")
	}
}

func TestRunDoctorProviderChecks_MissingBinary_Fails(t *testing.T) {
	home := t.TempDir()
	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	if err := os.MkdirAll(aqueductDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Empty PATH — no binaries found.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(minimalCisternConfigYAML), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	result := runDoctorProviderChecks(cfg)
	if result {
		t.Error("expected provider checks to fail when provider binary is missing")
	}
}

// --- providerAuthStatusFn (claude CLI authenticated) tests ---

func TestClaudeAuthenticated_ExitZero_PassesCheck(t *testing.T) {
	orig := providerAuthStatusFn
	t.Cleanup(func() { providerAuthStatusFn = orig })
	providerAuthStatusFn = func() error { return nil }

	got := checkWithFix("claude CLI authenticated", providerAuthStatusFn, nil)
	if !got {
		t.Error("expected checkWithFix to return true when claude auth status exits 0")
	}
}

func TestClaudeAuthenticated_NonZeroExit_FailsCheck(t *testing.T) {
	orig := providerAuthStatusFn
	t.Cleanup(func() { providerAuthStatusFn = orig })
	providerAuthStatusFn = func() error {
		return fmt.Errorf("Not logged in")
	}

	got := checkWithFix("claude CLI authenticated", providerAuthStatusFn, nil)
	if got {
		t.Error("expected checkWithFix to return false when claude auth status exits non-zero")
	}
}

// --- checkCisternEnvHasKey unit tests ---

func TestCheckCisternEnvHasKey_KeyPresent_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=sk-test123\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "OPENAI_API_KEY"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckCisternEnvHasKey_KeyAbsent_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("GH_TOKEN=ghp_abc\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "OPENAI_API_KEY"); err == nil {
		t.Error("expected error when key is absent from env file")
	}
}

func TestCheckCisternEnvHasKey_KeyPresentButEmpty_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "OPENAI_API_KEY"); err == nil {
		t.Error("expected error when key is present but has empty value")
	}
}

func TestCheckCisternEnvHasKey_FileAbsent_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "env")
	if err := checkCisternEnvHasKey(path, "OPENAI_API_KEY"); err == nil {
		t.Error("expected error when env file does not exist")
	}
}

func TestCheckCisternEnvHasKey_CommentsAndBlankLines_Ignored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "# credentials\n\nOPENAI_API_KEY=sk-test-real\nGH_TOKEN=ghp_abc\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "OPENAI_API_KEY"); err != nil {
		t.Errorf("unexpected error with comments and blank lines: %v", err)
	}
}

func TestCheckCisternEnvHasKey_MultipleKeys_FindsCorrectOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env")
	content := "GH_TOKEN=ghp_abc\nOPENAI_API_KEY=sk-test-real\nEXTRA_VAR=value\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	if err := checkCisternEnvHasKey(path, "OPENAI_API_KEY"); err != nil {
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

	existing := []byte("OPENAI_API_KEY=sk-test-existing\n")
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
	if !strings.Contains(string(data), "OPENAI_API_KEY") {
		t.Error("new env file does not contain OPENAI_API_KEY comment stub")
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
	existing := []byte("OPENAI_API_KEY=sk-test-existing\n")
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

func TestInstallSystemdService_ServiceFileHasNoHardcodedAPIKey(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	svcPath := filepath.Join(home, ".config", "systemd", "user", "cistern-castellarius.service")
	data, err := os.ReadFile(svcPath)
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GH_TOKEN"} {
		if strings.Contains(string(data), key) {
			t.Errorf("service file must not contain %s — credentials are loaded by the wrapper script", key)
		}
	}
}

// TestInstallSystemdService_ServiceFileHasEnvironmentFile verifies that the
// generated service file contains an EnvironmentFile directive pointing to
// ~/.cistern/env so that GH_TOKEN and other vars from that file are available
// to the castellarius process without being sourced by the wrapper script.
func TestInstallSystemdService_ServiceFileHasEnvironmentFile(t *testing.T) {
	home := setupInstallSystemdServiceTest(t)

	if err := installSystemdService(); err != nil {
		t.Fatalf("installSystemdService: %v", err)
	}

	svcPath := filepath.Join(home, ".config", "systemd", "user", "cistern-castellarius.service")
	data, err := os.ReadFile(svcPath)
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	want := "EnvironmentFile=-" + filepath.Join(home, ".cistern", "env")
	if !strings.Contains(string(data), want) {
		t.Errorf("service file missing EnvironmentFile directive; want %q in:\n%s", want, data)
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
// does NOT produce a warning about provider API keys being absent from the
// service environment. Provider keys are now loaded at runtime via the
// EnvironmentFile directive (~/.cistern/env), so they will never appear in
// systemd's Environment property — reporting their absence as a failure
// would be a false positive.
func TestCheckSystemdServiceEnv_NoAPIKeyCheck(t *testing.T) {
	// Inject a fake systemctl that returns a service env with no provider API keys.
	origFn := checkSystemdEnvFn
	t.Cleanup(func() { checkSystemdEnvFn = origFn })
	checkSystemdEnvFn = func(_ string) ([]byte, error) {
		return []byte("Environment=PATH=/usr/local/bin:/usr/bin:/bin\n"), nil
	}

	// Capture stdout to verify no provider API key warning is emitted.
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

	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		if strings.Contains(output, key) {
			t.Errorf("checkSystemdServiceEnv emitted a %s warning; output:\n%s", key, output)
		}
	}
}

// --- Provider-aware env file checks ---

// TestDoctorEnvCheck_GeminiProvider_GeminiKeySet_Passes verifies that the doctor
// env file check passes for a gemini-configured setup when GEMINI_API_KEY is
// present in ~/.cistern/env and no other provider keys are set.
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

// --- Installer stubs regression test (ci-7isae) ---

// TestRunDoctorExtendedChecks_DefaultWorkflow_InstallerStubs_Passes verifies that
// the skills installed by _install_skill_stubs in tests/installer/run-tests.sh
// are sufficient for runDoctorExtendedChecks to pass with the default workflow.
//
// If the default workflow (cmd/ct/assets/aqueduct/aqueduct.yaml) is updated to
// add, rename, or remove a skill, this test will fail. You MUST then update:
//   - installerStubs below
//   - _install_skill_stubs in tests/installer/run-tests.sh
//   - the skill lists in run-installer-tests.sh (3 occurrences)
func TestRunDoctorExtendedChecks_DefaultWorkflow_InstallerStubs_Passes(t *testing.T) {
	home := t.TempDir()
	cisternDir := filepath.Join(home, ".cistern")
	aqueductDir := filepath.Join(cisternDir, "aqueduct")
	cataractaeDir := filepath.Join(cisternDir, "cataractae")
	skillsDir := filepath.Join(cisternDir, "skills")
	for _, d := range []string{aqueductDir, cataractaeDir, skillsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Fake claude binary (default provider — no API key required).
	setupFakeBinAndAPIKey(t, "claude", "")

	// Write the default embedded workflow.
	wfPath := filepath.Join(aqueductDir, "aqueduct.yaml")
	if err := os.WriteFile(wfPath, defaultAqueductWorkflow, 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}

	// Write a config that references the default workflow.
	cfgContent := `repos:
  - name: cistern
    url: https://github.com/example/cistern
    workflow_path: aqueduct/aqueduct.yaml
    cataractae: 1
    prefix: ct
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Generate CLAUDE.md files for all identities, mirroring what ct init does.
	w, err := aqueduct.ParseWorkflowBytes(defaultAqueductWorkflow)
	if err != nil {
		t.Fatalf("parse default workflow: %v", err)
	}
	if err := initCataractaeDir(w, cataractaeDir); err != nil {
		t.Fatalf("init cataractae dir: %v", err)
	}
	if _, err := aqueduct.GenerateCataractaeFiles(w, cataractaeDir, ""); err != nil {
		t.Fatalf("generate CLAUDE.md files: %v", err)
	}

	// installerStubs mirrors _install_skill_stubs in tests/installer/run-tests.sh.
	// Keep these two lists in sync — this test will fail if the default workflow
	// requires a skill not present here, signalling that run-tests.sh needs updating.
	installerStubs := []string{
		"cistern-droplet-state",
		"cistern-git",
		"cistern-github",
		"cistern-reviewer",
		"critical-code-reviewer",
	}
	for _, name := range installerStubs {
		dir := filepath.Join(skillsDir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+" stub\n"), 0o644); err != nil {
			t.Fatalf("write skill stub %s: %v", name, err)
		}
	}

	cfg, err := aqueduct.ParseAqueductConfig(cfgPath)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	dbPath := filepath.Join(cisternDir, "cistern.db")
	result := runDoctorExtendedChecks(cfg, cfgPath, home, dbPath)
	if !result {
		t.Error("expected runDoctorExtendedChecks to pass with installer stubs and default workflow; " +
			"if the default workflow changed its required skills, update installerStubs above, " +
			"_install_skill_stubs in tests/installer/run-tests.sh, " +
			"and the skill lists in run-installer-tests.sh (3 occurrences)")
	}
}

// TestDoctorEnvCheck_GeminiProvider_GeminiKeyMissing_Fails verifies that the
// doctor env file check fails for a gemini setup when GEMINI_API_KEY is absent.
func TestDoctorEnvCheck_GeminiProvider_GeminiKeyMissing_Fails(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "gemini")
	envPath := filepath.Join(filepath.Dir(cfgPath), "env")
	// Env file with only OPENAI_API_KEY — not what gemini needs.
	if err := os.WriteFile(envPath, []byte("OPENAI_API_KEY=sk-test123\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}

	requiredVars, _ := startupRequiredEnvVars(cfgPath)
	for _, key := range requiredVars {
		if err := checkCisternEnvHasKey(envPath, key); err == nil {
			t.Errorf("expected env check for %s to fail when key is absent from env file", key)
		}
	}
}
