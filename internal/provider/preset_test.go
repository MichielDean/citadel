package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestBuiltins_ReturnsExpectedPresetNames verifies the five built-in presets are present.
func TestBuiltins_ReturnsExpectedPresetNames(t *testing.T) {
	want := []string{"claude", "codex", "gemini", "copilot", "opencode"}
	got := Builtins()

	if len(got) != len(want) {
		t.Fatalf("Builtins() returned %d presets, want %d", len(got), len(want))
	}

	byName := make(map[string]ProviderPreset)
	for _, p := range got {
		byName[p.Name] = p
	}
	for _, name := range want {
		if _, ok := byName[name]; !ok {
			t.Errorf("built-in preset %q not found", name)
		}
	}
}

// TestBuiltins_ClaudePreset validates each field of the claude built-in.
func TestBuiltins_ClaudePreset(t *testing.T) {
	got := builtinByName(t, "claude")

	assertStr(t, "Command", "claude", got.Command)
	assertStrs(t, "Args", []string{"--dangerously-skip-permissions"}, got.Args)
	assertStrs(t, "EnvPassthrough", []string{"ANTHROPIC_API_KEY"}, got.EnvPassthrough)
	assertStr(t, "ModelFlag", "--model", got.ModelFlag)
	assertStr(t, "AddDirFlag", "--add-dir", got.AddDirFlag)
	assertStr(t, "InstructionsFile", "CLAUDE.md", got.InstructionsFile)
}

// TestBuiltins_CodexPreset validates each field of the codex built-in.
func TestBuiltins_CodexPreset(t *testing.T) {
	got := builtinByName(t, "codex")

	assertStr(t, "Command", "codex", got.Command)
	assertStrs(t, "Args", []string{"--dangerously-bypass-approvals-and-sandbox"}, got.Args)
	assertStrs(t, "EnvPassthrough", []string{"OPENAI_API_KEY"}, got.EnvPassthrough)
	assertStr(t, "InstructionsFile", "AGENTS.md", got.InstructionsFile)
	assertStr(t, "ResumeStyle", string(ResumeStyleSubcommand), string(got.ResumeStyle))
}

// TestBuiltins_GeminiPreset validates each field of the gemini built-in.
func TestBuiltins_GeminiPreset(t *testing.T) {
	got := builtinByName(t, "gemini")

	assertStr(t, "Command", "gemini", got.Command)
	assertStrs(t, "Args", []string{"--yolo"}, got.Args)
	assertStrs(t, "EnvPassthrough", []string{"GEMINI_API_KEY"}, got.EnvPassthrough)
	assertStr(t, "ModelFlag", "--model", got.ModelFlag)
	assertStr(t, "InstructionsFile", "GEMINI.md", got.InstructionsFile)
}

// TestBuiltins_CopilotPreset validates each field of the copilot built-in.
func TestBuiltins_CopilotPreset(t *testing.T) {
	got := builtinByName(t, "copilot")

	assertStr(t, "Command", "copilot", got.Command)
	assertStrs(t, "Args", []string{"--yolo"}, got.Args)
	assertStrs(t, "EnvPassthrough", []string{"GH_TOKEN"}, got.EnvPassthrough)
	assertStr(t, "InstructionsFile", "AGENTS.md", got.InstructionsFile)
	if got.ReadyDelayMs != 5000 {
		t.Errorf("ReadyDelayMs = %d, want 5000", got.ReadyDelayMs)
	}
}

// TestBuiltins_OpencodePreset validates each field of the opencode built-in.
func TestBuiltins_OpencodePreset(t *testing.T) {
	got := builtinByName(t, "opencode")

	assertStr(t, "Command", "opencode", got.Command)
	assertStr(t, "InstructionsFile", "AGENTS.md", got.InstructionsFile)
}

// TestBuiltins_ReturnsCopy verifies that mutating the returned slice does not affect the built-ins.
func TestBuiltins_ReturnsCopy(t *testing.T) {
	t.Run("string field mutation is isolated", func(t *testing.T) {
		first := Builtins()
		first[0].Command = "mutated"

		second := Builtins()
		if second[0].Command == "mutated" {
			t.Error("Builtins() returned a reference to internal state, want an independent copy")
		}
	})

	t.Run("slice field mutation is isolated", func(t *testing.T) {
		first := Builtins()
		original := first[0].Args[0]
		first[0].Args[0] = "mutated"

		second := Builtins()
		if second[0].Args[0] != original {
			t.Errorf("Builtins() Args[0] = %q after mutation, want %q — slice field shares backing array with global state", second[0].Args[0], original)
		}
	})
}

// TestLoadUserPresets_NoFileReturnsBuiltins verifies that a missing file returns built-ins unchanged.
func TestLoadUserPresets_NoFileReturnsBuiltins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	presets, err := LoadUserPresets(path)
	if err != nil {
		t.Fatalf("LoadUserPresets: unexpected error: %v", err)
	}

	want := Builtins()
	if len(presets) != len(want) {
		t.Errorf("got %d presets, want %d", len(presets), len(want))
	}
}

// TestLoadUserPresets_OverridesBuiltinByName verifies user entries replace built-ins with matching names.
func TestLoadUserPresets_OverridesBuiltinByName(t *testing.T) {
	override := ProviderPreset{Name: "claude", Command: "my-claude"}
	path := writePresetsJSON(t, []ProviderPreset{override})

	presets, err := LoadUserPresets(path)
	if err != nil {
		t.Fatalf("LoadUserPresets: %v", err)
	}

	got := findByName(presets, "claude")
	if got == nil {
		t.Fatal("claude preset not found after override")
	}
	assertStr(t, "Command", "my-claude", got.Command)

	// Other built-ins must still be present.
	if findByName(presets, "gemini") == nil {
		t.Error("gemini built-in missing after user override")
	}
}

// TestLoadUserPresets_AppendsUnknownPreset verifies that unknown presets are appended.
func TestLoadUserPresets_AppendsUnknownPreset(t *testing.T) {
	extra := ProviderPreset{Name: "custom", Command: "my-agent"}
	path := writePresetsJSON(t, []ProviderPreset{extra})

	presets, err := LoadUserPresets(path)
	if err != nil {
		t.Fatalf("LoadUserPresets: %v", err)
	}

	got := findByName(presets, "custom")
	if got == nil {
		t.Fatal("custom preset not found after merge")
	}
	assertStr(t, "Command", "my-agent", got.Command)

	// Built-ins must still be present.
	if findByName(presets, "claude") == nil {
		t.Error("claude built-in missing after user preset append")
	}
}

// TestLoadUserPresets_InvalidJSONReturnsError verifies that malformed JSON returns an error.
func TestLoadUserPresets_InvalidJSONReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadUserPresets(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// TestLoadUserPresets_MultipleOverridesAndAppends exercises a realistic mixed JSON file.
func TestLoadUserPresets_MultipleOverridesAndAppends(t *testing.T) {
	user := []ProviderPreset{
		{Name: "claude", Command: "claude-dev", ModelFlag: "--model-override"},
		{Name: "new-agent", Command: "new-agent-bin", InstructionsFile: "NEW.md"},
	}
	path := writePresetsJSON(t, user)

	presets, err := LoadUserPresets(path)
	if err != nil {
		t.Fatalf("LoadUserPresets: %v", err)
	}

	claude := findByName(presets, "claude")
	if claude == nil {
		t.Fatal("claude not found")
	}
	assertStr(t, "claude Command", "claude-dev", claude.Command)
	assertStr(t, "claude ModelFlag", "--model-override", claude.ModelFlag)

	newAgent := findByName(presets, "new-agent")
	if newAgent == nil {
		t.Fatal("new-agent not found")
	}
	assertStr(t, "new-agent InstructionsFile", "NEW.md", newAgent.InstructionsFile)

	// Unmodified built-ins survive.
	if findByName(presets, "codex") == nil {
		t.Error("codex built-in missing")
	}
}

// --- helpers ---

func builtinByName(t *testing.T, name string) ProviderPreset {
	t.Helper()
	for _, p := range Builtins() {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("built-in preset %q not found", name)
	return ProviderPreset{}
}

func findByName(presets []ProviderPreset, name string) *ProviderPreset {
	for i := range presets {
		if presets[i].Name == name {
			return &presets[i]
		}
	}
	return nil
}

func assertStr(t *testing.T, field, want, got string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertStrs(t *testing.T, field string, want, got []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v (len %d), want %v (len %d)", field, got, len(got), want, len(want))
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", field, i, got[i], want[i])
		}
	}
}

func writePresetsJSON(t *testing.T, presets []ProviderPreset) string {
	t.Helper()
	data, err := json.Marshal(presets)
	if err != nil {
		t.Fatalf("marshal test JSON: %v", err)
	}
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write test JSON: %v", err)
	}
	return path
}
