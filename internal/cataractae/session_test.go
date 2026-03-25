package cataractae

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
)

// syncBuffer wraps bytes.Buffer with a mutex so that concurrent Write calls
// (from background goroutines writing via slog) and String() calls (from the
// test polling loop) do not race under go test -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureDefaultSlog temporarily replaces slog.Default() with a buffer-backed
// text logger for the duration of the test, then restores the original.
// Returns the buffer whose String() can be inspected for log output.
func captureDefaultSlog(t *testing.T) *syncBuffer {
	t.Helper()
	prev := slog.Default()
	buf := &syncBuffer{}
	l := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(l)
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestBuildClaudeCmd_ContainsAddDir(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp"}
	skillsDir := "/home/user/.cistern/skills"
	cmd := s.buildClaudeCmd(skillsDir)
	if !strings.Contains(cmd, "--add-dir") {
		t.Errorf("claudeCmd missing --add-dir flag: %s", cmd)
	}
	if !strings.Contains(cmd, skillsDir) {
		t.Errorf("claudeCmd missing skillsDir %q: %s", skillsDir, cmd)
	}
}

func TestBuildClaudeCmd_QuotesPathWithSpaces(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp"}
	skillsDir := "/home/john doe/.cistern/skills"
	cmd := s.buildClaudeCmd(skillsDir)

	// Unquoted form must not appear — it would split at the space.
	if strings.Contains(cmd, "--add-dir /home/john doe/") {
		t.Errorf("claudeCmd contains unquoted path with space — will break shell: %s", cmd)
	}
	// Shell-quoted form must be present.
	want := "--add-dir '/home/john doe/.cistern/skills'"
	if !strings.Contains(cmd, want) {
		t.Errorf("claudeCmd missing shell-quoted skillsDir\nwant substring: %s\ngot: %s", want, cmd)
	}
}

func TestBuildClaudeCmd_WithModel(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp", Model: "haiku"}
	cmd := s.buildClaudeCmd("/home/user/.cistern/skills")
	if !strings.Contains(cmd, "--model 'haiku'") {
		t.Errorf("claudeCmd missing shell-quoted --model flag: %s", cmd)
	}
}

func TestBuildClaudeCmd_WithoutModel(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp"}
	cmd := s.buildClaudeCmd("/home/user/.cistern/skills")
	if strings.Contains(cmd, "--model") {
		t.Errorf("claudeCmd should not contain --model when model is empty: %s", cmd)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/home/user/.cistern/skills", "'/home/user/.cistern/skills'"},
		{"/home/john doe/.cistern/skills", "'/home/john doe/.cistern/skills'"},
		{"it's a path", "'it'\\''s a path'"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.input)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildPrompt_WithIdentity_FileFound(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "CLAUDE.md"),
		[]byte("# Implementer\n\nYou implement things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{ID: "test", WorkDir: dir, Identity: "implementer"}
	prompt := s.buildPrompt()

	if !strings.Contains(prompt, "## Your Role") {
		t.Error("prompt missing '## Your Role' section when identity file is present")
	}
	if !strings.Contains(prompt, "You implement things.") {
		t.Error("prompt missing identity file content")
	}
	if !strings.Contains(prompt, baseCataractaePrompt) {
		t.Error("prompt missing constitutional base")
	}
}

func TestBuildPrompt_WithIdentity_FileMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no CLAUDE.md at cistern identity path

	s := &Session{ID: "test", WorkDir: dir, Identity: "implementer"}
	prompt := s.buildPrompt()

	// Fallback: prompt contains the actual missing path, not just any occurrence of "Read".
	if !strings.Contains(prompt, "cataractae/implementer/CLAUDE.md") {
		t.Error("prompt missing fallback path 'cataractae/implementer/CLAUDE.md' when identity file is missing")
	}
	if !strings.Contains(prompt, "implementer") {
		t.Error("prompt missing identity name in fallback")
	}
	if strings.Contains(prompt, "## Your Role") {
		t.Error("prompt should not contain '## Your Role' when identity file is missing")
	}
}

func TestResolveIdentityPath_CisternHome(t *testing.T) {
	dir := t.TempDir()
	cisternPath := filepath.Join(dir, ".cistern", "cataractae", "reviewer", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(cisternPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cisternPath, []byte("# Reviewer"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{Identity: "reviewer"}
	got := s.resolveIdentityPath()
	if got != cisternPath {
		t.Errorf("resolveIdentityPath = %q, want %q", got, cisternPath)
	}
}

func TestResolveIdentityPath_FallbackSandbox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no CLAUDE.md at cistern identity path

	s := &Session{Identity: "implementer"}
	got := s.resolveIdentityPath()
	want := "cataractae/implementer/CLAUDE.md"
	if got != want {
		t.Errorf("resolveIdentityPath = %q, want %q", got, want)
	}
}

func TestClaudePath_EnvOverride(t *testing.T) {
	t.Setenv("CLAUDE_PATH", "/usr/local/bin/my-claude")
	got := claudePath()
	if got != "/usr/local/bin/my-claude" {
		t.Errorf("claudePath() = %q, want %q", got, "/usr/local/bin/my-claude")
	}
}

func TestClaudePath_LookPath(t *testing.T) {
	t.Setenv("CLAUDE_PATH", "")
	// Place a fake "claude" executable on PATH so exec.LookPath finds it.
	dir := t.TempDir()
	fakeClaude := filepath.Join(dir, "claude")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	got := claudePath()
	if got != fakeClaude {
		t.Errorf("claudePath() = %q, want %q", got, fakeClaude)
	}
}

// TestClaudePresetBackwardCompat verifies that buildPresetCmd produces a correctly
// shell-quoted command string for the claude built-in preset. It was originally
// written as a parity-with-buildClaudeCmd gate (ci-sc2wl); after ci-qdc7q it
// checks the correct quoting behaviour instead, since command and args are now
// shell-quoted for safety.
func TestClaudePresetBackwardCompat(t *testing.T) {
	// Normalise resolveCommandFn to identity so tests do not depend on
	// claude being installed on the test machine.
	orig := resolveCommandFn
	resolveCommandFn = func(cmd string) string { return cmd }
	t.Cleanup(func() { resolveCommandFn = orig })

	var claudePreset provider.ProviderPreset
	for _, p := range provider.Builtins() {
		if p.Name == "claude" {
			claudePreset = p
			break
		}
	}
	if claudePreset.Name == "" {
		t.Fatal("claude preset not found in Builtins()")
	}

	skillsDir := "/home/user/.cistern/skills"

	t.Run("without model", func(t *testing.T) {
		s := &Session{ID: "test", WorkDir: "/tmp"}
		got, err := s.buildPresetCmd(claudePreset, skillsDir)
		if err != nil {
			t.Fatalf("buildPresetCmd: %v", err)
		}
		// Command and arg must be shell-quoted.
		if !strings.HasPrefix(got, "'claude' '--dangerously-skip-permissions'") {
			t.Errorf("buildPresetCmd: want shell-quoted command+arg prefix, got: %q", got)
		}
		if !strings.Contains(got, "--add-dir '"+skillsDir+"'") {
			t.Errorf("buildPresetCmd missing add-dir with quoted skillsDir, got: %q", got)
		}
		if strings.Contains(got, "--model") {
			t.Errorf("buildPresetCmd should not contain --model when model is empty, got: %q", got)
		}
	})

	t.Run("with model", func(t *testing.T) {
		s := &Session{ID: "test", WorkDir: "/tmp", Model: "haiku"}
		got, err := s.buildPresetCmd(claudePreset, skillsDir)
		if err != nil {
			t.Fatalf("buildPresetCmd: %v", err)
		}
		if !strings.HasPrefix(got, "'claude' '--dangerously-skip-permissions'") {
			t.Errorf("buildPresetCmd: want shell-quoted command+arg prefix, got: %q", got)
		}
		if !strings.Contains(got, "--model 'haiku'") {
			t.Errorf("buildPresetCmd missing shell-quoted model flag, got: %q", got)
		}
	})

	t.Run("skills dir with spaces", func(t *testing.T) {
		s := &Session{ID: "test", WorkDir: "/tmp"}
		dir := "/home/john doe/.cistern/skills"
		got, err := s.buildPresetCmd(claudePreset, dir)
		if err != nil {
			t.Fatalf("buildPresetCmd: %v", err)
		}
		if !strings.Contains(got, "--add-dir '/home/john doe/.cistern/skills'") {
			t.Errorf("buildPresetCmd missing shell-quoted skillsDir with spaces, got: %q", got)
		}
	})

	// This subtest verifies the LookPath contract: when resolveCommandFn returns
	// an absolute path, buildPresetCmd must shell-quote it so paths with spaces
	// are safe in /bin/sh -c.
	t.Run("LookPath resolution — resolved absolute path is shell-quoted", func(t *testing.T) {
		const resolvedPath = "/opt/test/claude"
		resolvedPreset := claudePreset
		resolvedPreset.Command = resolvedPath

		s := &Session{ID: "test", WorkDir: "/tmp"}
		got, err := s.buildPresetCmd(resolvedPreset, skillsDir)
		if err != nil {
			t.Fatalf("buildPresetCmd: %v", err)
		}
		want := "'" + resolvedPath + "'"
		if !strings.HasPrefix(got, want) {
			t.Errorf("buildPresetCmd: resolved path must be shell-quoted\nwant prefix: %s\ngot: %q", want, got)
		}
	})
}

// TestClaudeDefaultFallback verifies that an empty provider name resolves to the
// "claude" built-in preset and that buildPresetCmd produces a correctly
// shell-quoted command string.
func TestClaudeDefaultFallback(t *testing.T) {
	// Normalise resolveCommandFn so the test does not depend on claude being installed.
	orig := resolveCommandFn
	resolveCommandFn = func(cmd string) string { return cmd }
	t.Cleanup(func() { resolveCommandFn = orig })

	// Resolve preset: empty provider name must return the "claude" built-in.
	preset := provider.ResolvePreset("")
	if preset.Name != "claude" {
		t.Fatalf("ResolvePreset(\"\") = %q, want %q", preset.Name, "claude")
	}

	skillsDir := "/home/user/.cistern/skills"

	t.Run("without model", func(t *testing.T) {
		s := &Session{ID: "test", WorkDir: "/tmp"}
		got, err := s.buildPresetCmd(preset, skillsDir)
		if err != nil {
			t.Fatalf("buildPresetCmd error: %v", err)
		}
		if !strings.HasPrefix(got, "'claude' '--dangerously-skip-permissions'") {
			t.Errorf("default fallback: want shell-quoted command+arg prefix, got: %q", got)
		}
		if !strings.Contains(got, "--add-dir '"+skillsDir+"'") {
			t.Errorf("default fallback: missing add-dir with quoted skillsDir, got: %q", got)
		}
		if strings.Contains(got, "--model") {
			t.Errorf("default fallback: should not contain --model when model is empty, got: %q", got)
		}
	})

	t.Run("with model", func(t *testing.T) {
		s := &Session{ID: "test", WorkDir: "/tmp", Model: "haiku"}
		got, err := s.buildPresetCmd(preset, skillsDir)
		if err != nil {
			t.Fatalf("buildPresetCmd error: %v", err)
		}
		if !strings.HasPrefix(got, "'claude' '--dangerously-skip-permissions'") {
			t.Errorf("default fallback: want shell-quoted command+arg prefix, got: %q", got)
		}
		if !strings.Contains(got, "--model 'haiku'") {
			t.Errorf("default fallback: missing shell-quoted model flag, got: %q", got)
		}
	})
}

// buildTestBin compiles the Go package at importPath into a temp directory
// and returns the absolute path to the resulting binary.
func buildTestBin(t *testing.T, name, importPath string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	out, err := exec.Command("go", "build", "-o", bin, importPath).CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return bin
}

// TestFakeagent_SpawnOutcomeCycle exercises the full Session.Spawn →
// Session.isAlive → droplet outcome pipeline using the fakeagent binary.
//
// The test is skipped when tmux is unavailable (e.g. in minimal CI
// environments) so that 'go test ./...' never hard-fails on missing
// infrastructure.
func TestFakeagent_SpawnOutcomeCycle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available — skipping integration test")
	}

	// Build fakeagent and ct.
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	ctBin := buildTestBin(t, "ct", "github.com/MichielDean/cistern/cmd/ct")

	// Add both binaries to a temporary PATH so fakeagent can call 'ct'.
	binDir := filepath.Dir(ctBin)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	// Create an isolated cistern DB.
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	t.Setenv("CT_DB", dbPath)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	c, err := cistern.New(dbPath, "fa")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer c.Close()

	// Add a test droplet so fakeagent has an ID to pass.
	droplet, err := c.Add("testrepo", "fakeagent test", "desc", 1, 2)
	if err != nil {
		t.Fatalf("cistern.Add: %v", err)
	}

	// Write CONTEXT.md into the WorkDir with the droplet ID.
	workDir := t.TempDir()
	contextContent := fmt.Sprintf("# Context\n\n## Item: %s\n\n**Title:** fakeagent test\n", droplet.ID)
	if err := os.WriteFile(filepath.Join(workDir, "CONTEXT.md"), []byte(contextContent), 0o644); err != nil {
		t.Fatalf("write CONTEXT.md: %v", err)
	}

	// Point CLAUDE_PATH at the fakeagent binary.
	t.Setenv("CLAUDE_PATH", fakeagentBin)

	// Spawn the session.
	sessionID := "ci-t3xo9-fa-" + droplet.ID
	s := &Session{
		ID:      sessionID,
		WorkDir: workDir,
	}
	if err := s.Spawn(); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { s.kill() })

	// Wait for the session to die (fakeagent exits after calling ct droplet pass).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !s.isAlive() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s.isAlive() {
		t.Fatal("session still alive after 15s — fakeagent did not exit")
	}

	// Verify the outcome was recorded.
	got, err := c.Get(droplet.ID)
	if err != nil {
		t.Fatalf("cistern.Get: %v", err)
	}
	if got.Outcome != "pass" {
		t.Errorf("droplet outcome = %q, want %q", got.Outcome, "pass")
	}
}

// TestResolveIdentityPath_UsesPresetInstructionsFile verifies that resolveIdentityPath
// returns the preset's InstructionsFile rather than always CLAUDE.md.
func TestResolveIdentityPath_UsesPresetInstructionsFile(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create AGENTS.md (not CLAUDE.md) — codex-style preset.
	if err := os.WriteFile(filepath.Join(identityDir, "AGENTS.md"), []byte("# Implementer"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{
		Identity: "implementer",
		Preset:   provider.ProviderPreset{InstructionsFile: "AGENTS.md"},
	}
	got := s.resolveIdentityPath()
	want := filepath.Join(identityDir, "AGENTS.md")
	if got != want {
		t.Errorf("resolveIdentityPath = %q, want %q", got, want)
	}
}

// TestResolveIdentityPath_FallbackSandbox_WithPreset verifies that when the cistern
// path does not exist, the sandbox-relative path uses the preset's InstructionsFile.
func TestResolveIdentityPath_FallbackSandbox_WithPreset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no identity dir at cistern path

	s := &Session{
		Identity: "reviewer",
		Preset:   provider.ProviderPreset{InstructionsFile: "GEMINI.md"},
	}
	got := s.resolveIdentityPath()
	want := "cataractae/reviewer/GEMINI.md"
	if got != want {
		t.Errorf("resolveIdentityPath = %q, want %q", got, want)
	}
}

// TestBuildContextPreamble_ReadsInstructionsFile verifies that buildContextPreamble
// returns the content of the preset's InstructionsFile when it exists.
func TestBuildContextPreamble_ReadsInstructionsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Codex Role\n\nDo work."), 0o644); err != nil {
		t.Fatal(err)
	}
	preset := provider.ProviderPreset{InstructionsFile: "AGENTS.md"}
	got := buildContextPreamble(dir, preset)
	if got != "# Codex Role\n\nDo work." {
		t.Errorf("buildContextPreamble = %q, want %q", got, "# Codex Role\n\nDo work.")
	}
}

// TestBuildContextPreamble_FallsBackToSourceFiles verifies that when InstructionsFile
// is missing, PERSONA.md + INSTRUCTIONS.md are concatenated as fallback.
func TestBuildContextPreamble_FallsBackToSourceFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PERSONA.md"), []byte("# Role: Coder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "INSTRUCTIONS.md"), []byte("Write tests."), 0o644); err != nil {
		t.Fatal(err)
	}
	// No AGENTS.md — forces fallback.
	preset := provider.ProviderPreset{InstructionsFile: "AGENTS.md"}
	got := buildContextPreamble(dir, preset)
	if !strings.Contains(got, "# Role: Coder") {
		t.Error("fallback preamble missing PERSONA.md content")
	}
	if !strings.Contains(got, "Write tests.") {
		t.Error("fallback preamble missing INSTRUCTIONS.md content")
	}
}

// TestBuildContextPreamble_EmptyWhenAllMissing verifies that buildContextPreamble
// returns empty string when neither InstructionsFile nor source files exist.
func TestBuildContextPreamble_EmptyWhenAllMissing(t *testing.T) {
	dir := t.TempDir() // no files
	preset := provider.ProviderPreset{InstructionsFile: "AGENTS.md"}
	got := buildContextPreamble(dir, preset)
	if got != "" {
		t.Errorf("buildContextPreamble = %q, want empty string when all files missing", got)
	}
}

// TestBuildContextPreamble_DefaultsToClaude verifies that empty InstructionsFile
// defaults to reading CLAUDE.md.
func TestBuildContextPreamble_DefaultsToClaude(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Claude role content"), 0o644); err != nil {
		t.Fatal(err)
	}
	preset := provider.ProviderPreset{} // InstructionsFile is empty
	got := buildContextPreamble(dir, preset)
	if got != "Claude role content" {
		t.Errorf("buildContextPreamble = %q, want %q", got, "Claude role content")
	}
}

// TestBuildPrompt_NonAddDirProvider_InjectsContextPreamble verifies that for a preset
// without SupportsAddDir, buildPrompt injects the InstructionsFile content via
// buildContextPreamble.
func TestBuildPrompt_NonAddDirProvider_InjectsContextPreamble(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "AGENTS.md"),
		[]byte("# Implementer (codex)\n\nYou write code."), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{
		ID:       "test",
		WorkDir:  dir,
		Identity: "implementer",
		Preset: provider.ProviderPreset{
			Name:             "codex",
			InstructionsFile: "AGENTS.md",
			SupportsAddDir:   false,
		},
	}
	prompt := s.buildPrompt()

	if !strings.Contains(prompt, "## Your Role") {
		t.Error("prompt missing '## Your Role' section")
	}
	if !strings.Contains(prompt, "You write code.") {
		t.Error("prompt missing AGENTS.md content")
	}
	if !strings.Contains(prompt, baseCataractaePrompt) {
		t.Error("prompt missing constitutional base")
	}
}

// TestBuildPrompt_NonAddDirProvider_InjectsSkills verifies that for a preset without
// SupportsAddDir, skill content is injected into the prompt when Skills is set.
func TestBuildPrompt_NonAddDirProvider_InjectsSkills(t *testing.T) {
	dir := t.TempDir()
	// Create skill SKILL.md.
	skillDir := filepath.Join(dir, ".cistern", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# My Skill\n\nDo the skill thing."), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{
		ID:      "test",
		WorkDir: dir,
		Preset: provider.ProviderPreset{
			Name:           "codex",
			SupportsAddDir: false,
		},
		Skills: []string{"my-skill"},
	}
	prompt := s.buildPrompt()

	if !strings.Contains(prompt, "my-skill") {
		t.Error("prompt missing skill name")
	}
	if !strings.Contains(prompt, "Do the skill thing.") {
		t.Error("prompt missing skill content")
	}
}

// TestBuildPrompt_AddDirProvider_SkillsNotInjectedInPrompt verifies that for
// providers with SupportsAddDir=true, skills are NOT injected in the prompt
// (they are available via --add-dir instead).
func TestBuildPrompt_AddDirProvider_SkillsNotInjectedInPrompt(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "CLAUDE.md"),
		[]byte("# Implementer\n\nYou implement."), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, ".cistern", "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# My Skill\n\nSkill content."), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{
		ID:       "test",
		WorkDir:  dir,
		Identity: "implementer",
		Preset: provider.ProviderPreset{
			Name:             "claude",
			InstructionsFile: "CLAUDE.md",
			SupportsAddDir:   true,
		},
		Skills: []string{"my-skill"},
	}
	prompt := s.buildPrompt()

	// Role content is injected (via SupportsAddDir=true path).
	if !strings.Contains(prompt, "## Your Role") {
		t.Error("prompt missing '## Your Role'")
	}
	// Skill content must NOT be in the prompt for AddDir providers.
	if strings.Contains(prompt, "Skill content.") {
		t.Error("prompt must not contain injected skill content for AddDir providers — skills available via --add-dir")
	}
}

// TestIsAgentAlive_ProcessNameMatches_ReturnsTrue verifies that isAgentAlive
// returns true when the pane's current command matches one of the preset's
// ProcessNames.
func TestIsAgentAlive_ProcessNameMatches_ReturnsTrue(t *testing.T) {
	orig := tmuxDisplayMessage
	tmuxDisplayMessage = func(id string) (string, error) { return "claude", nil }
	t.Cleanup(func() { tmuxDisplayMessage = orig })

	s := &Session{
		ID:     "test-session",
		Preset: provider.ProviderPreset{ProcessNames: []string{"claude", "node"}},
	}
	if !s.isAgentAlive() {
		t.Error("isAgentAlive() = false, want true when pane_current_command is in ProcessNames")
	}
}

// TestIsAgentAlive_ProcessNameNotMatched_ReturnsFalse verifies that isAgentAlive
// returns false when the pane's current command is not in ProcessNames — this is
// a zombie session (tmux alive, agent dead).
func TestIsAgentAlive_ProcessNameNotMatched_ReturnsFalse(t *testing.T) {
	orig := tmuxDisplayMessage
	tmuxDisplayMessage = func(id string) (string, error) { return "bash", nil }
	t.Cleanup(func() { tmuxDisplayMessage = orig })

	s := &Session{
		ID:     "test-session",
		Preset: provider.ProviderPreset{ProcessNames: []string{"claude", "node"}},
	}
	if s.isAgentAlive() {
		t.Error("isAgentAlive() = true, want false when pane_current_command is not in ProcessNames")
	}
}

// TestIsAgentAlive_EmptyProcessNames_ReturnsTrue verifies that isAgentAlive
// returns true when no ProcessNames are configured — the preset has no way to
// detect zombie sessions so it conservatively assumes the agent is alive.
func TestIsAgentAlive_EmptyProcessNames_ReturnsTrue(t *testing.T) {
	orig := tmuxDisplayMessage
	tmuxDisplayMessage = func(id string) (string, error) { return "bash", nil }
	t.Cleanup(func() { tmuxDisplayMessage = orig })

	s := &Session{
		ID:     "test-session",
		Preset: provider.ProviderPreset{},
	}
	if !s.isAgentAlive() {
		t.Error("isAgentAlive() = false, want true when ProcessNames is empty (no detection configured)")
	}
}

// TestIsAgentAlive_TmuxError_ReturnsFalse verifies that isAgentAlive returns
// false when the tmux display-message call fails — treat an unqueryable session
// as a dead agent.
func TestIsAgentAlive_TmuxError_ReturnsFalse(t *testing.T) {
	orig := tmuxDisplayMessage
	tmuxDisplayMessage = func(id string) (string, error) {
		return "", errors.New("tmux: can't find session: test-session")
	}
	t.Cleanup(func() { tmuxDisplayMessage = orig })

	s := &Session{
		ID:     "test-session",
		Preset: provider.ProviderPreset{ProcessNames: []string{"claude"}},
	}
	if s.isAgentAlive() {
		t.Error("isAgentAlive() = true, want false when tmux command errors")
	}
}

// TestBuildPresetCmd_ModelWithSpaces_IsShellQuoted verifies that a model value
// containing spaces is shell-quoted before being interpolated into the tmux
// command string. An unquoted model with spaces would split in /bin/sh -c.
func TestBuildPresetCmd_ModelWithSpaces_IsShellQuoted(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp", Model: "claude opus 4.6"}
	preset := provider.ProviderPreset{
		Name:      "myagent",
		Command:   "myagent",
		ModelFlag: "--model",
	}
	cmd, err := s.buildPresetCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildPresetCmd: %v", err)
	}
	// Unquoted form must not appear — it would split at spaces in the shell.
	if strings.Contains(cmd, "--model claude opus") {
		t.Errorf("buildPresetCmd contains unquoted model with space — will break shell: %s", cmd)
	}
	// Shell-quoted form must be present.
	want := "--model 'claude opus 4.6'"
	if !strings.Contains(cmd, want) {
		t.Errorf("buildPresetCmd missing shell-quoted model\nwant substring: %s\ngot: %s", want, cmd)
	}
}

// TestBuildContinueCmd_ModelWithSpaces_IsShellQuoted verifies that a model value
// containing spaces is shell-quoted in buildContinueCmd, consistent with buildPresetCmd.
func TestBuildContinueCmd_ModelWithSpaces_IsShellQuoted(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp", Model: "claude opus 4.6"}
	preset := provider.ProviderPreset{
		Name:         "myagent",
		Command:      "myagent",
		ModelFlag:    "--model",
		ContinueFlag: "--continue",
	}
	cmd, err := s.buildContinueCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildContinueCmd: %v", err)
	}
	// Unquoted form must not appear.
	if strings.Contains(cmd, "--model claude opus") {
		t.Errorf("buildContinueCmd contains unquoted model with space — will break shell: %s", cmd)
	}
	// Shell-quoted form must be present.
	want := "--model 'claude opus 4.6'"
	if !strings.Contains(cmd, want) {
		t.Errorf("buildContinueCmd missing shell-quoted model\nwant substring: %s\ngot: %s", want, cmd)
	}
}

// TestBuildPresetCmd_EmptyCommand_ReturnsError verifies that buildPresetCmd
// returns a descriptive error when the preset has no command configured.
// A misconfigured provider with Name set but Command empty must not silently
// produce a broken tmux command.
func TestBuildPresetCmd_EmptyCommand_ReturnsError(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{Name: "custom"} // Command is deliberately empty
	_, err := s.buildPresetCmd(preset, "/skills")
	if err == nil {
		t.Fatal("expected error for preset with empty Command, got nil")
	}
	if !strings.Contains(err.Error(), "custom") {
		t.Errorf("error %q should mention the preset name", err.Error())
	}
	if !strings.Contains(err.Error(), "no command configured") {
		t.Errorf("error %q should mention 'no command configured'", err.Error())
	}
}

// TestBuildPresetCmd_PromptFlag_AppendedWhenNonEmpty verifies that buildPresetCmd
// uses preset.PromptFlag to deliver the prompt when it is set.
func TestBuildPresetCmd_PromptFlag_AppendedWhenNonEmpty(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{
		Name:       "myagent",
		Command:    "myagent",
		PromptFlag: "--prompt",
	}
	cmd, err := s.buildPresetCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildPresetCmd: %v", err)
	}
	if !strings.Contains(cmd, "--prompt") {
		t.Errorf("buildPresetCmd output missing PromptFlag: %s", cmd)
	}
}

// TestBuildPresetCmd_PromptFlag_OmittedWhenEmpty verifies that buildPresetCmd
// does not append any prompt flag when PromptFlag is empty. Presets for CLIs
// that do not accept -p (e.g. opencode) must have PromptFlag="" to avoid
// spawn failures from unrecognized flags.
func TestBuildPresetCmd_PromptFlag_OmittedWhenEmpty(t *testing.T) {
	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{
		Name:    "opencode",
		Command: "opencode",
		// PromptFlag deliberately empty — prompt delivered via instructions file
	}
	cmd, err := s.buildPresetCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildPresetCmd: %v", err)
	}
	if strings.Contains(cmd, " -p ") || strings.Contains(cmd, " --prompt") {
		t.Errorf("buildPresetCmd with empty PromptFlag should not contain a prompt flag: %s", cmd)
	}
	if !strings.HasPrefix(cmd, "'opencode'") {
		t.Errorf("buildPresetCmd output = %q, want prefix %q", cmd, "'opencode'")
	}
}

// TestCollectEnvArgs_GHToken_AlwaysForwarded_PresetPath verifies that GH_TOKEN
// is included in env args when using the preset path. This is a regression test
// for the ci-sc2wl refactor: the legacy path forwarded GH_TOKEN but the preset
// path only iterated EnvPassthrough (which did not include GH_TOKEN for claude).
func TestCollectEnvArgs_GHToken_AlwaysForwarded_PresetPath(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghtoken-preset-123")
	t.Setenv("ANTHROPIC_API_KEY", "") // isolate to GH_TOKEN check

	s := &Session{
		ID:     "test",
		Preset: provider.ProviderPreset{Name: "claude", Command: "claude"},
	}
	args := s.collectEnvArgs()
	if !containsEnvPair(args, "GH_TOKEN", "ghtoken-preset-123") {
		t.Errorf("collectEnvArgs (preset path) missing GH_TOKEN; args: %v", args)
	}
}

// TestCollectEnvArgs_GHToken_AlwaysForwarded_LegacyPath verifies that GH_TOKEN
// is included in env args when using the legacy (no-preset) path.
func TestCollectEnvArgs_GHToken_AlwaysForwarded_LegacyPath(t *testing.T) {
	t.Setenv("GH_TOKEN", "ghtoken-legacy-456")

	s := &Session{ID: "test"} // Preset.Name is empty — legacy path
	args := s.collectEnvArgs()
	if !containsEnvPair(args, "GH_TOKEN", "ghtoken-legacy-456") {
		t.Errorf("collectEnvArgs (legacy path) missing GH_TOKEN; args: %v", args)
	}
}

// TestCollectEnvArgs_GHToken_AbsentWhenNotSet verifies that GH_TOKEN is not
// included in env args when it is unset in the environment.
func TestCollectEnvArgs_GHToken_AbsentWhenNotSet(t *testing.T) {
	t.Setenv("GH_TOKEN", "")

	s := &Session{ID: "test", Preset: provider.ProviderPreset{Name: "claude"}}
	args := s.collectEnvArgs()
	for _, a := range args {
		if strings.Contains(a, "GH_TOKEN") {
			t.Errorf("collectEnvArgs contains GH_TOKEN when unset; args: %v", args)
		}
	}
}

// containsEnvPair checks whether args contains "-e" followed by "key=val".
func containsEnvPair(args []string, key, val string) bool {
	target := key + "=" + val
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-e" && args[i+1] == target {
			return true
		}
	}
	return false
}

// TestIsAgentAlive_PassesSessionIDToDisplayMessage verifies that isAgentAlive
// forwards the session ID to tmuxDisplayMessage.
func TestIsAgentAlive_PassesSessionIDToDisplayMessage(t *testing.T) {
	var capturedID string
	orig := tmuxDisplayMessage
	tmuxDisplayMessage = func(id string) (string, error) {
		capturedID = id
		return "claude", nil
	}
	t.Cleanup(func() { tmuxDisplayMessage = orig })

	s := &Session{
		ID:     "myrepo-alice",
		Preset: provider.ProviderPreset{ProcessNames: []string{"claude"}},
	}
	s.isAgentAlive()
	if capturedID != "myrepo-alice" {
		t.Errorf("tmuxDisplayMessage called with id = %q, want %q", capturedID, "myrepo-alice")
	}
}

// TestResolveIdentityDir_CisternDirWithInstrFile verifies that when the cistern
// directory exists and contains the instrFile, resolveIdentityDir returns the cistern path.
func TestResolveIdentityDir_CisternDirWithInstrFile(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "CLAUDE.md"), []byte("# Implementer"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	s := &Session{Identity: "implementer"}
	got := s.resolveIdentityDir()
	if got != identityDir {
		t.Errorf("resolveIdentityDir = %q, want %q", got, identityDir)
	}
}

// TestResolveIdentityDir_CisternDirWithoutInstrFile verifies that when the cistern
// directory exists but the instrFile is absent, resolveIdentityDir still returns the
// cistern path — directory existence, not file presence, is the resolution condition.
func TestResolveIdentityDir_CisternDirWithoutInstrFile(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Intentionally no instrFile in this directory.
	t.Setenv("HOME", dir)

	s := &Session{Identity: "implementer"}
	got := s.resolveIdentityDir()
	if got != identityDir {
		t.Errorf("resolveIdentityDir = %q, want %q", got, identityDir)
	}
}

// TestResolveIdentityDir_FallbackSandbox verifies that when the cistern directory
// does not exist, resolveIdentityDir returns the sandbox-relative path.
func TestResolveIdentityDir_FallbackSandbox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no cistern identity dir

	s := &Session{Identity: "implementer"}
	got := s.resolveIdentityDir()
	want := filepath.Join("cataractae", "implementer")
	if got != want {
		t.Errorf("resolveIdentityDir = %q, want %q", got, want)
	}
}

// --- ensureClaudeOAuthFresh tests ---

// writeSessionCredentials writes a minimal credentials file for session tests.
func writeSessionCredentials(t *testing.T, home, accessToken, refreshToken string, expiresAtMs int64) {
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

func TestEnsureClaudeOAuthFresh_FreshToken_DoesNotRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Token fresh for 1 hour — well outside the 5-minute window.
	writeSessionCredentials(t, home, "tok-fresh", "tok-refresh", time.Now().Add(time.Hour).UnixMilli())

	refreshCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalled = true
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok-new","expires_in":3600}`)
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	origHTTP := oauthHTTPDo
	t.Cleanup(func() { oauthTokenURL = origURL; oauthHTTPDo = origHTTP })
	oauthTokenURL = srv.URL
	oauthHTTPDo = srv.Client().Do

	if err := ensureClaudeOAuthFresh(home); err != nil {
		t.Fatalf("ensureClaudeOAuthFresh: %v", err)
	}
	if refreshCalled {
		t.Error("refresh endpoint should not be called for a fresh token")
	}
}

func TestEnsureClaudeOAuthFresh_ExpiredToken_RefreshesSuccessfully(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	expiredAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeSessionCredentials(t, home, "tok-old", "tok-refresh", expiredAt)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok-new","expires_in":3600}`)
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	origHTTP := oauthHTTPDo
	t.Cleanup(func() { oauthTokenURL = origURL; oauthHTTPDo = origHTTP })
	oauthTokenURL = srv.URL
	oauthHTTPDo = srv.Client().Do

	if err := ensureClaudeOAuthFresh(home); err != nil {
		t.Fatalf("ensureClaudeOAuthFresh: %v", err)
	}

	// Credentials file should have the new token.
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if !strings.Contains(string(data), "tok-new") {
		t.Errorf("credentials not updated with new token: %s", data)
	}

	// Process environment should have the new token.
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "tok-new" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want tok-new", got)
	}
}

func TestEnsureClaudeOAuthFresh_NearExpiry_RefreshesSuccessfully(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Expiring in 3 minutes — within the 5-minute window.
	nearExpiryAt := time.Now().Add(3 * time.Minute).UnixMilli()
	writeSessionCredentials(t, home, "tok-old", "tok-refresh", nearExpiryAt)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok-new","expires_in":3600}`)
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	origHTTP := oauthHTTPDo
	t.Cleanup(func() { oauthTokenURL = origURL; oauthHTTPDo = origHTTP })
	oauthTokenURL = srv.URL
	oauthHTTPDo = srv.Client().Do

	if err := ensureClaudeOAuthFresh(home); err != nil {
		t.Fatalf("ensureClaudeOAuthFresh: %v", err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "tok-new" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want tok-new", got)
	}
}

func TestEnsureClaudeOAuthFresh_NoCredentials_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	// No credentials file — should return nil (skip silently).
	if err := ensureClaudeOAuthFresh(home); err != nil {
		t.Errorf("expected nil when credentials absent, got %v", err)
	}
}

func TestEnsureClaudeOAuthFresh_NoRefreshToken_SkipsSilently(t *testing.T) {
	home := t.TempDir()
	// Expired token but no refresh token — should skip silently.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	expiredAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	content := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok","expiresAt":%d}}`, expiredAt)
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ensureClaudeOAuthFresh(home); err != nil {
		t.Errorf("expected nil when refresh token absent, got %v", err)
	}
}

func TestEnsureClaudeOAuthFresh_RefreshFails_ReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	expiredAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeSessionCredentials(t, home, "tok-old", "tok-refresh", expiredAt)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	origHTTP := oauthHTTPDo
	t.Cleanup(func() { oauthTokenURL = origURL; oauthHTTPDo = origHTTP })
	oauthTokenURL = srv.URL
	oauthHTTPDo = srv.Client().Do

	err := ensureClaudeOAuthFresh(home)
	if err == nil {
		t.Fatal("expected error when refresh fails")
	}
	if !strings.Contains(err.Error(), "re-authenticate") {
		t.Errorf("error message should mention re-authentication, got: %v", err)
	}
}

func TestEnsureClaudeOAuthFresh_ConcurrentCalls_NoRaceAndSingleRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	expiredAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	writeSessionCredentials(t, home, "tok-old", "tok-refresh", expiredAt)

	var refreshCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"tok-new","expires_in":3600}`)
	}))
	defer srv.Close()

	origURL := oauthTokenURL
	origHTTP := oauthHTTPDo
	t.Cleanup(func() { oauthTokenURL = origURL; oauthHTTPDo = origHTTP })
	oauthTokenURL = srv.URL
	oauthHTTPDo = srv.Client().Do

	const goroutines = 5
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = ensureClaudeOAuthFresh(home)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	// The mutex ensures at most one refresh per cycle. After the first goroutine
	// refreshes the token, subsequent goroutines see a fresh token and skip.
	// Verify the endpoint was not hammered — it must be called at least once
	// (the initial refresh) but not more than goroutines times.
	count := refreshCount.Load()
	if count == 0 {
		t.Error("refresh endpoint was never called — token should have been refreshed")
	}
	if count > int32(goroutines) {
		t.Errorf("refresh endpoint called %d times, want at most %d", count, goroutines)
	}
}

// TestBuildPresetCmd_CommandWithSpaces_IsShellQuoted verifies that a command
// path containing spaces is shell-quoted before being interpolated into the
// tmux command string. An unquoted path with spaces would split in /bin/sh -c.
func TestBuildPresetCmd_CommandWithSpaces_IsShellQuoted(t *testing.T) {
	orig := resolveCommandFn
	resolveCommandFn = func(cmd string) string { return cmd }
	t.Cleanup(func() { resolveCommandFn = orig })

	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{
		Name:    "myagent",
		Command: "/home/john doe/bin/myagent",
	}
	cmd, err := s.buildPresetCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildPresetCmd: %v", err)
	}
	// Unquoted form must not appear — it would split at the space.
	if strings.Contains(cmd, "/home/john doe/bin/myagent") && !strings.Contains(cmd, "'/home/john doe/bin/myagent'") {
		t.Errorf("buildPresetCmd contains unquoted command path with space — will break shell: %s", cmd)
	}
	// Shell-quoted form must be present.
	want := "'/home/john doe/bin/myagent'"
	if !strings.HasPrefix(cmd, want) {
		t.Errorf("buildPresetCmd should start with shell-quoted command\nwant prefix: %s\ngot: %s", want, cmd)
	}
}

// TestBuildPresetCmd_ArgsWithSpaces_AreShellQuoted verifies that Args elements
// containing spaces or shell metacharacters are shell-quoted. User-supplied
// preset overrides via LoadUserPresets can contain arbitrary strings.
func TestBuildPresetCmd_ArgsWithSpaces_AreShellQuoted(t *testing.T) {
	orig := resolveCommandFn
	resolveCommandFn = func(cmd string) string { return cmd }
	t.Cleanup(func() { resolveCommandFn = orig })

	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{
		Name:    "myagent",
		Command: "myagent",
		Args:    []string{"--flag with spaces", "--another$arg"},
	}
	cmd, err := s.buildPresetCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildPresetCmd: %v", err)
	}
	// Shell-quoted forms must be present.
	for _, want := range []string{"'--flag with spaces'", "'--another$arg'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildPresetCmd missing shell-quoted arg\nwant substring: %s\ngot: %s", want, cmd)
		}
	}
	// Unquoted dollar sign must not appear bare (would be shell-expanded).
	if strings.Contains(cmd, "--another$arg") && !strings.Contains(cmd, "'--another$arg'") {
		t.Errorf("buildPresetCmd contains bare dollar sign in arg — shell-expansion risk: %s", cmd)
	}
}

// TestBuildContinueCmd_CommandWithSpaces_IsShellQuoted verifies that a command
// path containing spaces is shell-quoted in buildContinueCmd, consistent with
// buildPresetCmd.
func TestBuildContinueCmd_CommandWithSpaces_IsShellQuoted(t *testing.T) {
	orig := resolveCommandFn
	resolveCommandFn = func(cmd string) string { return cmd }
	t.Cleanup(func() { resolveCommandFn = orig })

	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{
		Name:         "myagent",
		Command:      "/home/john doe/bin/myagent",
		ContinueFlag: "--continue",
	}
	cmd, err := s.buildContinueCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildContinueCmd: %v", err)
	}
	want := "'/home/john doe/bin/myagent'"
	if !strings.HasPrefix(cmd, want) {
		t.Errorf("buildContinueCmd should start with shell-quoted command\nwant prefix: %s\ngot: %s", want, cmd)
	}
}

// TestBuildContinueCmd_ArgsWithSpaces_AreShellQuoted verifies that Args elements
// containing spaces are shell-quoted in buildContinueCmd.
func TestBuildContinueCmd_ArgsWithSpaces_AreShellQuoted(t *testing.T) {
	orig := resolveCommandFn
	resolveCommandFn = func(cmd string) string { return cmd }
	t.Cleanup(func() { resolveCommandFn = orig })

	s := &Session{ID: "test", WorkDir: "/tmp"}
	preset := provider.ProviderPreset{
		Name:         "myagent",
		Command:      "myagent",
		Args:         []string{"--flag with spaces"},
		ContinueFlag: "--continue",
	}
	cmd, err := s.buildContinueCmd(preset, "/skills")
	if err != nil {
		t.Fatalf("buildContinueCmd: %v", err)
	}
	want := "'--flag with spaces'"
	if !strings.Contains(cmd, want) {
		t.Errorf("buildContinueCmd missing shell-quoted arg\nwant substring: %s\ngot: %s", want, cmd)
	}
}

// --- priorSessionCount tests ---

// TestPriorSessionCount_ReturnsZero_WhenNoDirExists verifies that priorSessionCount
// returns 0 when the Claude projects directory does not exist.
func TestPriorSessionCount_ReturnsZero_WhenNoDirExists(t *testing.T) {
	dir := t.TempDir()
	// No .claude/projects directory created.
	if got := priorSessionCount(dir, "/some/workdir"); got != 0 {
		t.Errorf("priorSessionCount = %d, want 0 when dir absent", got)
	}
}

// TestPriorSessionCount_ReturnsCount_WhenFilesExist verifies that priorSessionCount
// returns the number of entries in the Claude projects directory.
func TestPriorSessionCount_ReturnsCount_WhenFilesExist(t *testing.T) {
	home := t.TempDir()
	workDir := "/my/project/dir"
	escaped := strings.ReplaceAll(workDir, "/", "-")
	projectDir := filepath.Join(home, ".claude", "projects", escaped)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create 3 session files.
	for i := range 3 {
		if err := os.WriteFile(filepath.Join(projectDir, fmt.Sprintf("session-%d.json", i)), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := priorSessionCount(home, workDir); got != 3 {
		t.Errorf("priorSessionCount = %d, want 3", got)
	}
}

// --- spawn logging tests ---

// TestSpawn_LogsFreshSession_WhenNoTmux verifies that spawn emits a structured
// slog entry with session, context_type=fresh, and model fields. The test uses
// a fake tmux that always succeeds so the log is emitted without real tmux.
func TestSpawn_LogsFreshSession_WhenNoTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "echo") // fake agent: 'echo' exits quickly

	workDir := t.TempDir()
	s := &Session{
		ID:      "test-fresh-session",
		WorkDir: workDir,
		Model:   "haiku",
	}
	// Spawn may fail (fake agent) — we only care about log output.
	_ = s.spawn()
	defer s.kill()

	out := buf.String()
	if !strings.Contains(out, "session=test-fresh-session") {
		t.Errorf("log missing session field; got: %s", out)
	}
	if !strings.Contains(out, "context_type=fresh") {
		t.Errorf("log missing context_type=fresh; got: %s", out)
	}
	if !strings.Contains(out, "model=haiku") {
		t.Errorf("log missing model=haiku; got: %s", out)
	}
}

// TestSpawn_LogsResumeContext_WhenPriorSessionExists verifies that spawn emits a
// slog entry with context_type=resume and prior_session_count when a prior session
// exists for the working directory.
func TestSpawn_LogsResumeContext_WhenPriorSessionExists(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "echo") // fake agent

	workDir := t.TempDir()

	// Create a prior session so priorSessionCount > 0.
	escaped := strings.ReplaceAll(workDir, "/", "-")
	projectDir := filepath.Join(home, ".claude", "projects", escaped)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{
		ID:      "test-resume-session",
		WorkDir: workDir,
		Preset: provider.ProviderPreset{
			Name:         "claude",
			Command:      "echo",
			ContinueFlag: "--continue",
		},
	}
	_ = s.spawn()
	defer s.kill()

	out := buf.String()
	if !strings.Contains(out, "context_type=resume") {
		t.Errorf("log missing context_type=resume; got: %s", out)
	}
	if !strings.Contains(out, "prior_session_count=1") {
		t.Errorf("log missing prior_session_count=1; got: %s", out)
	}
	if !strings.Contains(out, "project_dir=") {
		t.Errorf("log missing project_dir field; got: %s", out)
	}
}

// TestSpawn_LogsQuickExit_WhenSessionDiesImmediately verifies that the quick-exit
// goroutine emits a Warn-level log when the session dies within the quick-exit window.
// This test uses a very short window so it does not have to wait 30 seconds.
func TestSpawn_LogsQuickExit_WhenSessionDiesImmediately(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true") // 'true' exits 0 immediately

	workDir := t.TempDir()
	s := &Session{
		ID:      "quick-exit-test",
		WorkDir: workDir,
	}

	// Override quick-exit window for this test.
	orig := quickExitWindow
	quickExitWindow = 200 * time.Millisecond
	t.Cleanup(func() { quickExitWindow = orig })

	if err := s.spawn(); err != nil {
		t.Skip("spawn failed (tmux environment issue):", err)
	}
	defer s.kill()

	// Wait longer than the quick-exit window for the goroutine to fire.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "exited quickly") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	out := buf.String()
	if !strings.Contains(out, "exited quickly") {
		t.Errorf("expected quick-exit warning in log; got: %s", out)
	}
	if !strings.Contains(out, "session=quick-exit-test") {
		t.Errorf("quick-exit log missing session field; got: %s", out)
	}
}

// TestSpawn_QuickExit_CancelledByKill verifies that calling kill() before the
// quick-exit window expires cancels the goroutine so no spurious warning is logged
// when the session is intentionally stopped.
func TestSpawn_QuickExit_CancelledByKill(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true") // exits immediately

	workDir := t.TempDir()
	s := &Session{
		ID:      "kill-cancel-test",
		WorkDir: workDir,
	}

	// Use a window long enough that we can call kill() before it fires.
	orig := quickExitWindow
	quickExitWindow = 500 * time.Millisecond
	t.Cleanup(func() { quickExitWindow = orig })

	if err := s.spawn(); err != nil {
		t.Skip("spawn failed (tmux environment issue):", err)
	}

	// Cancel the goroutine by killing the session before the window expires.
	s.kill()

	// Wait longer than the window to give the goroutine time to fire if not cancelled.
	time.Sleep(800 * time.Millisecond)

	out := buf.String()
	if strings.Contains(out, "exited quickly") {
		t.Errorf("unexpected quick-exit warning after intentional kill; got: %s", out)
	}
}

// --- isTmuxServerDeadError tests ---

// TestIsTmuxServerDeadError verifies that the dead-server detector matches the
// known tmux error patterns and ignores unrelated failures.
func TestIsTmuxServerDeadError(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{"no server running on socket path", "no server running on /tmp/tmux-1000/default", true},
		{"no server running uppercase", "No server running on /tmp/tmux-1000/default", true},
		{"failed to connect to server lowercase", "failed to connect to server", true},
		{"failed to connect to server uppercase", "Failed to connect to server", true},
		{"error connecting to socket", "error connecting to /tmp/tmux-1000/default", true},
		{"error connecting with reason", "Error connecting to /tmp/tmux-500/default (Connection refused)", true},
		{"unrelated error", "invalid option: -Z", false},
		{"permission denied", "open terminal failed: not a terminal", false},
		{"empty output", "", false},
		{"partial substring must not match", "server running fine", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isTmuxServerDeadError([]byte(tc.output))
			if got != tc.want {
				t.Errorf("isTmuxServerDeadError(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}

// --- dead tmux server recovery tests ---

// TestSpawn_TmuxServerDead_RecoverySucceeds verifies that when execTmuxNewSession
// fails with a dead-server error on the first attempt, spawn() kills the stale
// server, retries, and logs the recovery on success.
func TestSpawn_TmuxServerDead_RecoverySucceeds(t *testing.T) {
	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true") // 'true' binary exists; won't actually be run

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	callCount := 0
	killCalled := false
	execTmuxNewSession = func(_ []string) ([]byte, error) {
		callCount++
		if callCount <= 2 {
			// First call (outside mutex) and double-check (inside mutex) both see
			// a dead server. Third call (after kill) succeeds.
			return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
		}
		return nil, nil // retry after kill succeeds
	}
	execTmuxKillServer = func() { killCalled = true }

	// Extend quickExitWindow so the goroutine does not fire during this test.
	orig := quickExitWindow
	quickExitWindow = 10 * time.Second
	t.Cleanup(func() { quickExitWindow = orig })

	workDir := t.TempDir()
	s := &Session{ID: "tmux-recovery-ok", WorkDir: workDir}

	err := s.spawn()
	s.kill() // cancel the quick-exit goroutine before it fires

	if err != nil {
		t.Fatalf("spawn returned unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("execTmuxNewSession called %d times, want 3 (initial + double-check + retry-after-kill)", callCount)
	}
	if !killCalled {
		t.Error("execTmuxKillServer was not called during recovery")
	}
	out := buf.String()
	if !strings.Contains(out, "dead tmux server detected") {
		t.Errorf("expected dead-server detection log; got: %s", out)
	}
	if !strings.Contains(out, "recovered from dead tmux server") {
		t.Errorf("expected recovery success log; got: %s", out)
	}
}

// TestSpawn_TmuxServerDead_RecoveryFails verifies that when both spawn attempts
// fail with a dead-server error, spawn() returns an error with a clear reason and
// logs an ERROR distinguishing this from an auth failure.
func TestSpawn_TmuxServerDead_RecoveryFails(t *testing.T) {
	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	callCount := 0
	execTmuxNewSession = func(_ []string) ([]byte, error) {
		callCount++
		return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
	}
	execTmuxKillServer = func() {} // no-op

	workDir := t.TempDir()
	s := &Session{ID: "tmux-recovery-fail", WorkDir: workDir}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error when recovery fails")
	}
	if callCount != 3 {
		t.Errorf("execTmuxNewSession called %d times, want 3 (initial + double-check + retry-after-kill)", callCount)
	}
	if !strings.Contains(err.Error(), "server dead, recovery failed") {
		t.Errorf("error should describe recovery failure; got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "tmux server recovery failed") {
		t.Errorf("expected ERROR log for recovery failure; got: %s", out)
	}
}

// TestSpawn_TmuxError_NonServer_NoRecovery verifies that a generic tmux error
// (not a dead-server error) causes spawn() to return immediately without
// calling execTmuxKillServer or retrying.
func TestSpawn_TmuxError_NonServer_NoRecovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	callCount := 0
	killCalled := false
	execTmuxNewSession = func(_ []string) ([]byte, error) {
		callCount++
		return []byte("invalid option: -Z"), fmt.Errorf("exit status 1")
	}
	execTmuxKillServer = func() { killCalled = true }

	workDir := t.TempDir()
	s := &Session{ID: "tmux-non-server-err", WorkDir: workDir}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	if callCount != 1 {
		t.Errorf("execTmuxNewSession called %d times, want 1 (no retry for non-server errors)", callCount)
	}
	if killCalled {
		t.Error("execTmuxKillServer should not be called for non-server errors")
	}
}

// TestSpawn_TmuxServerDead_ConcurrentRecoveryIsSerializedByMutex verifies that
// when two goroutines simultaneously detect a dead tmux server, their recovery
// blocks are serialized — execTmuxKillServer is never called concurrently.
// This guards against the interleaving where goroutine B calls execTmuxKillServer
// and destroys the server that goroutine A just recovered.
func TestSpawn_TmuxServerDead_ConcurrentRecoveryIsSerializedByMutex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	orig := quickExitWindow
	quickExitWindow = 10 * time.Second
	t.Cleanup(func() { quickExitWindow = orig })

	// Both spawns always fail with a dead-server error so both enter the recovery
	// block. The test is about serialization, not recovery success.
	execTmuxNewSession = func(_ []string) ([]byte, error) {
		return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
	}

	var concurrent int64  // current number of goroutines inside execTmuxKillServer
	var raceDetected int64 // set non-zero if concurrent > 1 is ever observed
	execTmuxKillServer = func() {
		n := atomic.AddInt64(&concurrent, 1)
		if n > 1 {
			atomic.StoreInt64(&raceDetected, 1)
		}
		time.Sleep(20 * time.Millisecond) // hold long enough to detect overlap
		atomic.AddInt64(&concurrent, -1)
	}

	workDir := t.TempDir()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release both goroutines simultaneously
			s := &Session{ID: fmt.Sprintf("concurrent-recovery-%d", i), WorkDir: workDir}
			s.spawn() //nolint:errcheck // error expected — only testing serialization
		}(i)
	}
	close(start)
	wg.Wait()

	if atomic.LoadInt64(&raceDetected) != 0 {
		t.Error("execTmuxKillServer was called concurrently — tmuxRecoveryMu did not serialize recovery")
	}
}

// TestSpawn_TmuxServerDead_DoubleCheckPreventsKillingRecoveredServer verifies
// that when goroutine A recovers the dead tmux server inside the mutex, goroutine
// B's double-check (retrying before killing) detects the server is now alive and
// skips execTmuxKillServer entirely — preventing B from destroying A's session.
// This validates the double-checked locking pattern: re-validate after acquiring
// the lock before proceeding with the destructive kill step.
func TestSpawn_TmuxServerDead_DoubleCheckPreventsKillingRecoveredServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	orig := quickExitWindow
	quickExitWindow = 10 * time.Second
	t.Cleanup(func() { quickExitWindow = orig })

	// The server is "dead" until execTmuxKillServer is called; after that it is
	// "alive". This models: goroutine A's kill restarts the server, so goroutine
	// B's double-check (which runs after A releases the mutex) succeeds and B
	// skips its own kill.
	var killCount int64
	execTmuxNewSession = func(_ []string) ([]byte, error) {
		if atomic.LoadInt64(&killCount) == 0 {
			return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
		}
		return nil, nil // server recovered after kill
	}
	execTmuxKillServer = func() {
		atomic.AddInt64(&killCount, 1)
	}

	workDir := t.TempDir()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s := &Session{ID: fmt.Sprintf("double-check-%d", i), WorkDir: workDir}
			errs[i] = s.spawn()
			s.kill() // cancel quick-exit goroutine
		}(i)
	}
	close(start)
	wg.Wait()

	// Both spawns must succeed.
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: spawn returned unexpected error: %v", i, err)
		}
	}
	// execTmuxKillServer must be called exactly once: by the goroutine that held
	// the mutex first. The second goroutine's double-check sees the recovered
	// server and skips the kill.
	if got := atomic.LoadInt64(&killCount); got != 1 {
		t.Errorf("execTmuxKillServer called %d times, want 1 (double-check must prevent second kill)", got)
	}
}

// TestSpawn_QuickExit_SuppressedWhenOutcomeSignaled verifies that when
// DropletSignaledOutcome returns true the goroutine does not emit a warning —
// a fast agent that completed successfully should not be flagged as a possible
// auth failure.
func TestSpawn_QuickExit_SuppressedWhenOutcomeSignaled(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true") // exits immediately

	workDir := t.TempDir()
	s := &Session{
		ID:      "outcome-suppress-test",
		WorkDir: workDir,
		DropletSignaledOutcome: func() bool {
			return true // simulate a successfully completed droplet
		},
	}

	orig := quickExitWindow
	quickExitWindow = 200 * time.Millisecond
	t.Cleanup(func() { quickExitWindow = orig })

	if err := s.spawn(); err != nil {
		t.Skip("spawn failed (tmux environment issue):", err)
	}
	defer s.kill()

	// Wait longer than the window.
	time.Sleep(500 * time.Millisecond)

	out := buf.String()
	if strings.Contains(out, "exited quickly") {
		t.Errorf("unexpected quick-exit warning when outcome already signaled; got: %s", out)
	}
}
