package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

func TestStatusWatchFlagRegistered(t *testing.T) {
	f := statusCmd.Flags().Lookup("watch")
	if f == nil {
		t.Fatal("--watch flag not registered on statusCmd")
	}
	if f.DefValue != "false" {
		t.Fatalf("expected --watch default false, got %q", f.DefValue)
	}
}

func TestStatusIntervalFlagRegistered(t *testing.T) {
	f := statusCmd.Flags().Lookup("interval")
	if f == nil {
		t.Fatal("--interval flag not registered on statusCmd")
	}
	if f.DefValue != "5" {
		t.Fatalf("expected --interval default 5, got %q", f.DefValue)
	}
}

func TestStatusIntervalZeroReturnsError(t *testing.T) {
	origInterval := statusInterval
	defer func() { statusInterval = origInterval }()

	statusInterval = 0
	err := statusCmd.RunE(statusCmd, nil)
	if err == nil {
		t.Fatal("expected error for --interval 0, got nil")
	}
	if err.Error() != "--interval must be at least 1" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStatusIntervalNegativeReturnsError(t *testing.T) {
	origInterval := statusInterval
	defer func() { statusInterval = origInterval }()

	statusInterval = -5
	err := statusCmd.RunE(statusCmd, nil)
	if err == nil {
		t.Fatal("expected error for --interval -5, got nil")
	}
	if err.Error() != "--interval must be at least 1" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- validateWorkflowSkills tests ---

// installFakeSkill creates a minimal SKILL.md at the expected path under fakeHome.
func installFakeSkill(t *testing.T, fakeHome, name string) {
	t.Helper()
	dir := filepath.Join(fakeHome, ".cistern", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWorkflowSkills_NoWorkflows(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := validateWorkflowSkills(nil); err != nil {
		t.Errorf("expected no error for nil workflows, got: %v", err)
	}
	if err := validateWorkflowSkills(map[string]*aqueduct.Workflow{}); err != nil {
		t.Errorf("expected no error for empty workflows, got: %v", err)
	}
}

func TestValidateWorkflowSkills_NoSkillsInWorkflow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{Name: "implement", Type: aqueduct.CataractaeTypeAgent},
			},
		},
	}
	if err := validateWorkflowSkills(workflows); err != nil {
		t.Errorf("expected no error when workflow has no skills, got: %v", err)
	}
}

func TestValidateWorkflowSkills_AllInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakeSkill(t, home, "cistern-git")
	installFakeSkill(t, home, "github-workflow")

	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{
					Name:   "implement",
					Type:   aqueduct.CataractaeTypeAgent,
					Skills: []aqueduct.SkillRef{{Name: "cistern-git"}, {Name: "github-workflow"}},
				},
			},
		},
	}
	if err := validateWorkflowSkills(workflows); err != nil {
		t.Errorf("expected no error when all skills installed, got: %v", err)
	}
}

func TestValidateWorkflowSkills_OneMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{
					Name:   "implement",
					Skills: []aqueduct.SkillRef{{Name: "not-installed-skill-xyzabc"}},
				},
			},
		},
	}
	err := validateWorkflowSkills(workflows)
	if err == nil {
		t.Fatal("expected error for missing skill, got nil")
	}
	if !strings.Contains(err.Error(), "not-installed-skill-xyzabc") {
		t.Errorf("error should mention the missing skill name; got: %v", err)
	}
}

func TestValidateWorkflowSkills_MultipleMissing_AllListed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{
					Name:   "implement",
					Skills: []aqueduct.SkillRef{{Name: "skill-alpha"}, {Name: "skill-beta"}},
				},
			},
		},
	}
	err := validateWorkflowSkills(workflows)
	if err == nil {
		t.Fatal("expected error for multiple missing skills, got nil")
	}
	if !strings.Contains(err.Error(), "skill-alpha") {
		t.Errorf("error should mention skill-alpha; got: %v", err)
	}
	if !strings.Contains(err.Error(), "skill-beta") {
		t.Errorf("error should mention skill-beta; got: %v", err)
	}
}

func TestValidateWorkflowSkills_PartiallyInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	installFakeSkill(t, home, "present-skill")

	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{
					Name:   "implement",
					Skills: []aqueduct.SkillRef{{Name: "present-skill"}, {Name: "absent-skill"}},
				},
			},
		},
	}
	err := validateWorkflowSkills(workflows)
	if err == nil {
		t.Fatal("expected error when one skill is missing, got nil")
	}
	if strings.Contains(err.Error(), "present-skill") {
		t.Errorf("error should NOT mention the installed skill; got: %v", err)
	}
	if !strings.Contains(err.Error(), "absent-skill") {
		t.Errorf("error should mention the missing skill; got: %v", err)
	}
}

func TestValidateWorkflowSkills_DeduplicatesAcrossSteps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// same skill referenced in two different steps — should appear once in the error
	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{Name: "implement", Skills: []aqueduct.SkillRef{{Name: "shared-skill"}}},
				{Name: "review", Skills: []aqueduct.SkillRef{{Name: "shared-skill"}}},
			},
		},
	}
	err := validateWorkflowSkills(workflows)
	if err == nil {
		t.Fatal("expected error for missing skill, got nil")
	}
	if count := strings.Count(err.Error(), "shared-skill"); count != 1 {
		t.Errorf("expected shared-skill to appear exactly once in error, got %d; error: %v", count, err)
	}
}

func TestValidateWorkflowSkills_DeduplicatesAcrossRepos(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// same skill referenced in two different repos — should appear once in the error
	workflows := map[string]*aqueduct.Workflow{
		"repo-a": {
			Name:       "feature",
			Cataractae: []aqueduct.WorkflowCataractae{{Name: "implement", Skills: []aqueduct.SkillRef{{Name: "shared-skill"}}}},
		},
		"repo-b": {
			Name:       "feature",
			Cataractae: []aqueduct.WorkflowCataractae{{Name: "review", Skills: []aqueduct.SkillRef{{Name: "shared-skill"}}}},
		},
	}
	err := validateWorkflowSkills(workflows)
	if err == nil {
		t.Fatal("expected error for missing skill, got nil")
	}
	if count := strings.Count(err.Error(), "shared-skill"); count != 1 {
		t.Errorf("expected shared-skill to appear exactly once in error, got %d; error: %v", count, err)
	}
}

func TestValidateWorkflowSkills_SkipsEmptySkillName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{Name: "implement", Skills: []aqueduct.SkillRef{{Name: ""}}},
			},
		},
	}
	if err := validateWorkflowSkills(workflows); err != nil {
		t.Errorf("expected no error for empty skill name (should be skipped), got: %v", err)
	}
}

func TestValidateWorkflowSkills_ErrorMentionsInstallCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	workflows := map[string]*aqueduct.Workflow{
		"repo": {
			Name: "feature",
			Cataractae: []aqueduct.WorkflowCataractae{
				{Name: "implement", Skills: []aqueduct.SkillRef{{Name: "some-skill"}}},
			},
		},
	}
	err := validateWorkflowSkills(workflows)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ct skills install") {
		t.Errorf("error should mention ct skills install command; got: %v", err)
	}
	if !strings.Contains(err.Error(), "git_sync") {
		t.Errorf("error should mention git_sync as primary recovery path; got: %v", err)
	}
}

// --- checkStartupCredentials tests ---

// writeOAuthCredentials writes a credentials file with the given OAuth expiry.
func writeOAuthCredentials(t *testing.T, home string, expiresAt int64) {
	t.Helper()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	raw := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "sk-ant-test-invalid",
			"refreshToken": "invalid-refresh-token",
			"expiresAt":    expiresAt,
		},
	}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), data, 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

func TestCheckStartupCredentials_KeyMissing_ReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")

	// No config file — falls back to checking ANTHROPIC_API_KEY.
	err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error when ANTHROPIC_API_KEY is not set, got nil")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY not set") {
		t.Errorf("error should mention ANTHROPIC_API_KEY not set; got: %v", err)
	}
	if !strings.Contains(err.Error(), "~/.cistern/env") {
		t.Errorf("error should name ~/.cistern/env; got: %v", err)
	}
}

func TestCheckStartupCredentials_KeyPresent_NoCredentialsFile_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil when key set and no credentials file, got: %v", err)
	}
}

func TestCheckStartupCredentials_KeyPresent_FreshOAuth_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")
	futureExpiry := time.Now().Add(24 * time.Hour).UnixMilli()
	writeOAuthCredentials(t, home, futureExpiry)

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil for fresh OAuth token, got: %v", err)
	}
}

func TestCheckStartupCredentials_KeyPresent_ExpiredOAuth_InjectsTokenAnyway(t *testing.T) {
	// We no longer fail on expired tokens — we inject the token and let the agent
	// try it. The session log capture will tell us if auth fails. The Claude CLI
	// manages its own refresh; if the token is expired, running claude interactively
	// will refresh it and we pick it up on the next spawn.
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	writeOAuthCredentials(t, home, 1000) // expires 1970-01-01 — definitely expired

	err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml"))
	if err != nil {
		t.Errorf("expected nil even for expired token (inject and try), got: %v", err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "sk-ant-test-invalid" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want sk-ant-test-invalid", got)
	}
}

func TestCheckStartupCredentials_KeyPresent_ZeroExpiry_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")
	writeOAuthCredentials(t, home, 0) // zero ExpiresAt = no expiry info → skip silently

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil when ExpiresAt is zero (no expiry info), got: %v", err)
	}
}

// writeMinimalConfig writes a minimal cistern.yaml with the given provider name
// to a temp dir and returns the path to the config file.
func writeMinimalConfig(t *testing.T, dir, providerName string) string {
	t.Helper()
	cisternDir := filepath.Join(dir, ".cistern")
	if err := os.MkdirAll(cisternDir, 0o755); err != nil {
		t.Fatalf("mkdir .cistern: %v", err)
	}
	yaml := `repos:
  - name: testrepo
    url: https://github.com/example/testrepo
    workflow_path: aqueduct/workflow.yaml
    cataractae: 1
    prefix: ct
provider:
  name: ` + providerName + `
max_cataractae: 1
`
	cfgPath := filepath.Join(cisternDir, "cistern.yaml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// TestCheckStartupCredentials_CodexProvider_OpenAIKeySet_ReturnsNil verifies
// that checkStartupCredentials passes when the configured provider is codex and
// OPENAI_API_KEY is set, even when ANTHROPIC_API_KEY is unset.
func TestCheckStartupCredentials_CodexProvider_OpenAIKeySet_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "codex")

	t.Setenv("OPENAI_API_KEY", "sk-openai-test-key")
	t.Setenv("ANTHROPIC_API_KEY", "")

	if err := checkStartupCredentials(home, cfgPath); err != nil {
		t.Errorf("expected nil for codex provider with OPENAI_API_KEY set, got: %v", err)
	}
}

// TestCheckStartupCredentials_CodexProvider_OpenAIKeyMissing_ReturnsError
// verifies that checkStartupCredentials fails for a codex setup when OPENAI_API_KEY is unset.
func TestCheckStartupCredentials_CodexProvider_OpenAIKeyMissing_ReturnsError(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "codex")

	t.Setenv("OPENAI_API_KEY", "")

	err := checkStartupCredentials(home, cfgPath)
	if err == nil {
		t.Fatal("expected error when OPENAI_API_KEY is not set for codex provider, got nil")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY not set") {
		t.Errorf("error should mention OPENAI_API_KEY not set; got: %v", err)
	}
}

// TestCheckStartupCredentials_CodexProvider_ExpiredOAuth_ReturnsNil verifies
// that an expired Claude OAuth token does NOT block startup when the codex
// provider is configured (the OAuth check should be skipped entirely).
func TestCheckStartupCredentials_CodexProvider_ExpiredOAuth_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "codex")

	t.Setenv("OPENAI_API_KEY", "sk-openai-test-key")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Write an expired Claude OAuth credential — should be ignored for codex.
	writeOAuthCredentials(t, home, 1000)

	if err := checkStartupCredentials(home, cfgPath); err != nil {
		t.Errorf("expected nil: codex provider should skip OAuth check even when token is expired; got: %v", err)
	}
}

// TestCheckStartupCredentials_OAuthFreshToken_NoEnvKey_ReturnsNil verifies that
// when valid OAuth credentials exist, ANTHROPIC_API_KEY in ~/.cistern/env is not
// required — the token is injected automatically from ~/.claude/.credentials.json.
func TestCheckStartupCredentials_OAuthFreshToken_NoEnvKey_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	futureExpiry := time.Now().Add(24 * time.Hour).UnixMilli()
	writeOAuthCredentials(t, home, futureExpiry)

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil when OAuth credentials are fresh and no env key set, got: %v", err)
	}
	// Verify the OAuth token was injected into the process environment.
	if got := os.Getenv("ANTHROPIC_API_KEY"); got == "" {
		t.Error("expected ANTHROPIC_API_KEY to be injected from OAuth credentials, got empty")
	}
}

// TestCheckStartupCredentials_OAuthExpired_RefreshSucceeds_NoEnvKey_ReturnsNil
// verifies that an expired OAuth token is still injected (not refreshed) —
// we read it as-is and let the agent try it. Claude CLI handles its own refresh.
func TestCheckStartupCredentials_OAuthExpired_StillInjectsToken_ReturnsNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	writeOAuthCredentials(t, home, 1000) // expired

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil even for expired token, got: %v", err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "sk-ant-test-invalid" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want sk-ant-test-invalid (injected from credentials)", got)
	}
}

// TestCheckStartupCredentials_OAuthExpired_InjectsFromFile_NotFromEnv verifies
// that when credentials file has a (possibly expired) token, it takes precedence
// over whatever is in ANTHROPIC_API_KEY env — ensuring we always use the latest
// token the Claude CLI has written.
func TestCheckStartupCredentials_OAuthExpired_InjectsFromFile_NotFromEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-stale-env-key")
	writeOAuthCredentials(t, home, 1000) // expired token in credentials file

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
	// Token from credentials file should win over stale env var.
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "sk-ant-test-invalid" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want token from credentials file", got)
	}
}

// TestCheckStartupCredentials_OAuthExpired_NoRefreshToken_StillInjects verifies
// that an expired token with no refresh token is still injected. We don't refresh
// tokens — we just read and inject whatever Claude CLI last wrote.
func TestCheckStartupCredentials_OAuthExpired_NoRefreshToken_StillInjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")
	// Write credentials with expired token and no refresh token.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	creds := `{"claudeAiOauth":{"accessToken":"tok-expired","refreshToken":"","expiresAt":1000}}`
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	if err := checkStartupCredentials(home, filepath.Join(home, "nonexistent.yaml")); err != nil {
		t.Errorf("expected nil — expired token still injected, got: %v", err)
	}
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "tok-expired" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want tok-expired", got)
	}
}

// TestCheckStartupCredentials_OAuthCodexProvider_FreshToken_NotInjected verifies
// that OAuth credential injection only happens for the claude provider; a codex
// setup with fresh OAuth credentials does not inject ANTHROPIC_API_KEY.
func TestCheckStartupCredentials_OAuthCodexProvider_FreshToken_NotInjected(t *testing.T) {
	home := t.TempDir()
	cfgPath := writeMinimalConfig(t, home, "codex")
	t.Setenv("OPENAI_API_KEY", "sk-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "")
	futureExpiry := time.Now().Add(24 * time.Hour).UnixMilli()
	writeOAuthCredentials(t, home, futureExpiry)

	if err := checkStartupCredentials(home, cfgPath); err != nil {
		t.Errorf("expected nil for codex provider, got: %v", err)
	}
	// ANTHROPIC_API_KEY must remain unset — OAuth injection is claude-only.
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "" {
		t.Errorf("expected ANTHROPIC_API_KEY to remain unset for codex provider, got %q", got)
	}
}

// --- startupRequiredEnvVars tests ---

func TestStartupRequiredEnvVars_NoConfig_DefaultsToAnthropicKey(t *testing.T) {
	vars, usesClaude := startupRequiredEnvVars("")
	if len(vars) != 1 || vars[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("expected [ANTHROPIC_API_KEY], got %v", vars)
	}
	if !usesClaude {
		t.Error("expected usesClaude=true for default fallback")
	}
}

func TestStartupRequiredEnvVars_NonexistentConfig_DefaultsToAnthropicKey(t *testing.T) {
	vars, usesClaude := startupRequiredEnvVars(filepath.Join(t.TempDir(), "missing.yaml"))
	if len(vars) != 1 || vars[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("expected [ANTHROPIC_API_KEY], got %v", vars)
	}
	if !usesClaude {
		t.Error("expected usesClaude=true for default fallback")
	}
}

func TestStartupRequiredEnvVars_CodexConfig_ReturnsOpenAIKey(t *testing.T) {
	cfgPath := writeMinimalConfig(t, t.TempDir(), "codex")
	vars, usesClaude := startupRequiredEnvVars(cfgPath)
	if len(vars) != 1 || vars[0] != "OPENAI_API_KEY" {
		t.Errorf("expected [OPENAI_API_KEY], got %v", vars)
	}
	if usesClaude {
		t.Error("expected usesClaude=false for codex provider")
	}
}

func TestStartupRequiredEnvVars_GeminiConfig_ReturnsGeminiKey(t *testing.T) {
	cfgPath := writeMinimalConfig(t, t.TempDir(), "gemini")
	vars, usesClaude := startupRequiredEnvVars(cfgPath)
	if len(vars) != 1 || vars[0] != "GEMINI_API_KEY" {
		t.Errorf("expected [GEMINI_API_KEY], got %v", vars)
	}
	if usesClaude {
		t.Error("expected usesClaude=false for gemini provider")
	}
}

func TestStartupRequiredEnvVars_ClaudeConfig_ReturnsNoRequiredVars(t *testing.T) {
	// Claude authenticates via its own OAuth credentials file — no env vars required.
	cfgPath := writeMinimalConfig(t, t.TempDir(), "claude")
	vars, usesClaude := startupRequiredEnvVars(cfgPath)
	if len(vars) != 0 {
		t.Errorf("expected no required vars for claude, got %v", vars)
	}
	if !usesClaude {
		t.Error("expected usesClaude=true for claude provider")
	}
}

func TestStartupRequiredEnvVars_OpencodeConfig_ReturnsEmptyVarsNotClaude(t *testing.T) {
	// opencode has no EnvPassthrough — resolved repos with zero required vars
	// must NOT fall back to the claude default.
	cfgPath := writeMinimalConfig(t, t.TempDir(), "opencode")
	vars, usesClaude := startupRequiredEnvVars(cfgPath)
	if len(vars) != 0 {
		t.Errorf("expected no required vars for opencode, got %v", vars)
	}
	if usesClaude {
		t.Error("expected usesClaude=false for opencode provider")
	}
}

// --- repoQueueSummary tests ---

func TestRepoQueueSummary_NoItems_ReturnsZeroZero(t *testing.T) {
	got := repoQueueSummary("cistern", nil)
	want := "cistern: 0 queued, 0 flowing"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_EmptyList_ReturnsZeroZero(t *testing.T) {
	got := repoQueueSummary("cistern", []*cistern.Droplet{})
	want := "cistern: 0 queued, 0 flowing"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_OnlyOpenItems_CountsQueued(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "ct-aaa", Repo: "cistern", Status: "open"},
		{ID: "ct-bbb", Repo: "cistern", Status: "open"},
	}
	got := repoQueueSummary("cistern", items)
	want := "cistern: 2 queued, 0 flowing"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_OnlyFlowing_ShowsParenthetical(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "sc-abc123", Repo: "cistern", Status: "in_progress", Assignee: "julia", CurrentCataractae: "implement"},
	}
	got := repoQueueSummary("cistern", items)
	want := "cistern: 0 queued, 1 flowing (julia: sc-abc123/implement)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_MixedItems_ShowsBoth(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "ct-aaa", Repo: "cistern", Status: "open"},
		{ID: "ct-bbb", Repo: "cistern", Status: "open"},
		{ID: "sc-abc123", Repo: "cistern", Status: "in_progress", Assignee: "julia", CurrentCataractae: "implement"},
	}
	got := repoQueueSummary("cistern", items)
	want := "cistern: 2 queued, 1 flowing (julia: sc-abc123/implement)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_FiltersOtherRepos(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "ct-aaa", Repo: "other-repo", Status: "open"},
		{ID: "ct-bbb", Repo: "other-repo", Status: "in_progress", Assignee: "worker1", CurrentCataractae: "review"},
		{ID: "ct-ccc", Repo: "cistern", Status: "open"},
	}
	got := repoQueueSummary("cistern", items)
	want := "cistern: 1 queued, 0 flowing"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_MultipleFlowing_ListsAllCommaJoined(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "sc-aaa", Repo: "cistern", Status: "in_progress", Assignee: "julia", CurrentCataractae: "implement"},
		{ID: "sc-bbb", Repo: "cistern", Status: "in_progress", Assignee: "rex", CurrentCataractae: "review"},
	}
	got := repoQueueSummary("cistern", items)
	if !strings.Contains(got, "cistern: 0 queued, 2 flowing") {
		t.Errorf("expected count prefix; got %q", got)
	}
	if !strings.Contains(got, "julia: sc-aaa/implement") {
		t.Errorf("expected julia entry in parenthetical; got %q", got)
	}
	if !strings.Contains(got, "rex: sc-bbb/review") {
		t.Errorf("expected rex entry in parenthetical; got %q", got)
	}
}

func TestRepoQueueSummary_FlowingWithoutAssignee_ShowsIDAndCataractae(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "sc-xyz", Repo: "cistern", Status: "in_progress", Assignee: "", CurrentCataractae: "implement"},
	}
	got := repoQueueSummary("cistern", items)
	want := "cistern: 0 queued, 1 flowing (sc-xyz/implement)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_StagnantItems_NotCounted(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "ct-aaa", Repo: "cistern", Status: "stagnant"},
		{ID: "ct-bbb", Repo: "cistern", Status: "delivered"},
	}
	got := repoQueueSummary("cistern", items)
	want := "cistern: 0 queued, 0 flowing"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoQueueSummary_ZeroFlowing_NoParenthetical(t *testing.T) {
	items := []*cistern.Droplet{
		{ID: "ct-aaa", Repo: "cistern", Status: "open"},
	}
	got := repoQueueSummary("cistern", items)
	if strings.Contains(got, "(") {
		t.Errorf("expected no parenthetical when 0 flowing; got %q", got)
	}
}
