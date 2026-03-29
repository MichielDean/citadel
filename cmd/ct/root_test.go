package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// TestRootCmd_NoLogoOnCommandExecution verifies that no ASCII logo is printed
// when a CLI command runs.
// Given: a CLI command is executed,
// When: the command completes,
// Then: no logo content appears in stdout.
func TestRootCmd_NoLogoOnCommandExecution(t *testing.T) {
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
