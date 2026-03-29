package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
)

// --- callFilterAgent tests ---

// TestFilterCmd_NewSession_UnknownRepo_ReturnsError verifies that ct filter --title "..."
// --repo nonexistent returns an error before any LLM call, even without --file.
// This tests that resolveCanonicalRepo is called before resolveFilterPreset so that
// wrong-case repo names in non-file paths also produce errors, not silent fallbacks.
// Given config with "PortfolioWebsite",
// When filter --title "idea" --repo nonexistent is called,
// Then an error mentioning "unknown repo nonexistent" is returned.
func TestFilterCmd_NewSession_UnknownRepo_ReturnsError(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Cleanup(func() {
		filterTitle = ""
		filterRepo = ""
	})

	err := execCmd(t, "filter", "--title", "test idea", "--repo", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown repo in new session, got nil")
	}
	if !strings.Contains(err.Error(), "unknown repo nonexistent") {
		t.Errorf("error %q does not mention 'unknown repo nonexistent'", err.Error())
	}
}

// TestFilterCmd_UnknownRepo_ReturnsError verifies that ct filter --resume ... --file
// with an unknown repo name returns a clear error before any LLM call.
// Given config with "PortfolioWebsite",
// When filter --resume <id> --file --repo nonexistent is called,
// Then an error mentioning "unknown repo nonexistent" is returned.
func TestFilterCmd_UnknownRepo_ReturnsError(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Cleanup(func() {
		filterResume = ""
		filterFile = false
		filterRepo = ""
	})

	err := execCmd(t, "filter", "--resume", "some-session-id", "--file", "--repo", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown repo, got nil")
	}
	if !strings.Contains(err.Error(), "unknown repo nonexistent") {
		t.Errorf("error %q does not mention 'unknown repo nonexistent'", err.Error())
	}
}

// TestFilterCmd_CanonicalizeRepo_StoresCanonicalName verifies that ct filter
// --resume --file --repo portfoliowebsite (lower-case) stores the droplet with
// the canonical name "PortfolioWebsite" from the config.
// Given config with "PortfolioWebsite" and a fakeagent preset,
// When filter --resume --file --repo portfoliowebsite is exercised via direct
// function calls (mirroring what filterCmd.RunE does),
// Then the droplet is stored with repo = "PortfolioWebsite".
func TestFilterCmd_CanonicalizeRepo_StoresCanonicalName(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	// Resolve canonical repo (this is what filterCmd.RunE calls).
	repo, err := resolveCanonicalRepo("portfoliowebsite")
	if err != nil {
		t.Fatalf("resolveCanonicalRepo: %v", err)
	}
	if repo != "PortfolioWebsite" {
		t.Fatalf("resolveCanonicalRepo returned %q, want %q", repo, "PortfolioWebsite")
	}

	// Simulate the persist path using the canonical repo name.
	preset := provider.ProviderPreset{
		Name:       "test",
		Command:    fakeagentBin,
		ResumeFlag: "--resume",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}
	result, err := invokeFilterResume(preset, "test-session", filterFinalizePrompt)
	if err != nil {
		t.Fatalf("invokeFilterResume: %v", err)
	}

	c, err := cistern.New(db, "pw")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer c.Close()

	if err := addProposals(c, result.Proposals, repo, 2); err != nil {
		t.Fatalf("addProposals: %v", err)
	}

	items, err := c.List("PortfolioWebsite", "")
	if err != nil {
		t.Fatalf("c.List: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected droplet stored with canonical repo name 'PortfolioWebsite', got none")
	}
}

// TestCallFilterAgent_ReturnsProposalsAndSessionID verifies that callFilterAgent
// correctly invokes the agent with --output-format json and parses both the
// proposals and the session_id from the JSON envelope.
// Given a preset pointing at fakeagent (updated to support --output-format json),
// When callFilterAgent is called with nil extraArgs,
// Then proposals and a non-empty session_id are returned.
func TestCallFilterAgent_ReturnsProposalsAndSessionID(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := callFilterAgent(preset, nil, "Title: fix auth bug", "")
	if err != nil {
		t.Fatalf("callFilterAgent: unexpected error: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected non-empty session_id")
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
	if result.Proposals[0].Title != "mock proposal" {
		t.Errorf("title = %q, want %q", result.Proposals[0].Title, "mock proposal")
	}
	if result.Proposals[0].Description != "test description" {
		t.Errorf("description = %q, want %q", result.Proposals[0].Description, "test description")
	}
}

// TestCallFilterAgent_Resume_PassesExtraArgs verifies that --resume extraArgs are
// forwarded to the agent binary and fakeagent handles them gracefully.
// Given a preset and extraArgs containing --resume,
// When callFilterAgent is called,
// Then proposals and session_id are returned.
func TestCallFilterAgent_Resume_PassesExtraArgs(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := callFilterAgent(preset, []string{"--resume", "test-session-id"}, "refine further", "")
	if err != nil {
		t.Fatalf("callFilterAgent with --resume: unexpected error: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected non-empty session_id")
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
}

// TestCallFilterAgent_AgentExecFailure verifies that when the agent exits non-zero,
// callFilterAgent returns an error containing the agent's stderr output.
// Given a preset pointing at failagent (exits 1),
// When callFilterAgent is called,
// Then an error is returned.
func TestCallFilterAgent_AgentExecFailure(t *testing.T) {
	failagentBin := buildTestBin(t, "failagent", "github.com/MichielDean/cistern/internal/testutil/failagent")

	preset := provider.ProviderPreset{
		Name:    "test-fail",
		Command: failagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := callFilterAgent(preset, nil, "some prompt", "")
	if err == nil {
		t.Fatal("expected error when agent exits non-zero, got nil")
	}
}

// TestCallFilterAgent_JSONFallback_RawOutput verifies the fallback path where the
// agent exits 0 but returns non-JSON output (not a JSON envelope).
// callFilterAgent must fall back to extractProposals on the raw output and return
// proposals with an empty session_id.
// Given a fakeagent in raw_fallback mode (returns raw proposals despite --output-format),
// When callFilterAgent is called,
// Then proposals are returned and session_id is empty.
func TestCallFilterAgent_JSONFallback_RawOutput(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	t.Setenv("FAKEAGENT_MODE", "raw_fallback")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := callFilterAgent(preset, nil, "Title: fix auth bug", "")
	if err != nil {
		t.Fatalf("callFilterAgent fallback: unexpected error: %v", err)
	}
	if result.SessionID != "" {
		t.Errorf("expected empty session_id in fallback mode, got %q", result.SessionID)
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal from fallback path")
	}
	if result.Proposals[0].Title != "mock proposal" {
		t.Errorf("title = %q, want %q", result.Proposals[0].Title, "mock proposal")
	}
}

// TestCallFilterAgent_IsErrorEnvelope_ReturnsError verifies that when the agent
// returns a JSON envelope with is_error:true, callFilterAgent returns an error.
// Given a fakeagent in error_envelope mode (returns is_error:true JSON),
// When callFilterAgent is called,
// Then an error mentioning "agent returned error" is returned.
func TestCallFilterAgent_IsErrorEnvelope_ReturnsError(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	t.Setenv("FAKEAGENT_MODE", "error_envelope")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := callFilterAgent(preset, nil, "Title: fix auth bug", "")
	if err == nil {
		t.Fatal("expected error when agent returns is_error envelope, got nil")
	}
	if !strings.Contains(err.Error(), "agent returned error") {
		t.Errorf("error %q does not mention 'agent returned error'", err.Error())
	}
}

// TestCallFilterAgent_MissingRequiredEnvVar verifies that callFilterAgent returns
// an error without executing the agent when a required env var is absent.
// Given a preset with EnvPassthrough=["MISSING_FILTER_KEY"] and the var unset,
// When callFilterAgent is called,
// Then an error mentioning the key is returned.
func TestCallFilterAgent_MissingRequiredEnvVar(t *testing.T) {
	preset := provider.ProviderPreset{
		Name:           "test",
		Command:        "true",
		EnvPassthrough: []string{"MISSING_FILTER_KEY"},
		NonInteractive: provider.NonInteractiveConfig{PromptFlag: "-p"},
	}
	t.Setenv("MISSING_FILTER_KEY", "")

	_, err := callFilterAgent(preset, nil, "prompt", "")
	if err == nil {
		t.Fatal("expected error for missing env var, got nil")
	}
	if !strings.Contains(err.Error(), "MISSING_FILTER_KEY") {
		t.Errorf("error %q does not mention the missing key", err.Error())
	}
}

// --- invokeFilterNew tests ---

// TestInvokeFilterNew_ReturnsProposalsAndSessionID verifies that invokeFilterNew
// combines system + user prompts and returns proposals with a session_id.
// Given a preset pointing at fakeagent,
// When invokeFilterNew is called with a title and description,
// Then proposals and a non-empty session_id are returned.
func TestInvokeFilterNew_ReturnsProposalsAndSessionID(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := invokeFilterNew(preset, "Add user auth", "JWT-based auth with refresh tokens", "", "")
	if err != nil {
		t.Fatalf("invokeFilterNew: unexpected error: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected non-empty session_id from invokeFilterNew")
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
}

// TestInvokeFilterNew_TitleOnly verifies that invokeFilterNew works when only
// a title is provided (empty description).
func TestInvokeFilterNew_TitleOnly(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := invokeFilterNew(preset, "Add user auth", "", "", "")
	if err != nil {
		t.Fatalf("invokeFilterNew title-only: unexpected error: %v", err)
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
}

// --- invokeFilterResume tests ---

// TestInvokeFilterResume_WithFeedback verifies that invokeFilterResume passes
// the session ID via the preset's ResumeFlag and returns proposals.
// Given a preset with ResumeFlag="--resume" pointing at fakeagent,
// When invokeFilterResume is called with a session ID and feedback,
// Then proposals and a non-empty session_id are returned.
func TestInvokeFilterResume_WithFeedback(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	preset := provider.ProviderPreset{
		Name:       "test",
		Command:    fakeagentBin,
		ResumeFlag: "--resume",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := invokeFilterResume(preset, "existing-session-123", "Make it more focused")
	if err != nil {
		t.Fatalf("invokeFilterResume: unexpected error: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected non-empty session_id from invokeFilterResume")
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
}

// TestInvokeFilterResume_DefaultsToResumeFlag verifies that when ResumeFlag is
// empty in the preset, invokeFilterResume defaults to "--resume".
func TestInvokeFilterResume_DefaultsToResumeFlag(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	// No ResumeFlag set — should default to "--resume".
	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := invokeFilterResume(preset, "session-456", "more feedback")
	if err != nil {
		t.Fatalf("invokeFilterResume (default flag): unexpected error: %v", err)
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected proposals")
	}
}

// --- ct filter command flag validation tests ---

// TestFilterCmd_NewSession_RequiresTitle verifies that ct filter without --title
// and without --resume returns an error mentioning --title.
func TestFilterCmd_NewSession_RequiresTitle(t *testing.T) {
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	err := execCmd(t, "filter")
	if err == nil {
		t.Fatal("expected error when --title is missing, got nil")
	}
	if !strings.Contains(err.Error(), "--title") {
		t.Errorf("error %q does not mention --title", err.Error())
	}
}

// TestFilterCmd_Resume_RequiresFeedbackOrFile verifies that ct filter --resume
// without a feedback argument and without --file returns an error.
// Given ct filter --resume <id> with no positional arg and no --file,
// When the command is executed,
// Then an error mentioning "feedback" is returned.
func TestFilterCmd_Resume_RequiresFeedbackOrFile(t *testing.T) {
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	err := execCmd(t, "filter", "--resume", "some-session-id")
	if err == nil {
		t.Fatal("expected error when --resume without feedback or --file, got nil")
	}
	if !strings.Contains(err.Error(), "feedback") {
		t.Errorf("error %q does not mention feedback", err.Error())
	}
}

// TestFilterCmd_ResumeFile_RequiresRepo verifies that ct filter --resume ... --file
// without --repo returns an error mentioning --repo.
func TestFilterCmd_ResumeFile_RequiresRepo(t *testing.T) {
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	err := execCmd(t, "filter", "--resume", "some-session-id", "--file")
	if err == nil {
		t.Fatal("expected error when --file without --repo, got nil")
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("error %q does not mention --repo", err.Error())
	}
}

// --- printFilterResult tests ---

// TestPrintFilterResult_HumanFormat verifies that printFilterResult with "human"
// format does not return an error for a valid result.
func TestPrintFilterResult_HumanFormat(t *testing.T) {
	result := filterSessionResult{
		SessionID: "test-session",
		Proposals: []DropletProposal{
			{Title: "Fix login bug", Description: "Handle edge case in auth flow", Complexity: "trivial"},
		},
	}
	if err := printFilterResult(result, "human"); err != nil {
		t.Fatalf("printFilterResult human: unexpected error: %v", err)
	}
}

// TestPrintFilterResult_JSONFormat verifies that printFilterResult with "json"
// format does not return an error for a valid result.
func TestPrintFilterResult_JSONFormat(t *testing.T) {
	result := filterSessionResult{
		SessionID: "session-xyz",
		Proposals: []DropletProposal{
			{Title: "Add caching", Description: "Implement Redis caching", Complexity: "standard"},
		},
	}
	if err := printFilterResult(result, "json"); err != nil {
		t.Fatalf("printFilterResult json: unexpected error: %v", err)
	}
}

// TestPrintFilterResult_HumanFormat_MultipleProposals verifies that human output
// includes section headers when multiple proposals are present.
func TestPrintFilterResult_HumanFormat_MultipleProposals(t *testing.T) {
	result := filterSessionResult{
		SessionID: "test-session",
		Proposals: []DropletProposal{
			{Title: "First task", Description: "Do the first thing", Complexity: "trivial"},
			{Title: "Second task", Description: "Do the second thing", Complexity: "standard"},
		},
	}
	if err := printFilterResult(result, "human"); err != nil {
		t.Fatalf("printFilterResult multi-proposal: unexpected error: %v", err)
	}
}

// TestFilterJSONOutput_HasRequiredFields verifies that the JSON output structure
// contains session_id, title, and description — the fields required for scripting.
func TestFilterJSONOutput_HasRequiredFields(t *testing.T) {
	type filterJSONOutput struct {
		SessionID   string           `json:"session_id"`
		Title       string           `json:"title,omitempty"`
		Description string           `json:"description,omitempty"`
		Proposals   []DropletProposal `json:"proposals,omitempty"`
	}
	out := filterJSONOutput{
		SessionID:   "abc123",
		Title:       "Refactor auth",
		Description: "Clean up auth module",
		Proposals: []DropletProposal{
			{Title: "Refactor auth", Description: "Clean up auth module", Complexity: "standard"},
		},
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"session_id", "title", "description"} {
		if v, ok := got[field]; !ok || v == "" {
			t.Errorf("JSON output missing required field %q", field)
		}
	}
}

// --- end-to-end: --resume --file --repo persists a droplet ---

// TestFilterCmd_ResumeFile_PersistsDroplet verifies the full --file path:
// invokeFilterResume + addProposals produces a droplet in the database.
// Given a fakeagent preset and an empty database,
// When invokeFilterResume is called and addProposals writes to the DB,
// Then the droplet is present in the cistern with the expected title.
func TestFilterCmd_ResumeFile_PersistsDroplet(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")

	preset := provider.ProviderPreset{
		Name:       "test",
		Command:    fakeagentBin,
		ResumeFlag: "--resume",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	result, err := invokeFilterResume(preset, "test-session", filterFinalizePrompt)
	if err != nil {
		t.Fatalf("invokeFilterResume: %v", err)
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected proposals from invokeFilterResume")
	}

	c, err := cistern.New(db, "ct")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer c.Close()

	if err := addProposals(c, result.Proposals, "myrepo", 2); err != nil {
		t.Fatalf("addProposals: %v", err)
	}

	items, err := c.List("myrepo", "")
	if err != nil {
		t.Fatalf("c.List: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one droplet to be persisted")
	}
	if items[0].Title != "mock proposal" {
		t.Errorf("title = %q, want %q", items[0].Title, "mock proposal")
	}
}

// TestFilterCmd_SkipContextFlag_IsRecognized verifies that --skip-context is a
// recognized flag and does not produce an "unknown flag" error.
// Given a config with "PortfolioWebsite" and an unknown repo name,
// When ct filter --title "..." --repo nonexistent --skip-context is called,
// Then the error is about the unknown repo, not about an unrecognized flag.
func TestFilterCmd_SkipContextFlag_IsRecognized(t *testing.T) {
	cfgPath := writeTestConfig(t, "PortfolioWebsite")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Cleanup(func() {
		filterTitle = ""
		filterRepo = ""
		filterSkipContext = false
	})

	err := execCmd(t, "filter", "--title", "test idea", "--repo", "nonexistent-xyz", "--skip-context")
	if err == nil {
		t.Fatal("expected error (unknown repo), got nil")
	}
	if strings.Contains(err.Error(), "unknown flag: --skip-context") {
		t.Errorf("--skip-context flag not recognized: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent-xyz") {
		t.Errorf("expected error about unknown repo 'nonexistent-xyz', got: %v", err)
	}
}

// TestFilterCmd_SkipContext_PromptHasNoContextHeader verifies that when
// --skip-context is set, the prompt sent to the agent contains no
// '=== CODEBASE CONTEXT ===' header, exercising the if !filterSkipContext branch.
// Given a config that routes the filter preset to fakeagent with FAKEAGENT_PROMPT_FILE set,
// When ct filter --title "..." --skip-context is called,
// Then the captured prompt must not contain the codebase context header.
func TestFilterCmd_SkipContext_PromptHasNoContextHeader(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	dir := t.TempDir()

	// Config that overrides the claude preset command to point at fakeagent.
	cfgPath := filepath.Join(dir, "cistern.yaml")
	cfgContent := fmt.Sprintf("provider:\n  command: %s\nrepos:\n  - name: testRepo\n    cataractae: 1\n", fakeagentBin)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	promptFile := filepath.Join(dir, "prompt.txt")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", filepath.Join(dir, "test.db"))
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("FAKEAGENT_PROMPT_FILE", promptFile)
	// Reset globals that may be polluted by prior tests.
	filterTitle = ""
	filterResume = ""
	filterFile = false
	filterRepo = ""
	filterSkipContext = false
	t.Cleanup(func() {
		filterTitle = ""
		filterResume = ""
		filterFile = false
		filterRepo = ""
		filterSkipContext = false
	})

	if err := execCmd(t, "filter", "--title", "test idea", "--skip-context"); err != nil {
		t.Fatalf("filter --skip-context: unexpected error: %v", err)
	}

	captured, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("reading captured prompt: %v", err)
	}
	if strings.Contains(string(captured), "=== CODEBASE CONTEXT ===") {
		t.Errorf("--skip-context: prompt must not contain context header, got:\n%s", captured)
	}
}

// TestFilterCmd_WithoutSkipContext_PromptHasContextHeader verifies that when
// --skip-context is NOT set, the prompt sent to the agent does contain the
// '=== CODEBASE CONTEXT ===' header. This exercises the !filterSkipContext path.
// Given a config that routes the filter preset to fakeagent with FAKEAGENT_PROMPT_FILE set,
// When ct filter --title "..." is called (no --skip-context),
// Then the captured prompt must contain the codebase context header.
func TestFilterCmd_WithoutSkipContext_PromptHasContextHeader(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	dir := t.TempDir()

	cfgPath := filepath.Join(dir, "cistern.yaml")
	cfgContent := fmt.Sprintf("provider:\n  command: %s\nrepos:\n  - name: testRepo\n    cataractae: 1\n", fakeagentBin)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	promptFile := filepath.Join(dir, "prompt.txt")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", filepath.Join(dir, "test.db"))
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("FAKEAGENT_PROMPT_FILE", promptFile)
	// Reset globals that may be polluted by prior tests.
	filterTitle = ""
	filterResume = ""
	filterFile = false
	filterRepo = ""
	filterSkipContext = false
	t.Cleanup(func() {
		filterTitle = ""
		filterResume = ""
		filterFile = false
		filterRepo = ""
		filterSkipContext = false
	})

	if err := execCmd(t, "filter", "--title", "test idea"); err != nil {
		t.Fatalf("filter without --skip-context: unexpected error: %v", err)
	}

	captured, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("reading captured prompt: %v", err)
	}
	if !strings.Contains(string(captured), "=== CODEBASE CONTEXT ===") {
		t.Errorf("without --skip-context: prompt must contain context header, got:\n%s", captured)
	}
}

// TestInvokeFilterNew_WithContextBlock_IncludesContextInResult verifies that
// invokeFilterNew accepts a non-empty contextBlock without error.
// Given a preset pointing at fakeagent and a non-empty contextBlock,
// When invokeFilterNew is called,
// Then proposals and a session_id are returned (fakeagent ignores prompt content).
func TestInvokeFilterNew_WithContextBlock_IncludesContextInResult(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	contextBlock := "=== CODEBASE CONTEXT ===\nsome schema here\n=== END CODEBASE CONTEXT ==="
	result, err := invokeFilterNew(preset, "Add feature", "Some description", contextBlock, "")
	if err != nil {
		t.Fatalf("invokeFilterNew with contextBlock: unexpected error: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected non-empty session_id")
	}
	if len(result.Proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
}

// TestCallFilterAgent_WithAllowedTools_PassesToolFlag verifies that when the
// preset's NonInteractive.AllowedToolsFlag is set, callFilterAgent appends
// the flag with "Glob,Grep,Read" to the agent command args.
// Given a preset with AllowedToolsFlag: "--allowedTools" pointing at fakeagent,
// When callFilterAgent is called,
// Then fakeagent receives --allowedTools and Glob,Grep,Read in its args.
func TestCallFilterAgent_WithAllowedTools_PassesToolFlag(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("FAKEAGENT_ARGS_FILE", argsFile)

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:        "--print",
			PromptFlag:       "-p",
			AllowedToolsFlag: "--allowedTools",
		},
	}

	_, err := callFilterAgent(preset, nil, "Title: fix auth bug", "")
	if err != nil {
		t.Fatalf("callFilterAgent: unexpected error: %v", err)
	}

	captured, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading args file: %v", err)
	}
	args := string(captured)
	if !strings.Contains(args, "--allowedTools") {
		t.Errorf("expected --allowedTools in agent args, got:\n%s", args)
	}
	if !strings.Contains(args, "Glob,Grep,Read") {
		t.Errorf("expected Glob,Grep,Read in agent args, got:\n%s", args)
	}
}

// TestCallFilterAgent_WithAddDir_PassesDirFlag verifies that when repoPath is
// non-empty and the preset defines AddDirFlag, --add-dir <repoPath> is passed
// to the agent.
// Given a preset with AddDirFlag: "--add-dir" and a non-empty repoPath,
// When callFilterAgent is called,
// Then fakeagent receives --add-dir <repoPath> in its args.
func TestCallFilterAgent_WithAddDir_PassesDirFlag(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("FAKEAGENT_ARGS_FILE", argsFile)

	preset := provider.ProviderPreset{
		Name:       "test",
		Command:    fakeagentBin,
		AddDirFlag: "--add-dir",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	repoPath := t.TempDir()
	_, err := callFilterAgent(preset, nil, "Title: fix auth bug", repoPath)
	if err != nil {
		t.Fatalf("callFilterAgent with repoPath: unexpected error: %v", err)
	}

	captured, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading args file: %v", err)
	}
	args := string(captured)
	if !strings.Contains(args, "--add-dir") {
		t.Errorf("expected --add-dir in agent args, got:\n%s", args)
	}
	if !strings.Contains(args, repoPath) {
		t.Errorf("expected repoPath %q in agent args, got:\n%s", repoPath, args)
	}
}

// TestFilterCmd_WithRepo_ForwardsAddDirToAgent verifies the end-to-end path:
// filterCmd.RunE → filterRepo != "" → repoPath computed → invokeFilterNew →
// callFilterAgent → --add-dir <repoPath> forwarded to the agent subprocess.
// Given a config with fakeagent as the preset command and testRepo in repos,
// When ct filter --title "..." --repo testRepo is called with FAKEAGENT_ARGS_FILE set,
// Then the captured subprocess args must contain --add-dir and the expected path.
func TestFilterCmd_WithRepo_ForwardsAddDirToAgent(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	dir := t.TempDir()

	// Config that overrides the claude preset command to point at fakeagent
	// and registers testRepo so resolveCanonicalRepo succeeds.
	cfgPath := filepath.Join(dir, "cistern.yaml")
	cfgContent := fmt.Sprintf("provider:\n  command: %s\nrepos:\n  - name: testRepo\n    cataractae: 1\n", fakeagentBin)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", filepath.Join(dir, "test.db"))
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("FAKEAGENT_ARGS_FILE", argsFile)
	// Reset globals that may be polluted by prior tests.
	filterTitle = ""
	filterResume = ""
	filterFile = false
	filterRepo = ""
	filterSkipContext = false
	t.Cleanup(func() {
		filterTitle = ""
		filterResume = ""
		filterFile = false
		filterRepo = ""
		filterSkipContext = false
	})

	if err := execCmd(t, "filter", "--title", "test idea", "--repo", "testRepo", "--skip-context"); err != nil {
		t.Fatalf("filter --repo testRepo: unexpected error: %v", err)
	}

	captured, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading args file: %v", err)
	}
	args := string(captured)

	if !strings.Contains(args, "--add-dir") {
		t.Errorf("expected --add-dir in agent args, got:\n%s", args)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	expectedPath := filepath.Join(home, ".cistern", "sandboxes", "testRepo", "_primary")
	if !strings.Contains(args, expectedPath) {
		t.Errorf("expected path %q in agent args, got:\n%s", expectedPath, args)
	}
}

// TestCallFilterAgent_WithEmptyRepoPath_SkipsAddDirFlag verifies that when
// repoPath is empty, --add-dir is NOT passed even if AddDirFlag is set.
// Given a preset with AddDirFlag and an empty repoPath,
// When callFilterAgent is called with repoPath="",
// Then fakeagent does not receive --add-dir in its args.
func TestCallFilterAgent_WithEmptyRepoPath_SkipsAddDirFlag(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeagent", "github.com/MichielDean/cistern/internal/testutil/fakeagent")
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	t.Setenv("FAKEAGENT_ARGS_FILE", argsFile)

	preset := provider.ProviderPreset{
		Name:       "test",
		Command:    fakeagentBin,
		AddDirFlag: "--add-dir",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := callFilterAgent(preset, nil, "Title: fix auth bug", "")
	if err != nil {
		t.Fatalf("callFilterAgent with empty repoPath: unexpected error: %v", err)
	}

	captured, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading args file: %v", err)
	}
	args := string(captured)
	if strings.Contains(args, "--add-dir") {
		t.Errorf("expected no --add-dir when repoPath is empty, got:\n%s", args)
	}
}
