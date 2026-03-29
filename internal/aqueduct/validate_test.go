package aqueduct

import (
	"strings"
	"testing"
)

// minimalValidConfig returns the smallest valid AqueductConfig for use as a
// base in table-driven tests.
func minimalValidConfig() AqueductConfig {
	return AqueductConfig{
		Repos: []RepoConfig{
			{Name: "test-repo", Cataractae: 1, Prefix: "t"},
		},
	}
}

// --- ArchitectiConfig validation ---

func TestValidateAqueductConfig_Architecti_Nil_NoError(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Architecti = nil
	if err := ValidateAqueductConfig(&cfg); err != nil {
		t.Errorf("unexpected error with nil Architecti: %v", err)
	}
}

func TestValidateAqueductConfig_Architecti_Valid_NoError(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: 30,
		MaxFilesPerRun:   50,
	}
	if err := ValidateAqueductConfig(&cfg); err != nil {
		t.Errorf("unexpected error with valid Architecti config: %v", err)
	}
}

func TestValidateAqueductConfig_Architecti_DisabledWithValidValues_NoError(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          false,
		ThresholdMinutes: 30,
		MaxFilesPerRun:   10,
	}
	if err := ValidateAqueductConfig(&cfg); err != nil {
		t.Errorf("unexpected error when Architecti is disabled with valid values: %v", err)
	}
}

func TestValidateAqueductConfig_Architecti_NegativeThreshold_ReturnsError(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: -1,
		MaxFilesPerRun:   10,
	}
	err := ValidateAqueductConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for negative threshold_minutes, got nil")
	}
	if !strings.Contains(err.Error(), "threshold_minutes") {
		t.Errorf("error %q does not mention threshold_minutes", err.Error())
	}
}

func TestValidateAqueductConfig_Architecti_ZeroThreshold_NoError(t *testing.T) {
	// threshold_minutes == 0 is valid (zero means "no minimum wait")
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: 0,
		MaxFilesPerRun:   10,
	}
	if err := ValidateAqueductConfig(&cfg); err != nil {
		t.Errorf("unexpected error for zero threshold_minutes: %v", err)
	}
}

func TestValidateAqueductConfig_Architecti_ZeroMaxFiles_ReturnsError(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: 30,
		MaxFilesPerRun:   0,
	}
	err := ValidateAqueductConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for zero max_files_per_run, got nil")
	}
	if !strings.Contains(err.Error(), "max_files_per_run") {
		t.Errorf("error %q does not mention max_files_per_run", err.Error())
	}
}

func TestValidateAqueductConfig_Architecti_NegativeMaxFiles_ReturnsError(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          true,
		ThresholdMinutes: 30,
		MaxFilesPerRun:   -1,
	}
	err := ValidateAqueductConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for negative max_files_per_run, got nil")
	}
	if !strings.Contains(err.Error(), "max_files_per_run") {
		t.Errorf("error %q does not mention max_files_per_run", err.Error())
	}
}

func TestValidateAqueductConfig_Architecti_NegativeThresholdWhenDisabled_ReturnsError(t *testing.T) {
	// Validation applies regardless of Enabled — invalid values are rejected at startup.
	cfg := minimalValidConfig()
	cfg.Architecti = &ArchitectiConfig{
		Enabled:          false,
		ThresholdMinutes: -5,
		MaxFilesPerRun:   10,
	}
	err := ValidateAqueductConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for negative threshold_minutes even when disabled, got nil")
	}
}
