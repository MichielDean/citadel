package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRootCmd_NoLogoOnCommandExecution verifies that the ASCII logo is never
// printed when a CLI command runs.
// Given: a logo file exists and CT_ASCII_LOGO points to it,
// When: any command executes (triggering PersistentPreRun),
// Then: the logo content does not appear in stdout.
func TestRootCmd_NoLogoOnCommandExecution(t *testing.T) {
	// Create a recognisable logo file in a temp directory.
	logoContent := "%%%%CISTERN_LOGO%%%%\n"
	logoPath := filepath.Join(t.TempDir(), "cistern_logo_ascii.txt")
	if err := os.WriteFile(logoPath, []byte(logoContent), 0o600); err != nil {
		t.Fatalf("failed to write test logo file: %v", err)
	}
	t.Setenv("CT_ASCII_LOGO", logoPath)
	// Ensure the opt-out env var is NOT set, so there is no false-pass from the guard.
	t.Setenv("CT_NO_ASCII_LOGO", "")

	// Capture stdout so we can inspect what was printed.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	rootCmd.SetArgs([]string{"version"})
	_ = rootCmd.Execute()

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read captured stdout: %v", err)
	}

	if strings.Contains(buf.String(), "%%%%CISTERN_LOGO%%%%") {
		t.Error("ASCII logo must not be printed on command execution; got logo in stdout")
	}
}
