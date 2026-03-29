package cataractae

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
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

	// Isolate HOME so ensureClaudeOAuthFresh finds no credentials file and
	// skips the refresh entirely. Without this the test hits the real OAuth
	// endpoint with whatever (potentially expired) credentials exist on the
	// test machine, causing intermittent CI failures.
	t.Setenv("HOME", t.TempDir())

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

// TestSpawn_QuickExit_CancelledByKill verifies that calling kill() before the
// quick-exit window expires cancels the goroutine so no spurious warning is logged
// when the session is intentionally stopped.

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


	workDir := t.TempDir()
	s := &Session{ID: "tmux-recovery-ok", WorkDir: workDir}

	err := s.spawn()

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

// TestSpawn_TmuxError_ArgsInErrorMessage_InitialFailure verifies that when the
// initial execTmuxNewSession call fails with a non-dead-server error, the returned
// error message includes the tmux args so operators can reproduce the failure.
func TestSpawn_TmuxError_ArgsInErrorMessage_InitialFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")

	origSpawn := execTmuxNewSession
	t.Cleanup(func() { execTmuxNewSession = origSpawn })

	execTmuxNewSession = func(_ []string) ([]byte, error) {
		return []byte("invalid option: -Z"), fmt.Errorf("exit status 1")
	}

	workDir := t.TempDir()
	s := &Session{ID: "spawn-args-test", WorkDir: workDir}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	// The session ID appears inside the [args: ...] portion since it is one of
	// the args passed to tmux new-session.
	if !strings.Contains(err.Error(), "[args:") {
		t.Errorf("error should contain [args: ...]; got: %v", err)
	}
	if !strings.Contains(err.Error(), s.ID) {
		t.Errorf("error should contain session ID %q in args; got: %v", s.ID, err)
	}
}

// TestSpawn_TmuxServerDead_ArgsInErrorMessage_RecoveryFails verifies that when
// all three spawn attempts fail (initial + double-check + post-kill retry), the
// returned error message includes the tmux args for diagnostics.
func TestSpawn_TmuxServerDead_ArgsInErrorMessage_RecoveryFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	execTmuxNewSession = func(_ []string) ([]byte, error) {
		return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
	}
	execTmuxKillServer = func() {}

	workDir := t.TempDir()
	s := &Session{ID: "spawn-args-recovery-fail", WorkDir: workDir}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	if !strings.Contains(err.Error(), "[args:") {
		t.Errorf("error should contain [args: ...]; got: %v", err)
	}
	if !strings.Contains(err.Error(), s.ID) {
		t.Errorf("error should contain session ID %q in args; got: %v", s.ID, err)
	}
	if !strings.Contains(err.Error(), "server dead, recovery failed") {
		t.Errorf("error should describe recovery failure; got: %v", err)
	}
}

// TestSpawn_TmuxServerDead_ArgsInErrorMessage_DoubleCheckNonServerError verifies
// that when the double-check retry (inside the recovery mutex) fails with a
// non-dead-server error, the returned error includes the tmux args.
func TestSpawn_TmuxServerDead_ArgsInErrorMessage_DoubleCheckNonServerError(t *testing.T) {
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
		if callCount == 1 {
			// First call: dead-server error to trigger recovery path.
			return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
		}
		// Double-check retry: non-dead-server error.
		return []byte("invalid option: -Z"), fmt.Errorf("exit status 1")
	}
	execTmuxKillServer = func() { killCalled = true }

	workDir := t.TempDir()
	s := &Session{ID: "spawn-args-doublecheck", WorkDir: workDir}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	if killCalled {
		t.Error("execTmuxKillServer should not be called when double-check returns a non-server error")
	}
	if !strings.Contains(err.Error(), "[args:") {
		t.Errorf("error should contain [args: ...]; got: %v", err)
	}
	if !strings.Contains(err.Error(), s.ID) {
		t.Errorf("error should contain session ID %q in args; got: %v", s.ID, err)
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

// --- spawn guard tests (PR #204: don't kill running sessions) ---

// TestSpawn_AlreadyRunning_SkipsRespawn verifies that when a healthy session is
// already alive and the agent process is running, Spawn() returns nil without
// touching the session. This is the core correctness check for the self-kill fix:
// the heartbeat resetting a droplet must not cause the dispatcher to kill a
// session that is actively doing work.
func TestSpawn_AlreadyRunning_SkipsRespawn(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "spawn-guard-running-test"

	// Start a long-running tmux session manually to simulate an active agent.
	// 'sleep 60' keeps the session alive; its process name is 'sleep' which is
	// not in ProcessNames, so isAgentAlive() returns true (conservative fallback
	// when ProcessNames is empty).
	spawnArgs := []string{"new-session", "-d", "-s", sessionID, "-c", t.TempDir(), "sleep 60"}
	if out, err := exec.Command("tmux", spawnArgs...).CombinedOutput(); err != nil {
		t.Skipf("could not create tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionID).Run()
	})

	workDir := t.TempDir()
	s := &Session{
		ID:      sessionID,
		WorkDir: workDir,
		// No ProcessNames configured → isAgentAlive() returns true (conservative).
		Preset: provider.ProviderPreset{},
	}

	err := s.Spawn()
	if err != nil {
		t.Fatalf("Spawn() returned error for already-running session: %v", err)
	}

	// The session must still be alive — Spawn() must not have killed it.
	if !isSessionAlive(sessionID) {
		t.Error("Spawn() killed the running session — expected it to be left alone")
	}

	// Spawn() must have logged the skip message.
	out := buf.String()
	if !strings.Contains(out, "already running") {
		t.Errorf("expected 'already running' log; got: %s", out)
	}

	// kill() must NOT have been called (no "killing zombie" log).
	if strings.Contains(out, "killing zombie") {
		t.Errorf("unexpected 'killing zombie' log — session should have been left alone; got: %s", out)
	}
}

// TestSpawn_ZombieSession_KillsAndRespawns verifies that when a tmux session
// exists but the agent process has exited (zombie state), Spawn() kills the
// dead shell and starts a fresh session.
func TestSpawn_ZombieSession_KillsAndRespawns(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	buf := captureDefaultSlog(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true") // exits immediately — simulates dead agent

	const sessionID = "spawn-guard-zombie-test"

	// Create a zombie: start a tmux session running 'bash' (shell prompt only,
	// no agent). ProcessNames = ["notbash"] so isAgentAlive() returns false.
	spawnArgs := []string{"new-session", "-d", "-s", sessionID, "-c", t.TempDir(), "bash"}
	if out, err := exec.Command("tmux", spawnArgs...).CombinedOutput(); err != nil {
		t.Skipf("could not create tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", sessionID).Run()
	})

	workDir := t.TempDir()
	s := &Session{
		ID:      sessionID,
		WorkDir: workDir,
		// ProcessNames that don't match 'bash' → isAgentAlive() returns false.
		Preset: provider.ProviderPreset{
			Name:         "test",
			Command:      "true",
			ProcessNames: []string{"notbash", "notnode"},
		},
	}

	// Override execTmuxNewSession so the respawn doesn't need a real agent.
	origSpawn := execTmuxNewSession
	var spawnCalled bool
	execTmuxNewSession = func(args []string) ([]byte, error) {
		spawnCalled = true
		return nil, nil
	}
	t.Cleanup(func() { execTmuxNewSession = origSpawn })

	err := s.Spawn()
	if err != nil {
		t.Fatalf("Spawn() returned unexpected error: %v", err)
	}

	out := buf.String()

	// Must have logged zombie detection.
	if !strings.Contains(out, "killing zombie") {
		t.Errorf("expected 'killing zombie' log; got: %s", out)
	}

	// Must have attempted a new spawn after killing the zombie.
	if !spawnCalled {
		t.Error("expected execTmuxNewSession to be called after zombie kill — got no respawn")
	}
}

// TestSpawn_NoExistingSession_SpawnsNormally verifies the happy path: when no
// session exists, Spawn() creates one without any kill or skip logic.
func TestSpawn_NoExistingSession_SpawnsNormally(t *testing.T) {
	buf := captureDefaultSlog(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "spawn-guard-fresh-test"

	// Ensure no session with this ID exists.
	exec.Command("tmux", "kill-session", "-t", sessionID).Run()

	var spawnCalled bool
	origSpawn := execTmuxNewSession
	execTmuxNewSession = func(args []string) ([]byte, error) {
		spawnCalled = true
		return nil, nil
	}
	t.Cleanup(func() { execTmuxNewSession = origSpawn })

	s := &Session{
		ID:      sessionID,
		WorkDir: t.TempDir(),
		Preset:  provider.ProviderPreset{Name: "test", Command: "true"},
	}

	err := s.Spawn()
	if err != nil {
		t.Fatalf("Spawn() returned unexpected error: %v", err)
	}

	// Must have spawned — no skip, no zombie kill.
	if !spawnCalled {
		t.Error("expected execTmuxNewSession to be called for a fresh spawn")
	}

	out := buf.String()
	if strings.Contains(out, "already running") {
		t.Errorf("unexpected 'already running' log for fresh spawn; got: %s", out)
	}
	if strings.Contains(out, "killing zombie") {
		t.Errorf("unexpected 'killing zombie' log for fresh spawn; got: %s", out)
	}
}

// --- redactArgs tests ---

// TestRedactArgs_RedactsEnvValues verifies that redactArgs masks the value portion
// of -e KEY=VALUE pairs while preserving structural args and key names.
func TestRedactArgs_RedactsEnvValues(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "no env args",
			input: []string{"new-session", "-d", "-s", "my-session", "-c", "/work"},
			want:  []string{"new-session", "-d", "-s", "my-session", "-c", "/work"},
		},
		{
			name:  "single env arg is redacted",
			input: []string{"-e", "ANTHROPIC_API_KEY=sk-secret"},
			want:  []string{"-e", "ANTHROPIC_API_KEY=[REDACTED]"},
		},
		{
			name:  "multiple env args all redacted",
			input: []string{"-e", "ANTHROPIC_API_KEY=tok123", "-e", "GH_TOKEN=ghp_abc", "-e", "CT_DB=postgres://user:pass@host/db"},
			want:  []string{"-e", "ANTHROPIC_API_KEY=[REDACTED]", "-e", "GH_TOKEN=[REDACTED]", "-e", "CT_DB=[REDACTED]"},
		},
		{
			name: "structural args preserved alongside env args",
			input: []string{
				"new-session", "-d", "-s", "my-session", "-c", "/work",
				"-e", "ANTHROPIC_API_KEY=sk-secret",
				"-e", "PATH=/usr/bin:/bin",
				"-e", "CT_CATARACTA_NAME=implementer",
				"claude --dangerously-skip-permissions -p 'do work'",
			},
			want: []string{
				"new-session", "-d", "-s", "my-session", "-c", "/work",
				"-e", "ANTHROPIC_API_KEY=[REDACTED]",
				"-e", "PATH=[REDACTED]",
				"-e", "CT_CATARACTA_NAME=[REDACTED]",
				"claude --dangerously-skip-permissions -p 'do work'",
			},
		},
		{
			name:  "env arg without equals sign left as-is",
			input: []string{"-e", "NOEQUALS"},
			want:  []string{"-e", "NOEQUALS"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactArgs(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("redactArgs(%v) len = %d, want %d", tc.input, len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("redactArgs(%v)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestRedactArgs_DoesNotMutateInput verifies that redactArgs returns a new slice
// and does not modify the original args.
func TestRedactArgs_DoesNotMutateInput(t *testing.T) {
	input := []string{"-e", "SECRET=plaintext"}
	original := make([]string, len(input))
	copy(original, input)

	redactArgs(input)

	for i, v := range input {
		if v != original[i] {
			t.Errorf("input[%d] was mutated: got %q, want %q", i, v, original[i])
		}
	}
}

// TestSpawn_TmuxError_SecretsNotLeaked_InitialFailure verifies that when the
// initial spawn fails, env var values (ANTHROPIC_API_KEY, GH_TOKEN) do not
// appear in plaintext in the returned error message.
func TestSpawn_TmuxError_SecretsNotLeaked_InitialFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")
	t.Setenv("ANTHROPIC_API_KEY", "sk-very-secret-api-key")
	t.Setenv("GH_TOKEN", "ghp_very-secret-gh-token")

	origSpawn := execTmuxNewSession
	t.Cleanup(func() { execTmuxNewSession = origSpawn })

	execTmuxNewSession = func(_ []string) ([]byte, error) {
		return []byte("invalid option: -Z"), fmt.Errorf("exit status 1")
	}

	s := &Session{ID: "secrets-test", WorkDir: t.TempDir()}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "sk-very-secret-api-key") {
		t.Errorf("error message leaks ANTHROPIC_API_KEY value in plaintext: %v", errMsg)
	}
	if strings.Contains(errMsg, "ghp_very-secret-gh-token") {
		t.Errorf("error message leaks GH_TOKEN value in plaintext: %v", errMsg)
	}
	// Structural args must still be present for diagnostics.
	if !strings.Contains(errMsg, "[args:") {
		t.Errorf("error should still contain [args: ...] for diagnostics; got: %v", errMsg)
	}
	if !strings.Contains(errMsg, s.ID) {
		t.Errorf("error should contain session ID %q; got: %v", s.ID, errMsg)
	}
}

// TestSpawn_TmuxServerDead_SecretsNotLeaked_RecoveryFails verifies that when
// all three spawn attempts fail (including post-kill retry), the error message
// does not contain env var secret values.
func TestSpawn_TmuxServerDead_SecretsNotLeaked_RecoveryFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")
	t.Setenv("ANTHROPIC_API_KEY", "sk-very-secret-api-key")
	t.Setenv("GH_TOKEN", "ghp_very-secret-gh-token")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	execTmuxNewSession = func(_ []string) ([]byte, error) {
		return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
	}
	execTmuxKillServer = func() {}

	s := &Session{ID: "secrets-recovery-fail", WorkDir: t.TempDir()}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "sk-very-secret-api-key") {
		t.Errorf("error message leaks ANTHROPIC_API_KEY value in recovery-fail path: %v", errMsg)
	}
	if strings.Contains(errMsg, "ghp_very-secret-gh-token") {
		t.Errorf("error message leaks GH_TOKEN value in recovery-fail path: %v", errMsg)
	}
	if !strings.Contains(errMsg, "server dead, recovery failed") {
		t.Errorf("error should describe recovery failure; got: %v", errMsg)
	}
}

// TestSpawn_TmuxServerDead_SecretsNotLeaked_DoubleCheckFailure verifies that
// when the double-check retry fails with a non-dead-server error, the returned
// error does not contain env var secret values.
func TestSpawn_TmuxServerDead_SecretsNotLeaked_DoubleCheckFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PATH", "true")
	t.Setenv("ANTHROPIC_API_KEY", "sk-very-secret-api-key")
	t.Setenv("GH_TOKEN", "ghp_very-secret-gh-token")

	origSpawn := execTmuxNewSession
	origKill := execTmuxKillServer
	t.Cleanup(func() {
		execTmuxNewSession = origSpawn
		execTmuxKillServer = origKill
	})

	callCount := 0
	execTmuxNewSession = func(_ []string) ([]byte, error) {
		callCount++
		if callCount == 1 {
			return []byte("no server running on /tmp/tmux-1000/default"), fmt.Errorf("exit status 1")
		}
		return []byte("invalid option: -Z"), fmt.Errorf("exit status 1")
	}
	execTmuxKillServer = func() {}

	s := &Session{ID: "secrets-doublecheck", WorkDir: t.TempDir()}

	err := s.spawn()

	if err == nil {
		t.Fatal("spawn should have returned an error")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "sk-very-secret-api-key") {
		t.Errorf("error message leaks ANTHROPIC_API_KEY value in double-check path: %v", errMsg)
	}
	if strings.Contains(errMsg, "ghp_very-secret-gh-token") {
		t.Errorf("error message leaks GH_TOKEN value in double-check path: %v", errMsg)
	}
}

// TestBuildPrompt_TemplateCtxWiring_AddDirProvider_RendersMarker verifies that
// the SupportsAddDir=true path (line 449) renders {{.Step.Name}} using TemplateCtx.
// If the RenderTemplate call were removed, this test would fail because the raw
// marker would still appear in the prompt output.
func TestBuildPrompt_TemplateCtxWiring_AddDirProvider_RendersMarker(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "CLAUDE.md"),
		[]byte("You are the {{.Step.Name}} agent."), 0o644); err != nil {
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
		TemplateCtx: aqueduct.TemplateContext{
			Step: aqueduct.StepTemplateContext{Name: "implement"},
		},
	}
	prompt := s.buildPrompt()

	if !strings.Contains(prompt, "You are the implement agent.") {
		t.Errorf("prompt missing rendered template content; got:\n%s", prompt)
	}
	if strings.Contains(prompt, "{{.Step.Name}}") {
		t.Error("prompt still contains unreplaced template marker — RenderTemplate not called")
	}
}

// TestBuildPrompt_TemplateCtxWiring_NonAddDirProvider_RendersMarker verifies that
// the SupportsAddDir=false path (line 439) renders {{.Step.Name}} using TemplateCtx.
// If the RenderTemplate call were removed, this test would fail because the raw
// marker would still appear in the prompt output.
func TestBuildPrompt_TemplateCtxWiring_NonAddDirProvider_RendersMarker(t *testing.T) {
	dir := t.TempDir()
	identityDir := filepath.Join(dir, ".cistern", "cataractae", "implementer")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "AGENTS.md"),
		[]byte("You are the {{.Step.Name}} agent."), 0o644); err != nil {
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
		TemplateCtx: aqueduct.TemplateContext{
			Step: aqueduct.StepTemplateContext{Name: "implement"},
		},
	}
	prompt := s.buildPrompt()

	if !strings.Contains(prompt, "You are the implement agent.") {
		t.Errorf("prompt missing rendered template content; got:\n%s", prompt)
	}
	if strings.Contains(prompt, "{{.Step.Name}}") {
		t.Error("prompt still contains unreplaced template marker — RenderTemplate not called")
	}
}
