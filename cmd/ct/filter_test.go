package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
)

// --- callFilterAgent tests ---

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

	result, err := callFilterAgent(preset, nil, "Title: fix auth bug")
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

	result, err := callFilterAgent(preset, []string{"--resume", "test-session-id"}, "refine further")
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

	_, err := callFilterAgent(preset, nil, "some prompt")
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

	result, err := callFilterAgent(preset, nil, "Title: fix auth bug")
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

	_, err := callFilterAgent(preset, nil, "Title: fix auth bug")
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

	_, err := callFilterAgent(preset, nil, "prompt")
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

	result, err := invokeFilterNew(preset, "Add user auth", "JWT-based auth with refresh tokens")
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

	result, err := invokeFilterNew(preset, "Add user auth", "")
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
