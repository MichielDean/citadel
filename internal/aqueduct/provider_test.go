package aqueduct

import (
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/provider"
)

// --- ResolveProvider tests ---

// TestResolveProvider_DefaultsToClaudeWhenNoProvider verifies that an AqueductConfig
// with no provider block resolves to the built-in claude preset unchanged.
func TestResolveProvider_DefaultsToClaudeWhenNoProvider(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{{Name: "myrepo", Cataractae: 1}},
	}
	preset, err := cfg.ResolveProvider("myrepo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Name != "claude" {
		t.Errorf("Name = %q, want %q", preset.Name, "claude")
	}
	if preset.Command != "claude" {
		t.Errorf("Command = %q, want %q", preset.Command, "claude")
	}
}

// TestResolveProvider_UsesTopLevelProviderName verifies that the top-level provider
// name selects the corresponding built-in preset.
func TestResolveProvider_UsesTopLevelProviderName(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:    []RepoConfig{{Name: "r", Cataractae: 1}},
		Provider: &ProviderConfig{Name: "gemini"},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Name != "gemini" {
		t.Errorf("Name = %q, want %q", preset.Name, "gemini")
	}
	if preset.Command != "gemini" {
		t.Errorf("Command = %q, want %q", preset.Command, "gemini")
	}
}

// TestResolveProvider_RepoNameOverridesTopLevelName verifies that a repo-level
// provider name takes precedence over the top-level provider name.
func TestResolveProvider_RepoNameOverridesTopLevelName(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r", Cataractae: 1, Provider: &ProviderConfig{Name: "codex"}},
		},
		Provider: &ProviderConfig{Name: "gemini"},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Name != "codex" {
		t.Errorf("Name = %q, want %q", preset.Name, "codex")
	}
}

// TestResolveProvider_TopLevelCommandOverride verifies that a top-level command
// override replaces the preset's executable.
func TestResolveProvider_TopLevelCommandOverride(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:    []RepoConfig{{Name: "r", Cataractae: 1}},
		Provider: &ProviderConfig{Command: "my-claude"},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Command != "my-claude" {
		t.Errorf("Command = %q, want %q", preset.Command, "my-claude")
	}
}

// TestResolveProvider_RepoCommandOverridesTopLevel verifies that a repo-level
// command override wins over the top-level command override.
func TestResolveProvider_RepoCommandOverridesTopLevel(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r", Cataractae: 1, Provider: &ProviderConfig{Command: "repo-claude"}},
		},
		Provider: &ProviderConfig{Command: "top-claude"},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Command != "repo-claude" {
		t.Errorf("Command = %q, want %q", preset.Command, "repo-claude")
	}
}

// TestResolveProvider_ArgsAreAppended verifies that top-level and repo-level args
// are both appended to the preset's built-in args.
func TestResolveProvider_ArgsAreAppended(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r", Cataractae: 1, Provider: &ProviderConfig{Args: []string{"--repo-arg"}}},
		},
		Provider: &ProviderConfig{Args: []string{"--top-arg"}},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasTopArg := false
	hasRepoArg := false
	for _, a := range preset.Args {
		if a == "--top-arg" {
			hasTopArg = true
		}
		if a == "--repo-arg" {
			hasRepoArg = true
		}
	}
	if !hasTopArg {
		t.Errorf("Args %v missing --top-arg", preset.Args)
	}
	if !hasRepoArg {
		t.Errorf("Args %v missing --repo-arg", preset.Args)
	}
}

// TestResolveProvider_EnvIsMerged verifies that env entries from top-level and
// repo-level configs are merged, with repo-level values winning for shared keys.
func TestResolveProvider_EnvIsMerged(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{
			{Name: "r", Cataractae: 1, Provider: &ProviderConfig{
				Env: map[string]string{"REPO_KEY": "repo-val", "SHARED": "from-repo"},
			}},
		},
		Provider: &ProviderConfig{
			Env: map[string]string{"TOP_KEY": "top-val", "SHARED": "from-top"},
		},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.ExtraEnv["TOP_KEY"] != "top-val" {
		t.Errorf("ExtraEnv[TOP_KEY] = %q, want %q", preset.ExtraEnv["TOP_KEY"], "top-val")
	}
	if preset.ExtraEnv["REPO_KEY"] != "repo-val" {
		t.Errorf("ExtraEnv[REPO_KEY] = %q, want %q", preset.ExtraEnv["REPO_KEY"], "repo-val")
	}
	// Repo value overrides top-level for shared keys.
	if preset.ExtraEnv["SHARED"] != "from-repo" {
		t.Errorf("ExtraEnv[SHARED] = %q, want %q", preset.ExtraEnv["SHARED"], "from-repo")
	}
}

// TestResolveProvider_UnknownProviderReturnsError verifies that an unknown preset
// name causes ResolveProvider to return an error.
func TestResolveProvider_UnknownProviderReturnsError(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:    []RepoConfig{{Name: "r", Cataractae: 1}},
		Provider: &ProviderConfig{Name: "nonexistent"},
	}
	_, err := cfg.ResolveProvider("r")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "nonexistent")
	}
}

// TestResolveProvider_UnknownRepoFallsBackToTopLevel verifies that an unknown
// repo name causes only the top-level provider to be applied.
func TestResolveProvider_UnknownRepoFallsBackToTopLevel(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:    []RepoConfig{{Name: "r", Cataractae: 1}},
		Provider: &ProviderConfig{Name: "gemini"},
	}
	preset, err := cfg.ResolveProvider("nonexistent-repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Name != "gemini" {
		t.Errorf("Name = %q, want %q", preset.Name, "gemini")
	}
}

// TestResolveProvider_CustomProviderStartsEmpty verifies that "custom" as the
// provider name starts from an empty preset (no built-in base).
func TestResolveProvider_CustomProviderStartsEmpty(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{{Name: "r", Cataractae: 1}},
		Provider: &ProviderConfig{
			Name:    "custom",
			Command: "my-agent",
			Args:    []string{"--custom-flag"},
		},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error for custom provider: %v", err)
	}
	if preset.Command != "my-agent" {
		t.Errorf("Command = %q, want %q", preset.Command, "my-agent")
	}
	// No built-in args should be inherited.
	for _, a := range preset.Args {
		if a == "--dangerously-skip-permissions" {
			t.Error("custom provider inherited built-in claude args; want clean slate")
		}
	}
}

// TestResolveProvider_EmptyRepoNameReturnsTopLevel verifies that an empty repo
// name applies only the top-level provider config.
func TestResolveProvider_EmptyRepoNameReturnsTopLevel(t *testing.T) {
	cfg := &AqueductConfig{
		Repos:    []RepoConfig{{Name: "r", Cataractae: 1}},
		Provider: &ProviderConfig{Command: "override-cmd"},
	}
	preset, err := cfg.ResolveProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preset.Command != "override-cmd" {
		t.Errorf("Command = %q, want %q", preset.Command, "override-cmd")
	}
}

// TestResolveProvider_NoProviderBlockUsesBuiltinFields verifies that the full
// built-in preset is returned when no provider config is present.
func TestResolveProvider_NoProviderBlockUsesBuiltinFields(t *testing.T) {
	cfg := &AqueductConfig{
		Repos: []RepoConfig{{Name: "r", Cataractae: 1}},
	}
	preset, err := cfg.ResolveProvider("r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// claude built-in has ModelFlag "--model"
	if preset.ModelFlag != "--model" {
		t.Errorf("ModelFlag = %q, want %q", preset.ModelFlag, "--model")
	}
}

// --- ValidateModelForProvider tests ---

// TestValidateModelForProvider_NoModelReturnsEmptyWarning verifies that a step
// with no model set produces no warning.
func TestValidateModelForProvider_NoModelReturnsEmptyWarning(t *testing.T) {
	step := WorkflowCataractae{Name: "s", Type: CataractaeTypeAgent}
	preset := provider.ProviderPreset{Name: "claude", ModelFlag: "--model"}
	if warn := ValidateModelForProvider(step, preset); warn != "" {
		t.Errorf("expected empty warning, got %q", warn)
	}
}

// TestValidateModelForProvider_ModelWithModelFlagReturnsEmptyWarning verifies
// that a step with a model set against a preset that supports ModelFlag is fine.
func TestValidateModelForProvider_ModelWithModelFlagReturnsEmptyWarning(t *testing.T) {
	m := "claude-opus-4-6"
	step := WorkflowCataractae{Name: "s", Type: CataractaeTypeAgent, Model: &m}
	preset := provider.ProviderPreset{Name: "claude", ModelFlag: "--model"}
	if warn := ValidateModelForProvider(step, preset); warn != "" {
		t.Errorf("expected empty warning, got %q", warn)
	}
}

// TestValidateModelForProvider_ModelWithoutModelFlagReturnsWarning verifies
// that setting a model against a preset with no ModelFlag produces a warning.
func TestValidateModelForProvider_ModelWithoutModelFlagReturnsWarning(t *testing.T) {
	m := "some-model"
	step := WorkflowCataractae{Name: "my-step", Type: CataractaeTypeAgent, Model: &m}
	preset := provider.ProviderPreset{Name: "copilot", ModelFlag: ""}
	warn := ValidateModelForProvider(step, preset)
	if warn == "" {
		t.Fatal("expected warning, got empty string")
	}
	if !strings.Contains(warn, "my-step") {
		t.Errorf("warning %q should mention step name %q", warn, "my-step")
	}
	if !strings.Contains(warn, "copilot") {
		t.Errorf("warning %q should mention provider name %q", warn, "copilot")
	}
	if !strings.Contains(warn, "some-model") {
		t.Errorf("warning %q should mention model value %q", warn, "some-model")
	}
}
