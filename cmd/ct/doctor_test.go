package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
