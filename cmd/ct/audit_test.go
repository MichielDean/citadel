package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
)

// --- extractFindings tests ---

// TestExtractFindings_ParsesValidJSONArray verifies that extractFindings correctly
// parses a well-formed JSON array of findings.
// Given a valid JSON array with one finding,
// When extractFindings is called,
// Then one AuditFinding with all fields populated is returned.
func TestExtractFindings_ParsesValidJSONArray(t *testing.T) {
	input := `[{"title":"SQL injection","severity":"blocking","file":"db.go","line":10,"attack_vector":"user input in query","remediation":"use parameterized queries"}]`
	findings, err := extractFindings(input)
	if err != nil {
		t.Fatalf("extractFindings: unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Title != "SQL injection" {
		t.Errorf("title = %q, want %q", f.Title, "SQL injection")
	}
	if f.Severity != "blocking" {
		t.Errorf("severity = %q, want %q", f.Severity, "blocking")
	}
	if f.File != "db.go" {
		t.Errorf("file = %q, want %q", f.File, "db.go")
	}
	if f.Line != 10 {
		t.Errorf("line = %d, want %d", f.Line, 10)
	}
	if f.AttackVector != "user input in query" {
		t.Errorf("attack_vector = %q, want %q", f.AttackVector, "user input in query")
	}
	if f.Remediation != "use parameterized queries" {
		t.Errorf("remediation = %q, want %q", f.Remediation, "use parameterized queries")
	}
}

// TestExtractFindings_ReturnsEmptySliceForEmptyArray verifies that extractFindings
// returns an empty slice (not an error) when the agent output is an empty array.
// Given an empty JSON array "[]",
// When extractFindings is called,
// Then an empty slice and no error are returned.
func TestExtractFindings_ReturnsEmptySliceForEmptyArray(t *testing.T) {
	findings, err := extractFindings("[]")
	if err != nil {
		t.Fatalf("extractFindings([]): unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected empty slice, got %d findings", len(findings))
	}
}

// TestExtractFindings_ParsesMultipleFindings verifies that extractFindings handles
// an array with multiple findings.
// Given a JSON array with two findings,
// When extractFindings is called,
// Then two AuditFinding values are returned.
func TestExtractFindings_ParsesMultipleFindings(t *testing.T) {
	input := `[
		{"title":"Finding A","severity":"blocking","file":"a.go","line":1,"attack_vector":"av1","remediation":"r1"},
		{"title":"Finding B","severity":"suggestion","file":"b.go","line":2,"attack_vector":"av2","remediation":"r2"}
	]`
	findings, err := extractFindings(input)
	if err != nil {
		t.Fatalf("extractFindings: unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if findings[0].Title != "Finding A" {
		t.Errorf("findings[0].Title = %q, want %q", findings[0].Title, "Finding A")
	}
	if findings[1].Title != "Finding B" {
		t.Errorf("findings[1].Title = %q, want %q", findings[1].Title, "Finding B")
	}
}

// TestExtractFindings_StripsMarkdownCodeFences verifies that extractFindings
// handles JSON wrapped in markdown code fences.
// Given JSON wrapped in ```json ... ```,
// When extractFindings is called,
// Then the findings are parsed correctly.
func TestExtractFindings_StripsMarkdownCodeFences(t *testing.T) {
	input := "```json\n[{\"title\":\"XSS\",\"severity\":\"required\",\"file\":\"handler.go\",\"line\":5,\"attack_vector\":\"user input in template\",\"remediation\":\"escape output\"}]\n```"
	findings, err := extractFindings(input)
	if err != nil {
		t.Fatalf("extractFindings with markdown fences: unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Title != "XSS" {
		t.Errorf("title = %q, want %q", findings[0].Title, "XSS")
	}
}

// TestExtractFindings_HandlesProsePrefix verifies that extractFindings finds the
// JSON array even when there is leading prose text.
// Given agent output with text before the JSON array,
// When extractFindings is called,
// Then findings are correctly extracted.
func TestExtractFindings_HandlesProsePrefix(t *testing.T) {
	input := "Here are the findings I discovered:\n[{\"title\":\"Hardcoded secret\",\"severity\":\"blocking\",\"file\":\"config.go\",\"line\":3,\"attack_vector\":\"leaked credentials\",\"remediation\":\"use env vars\"}]"
	findings, err := extractFindings(input)
	if err != nil {
		t.Fatalf("extractFindings with prose prefix: unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Title != "Hardcoded secret" {
		t.Errorf("title = %q, want %q", findings[0].Title, "Hardcoded secret")
	}
}

// TestExtractFindings_ReturnsErrorWhenNoArray verifies that extractFindings returns
// an error when the input contains no JSON array.
// Given a string with no '[' character,
// When extractFindings is called,
// Then an error is returned.
func TestExtractFindings_ReturnsErrorWhenNoArray(t *testing.T) {
	_, err := extractFindings("no JSON here")
	if err == nil {
		t.Fatal("expected error for input with no JSON array, got nil")
	}
}

// TestExtractFindings_ReturnsErrorForMalformedJSON verifies that extractFindings
// returns an error when the JSON array is malformed.
// Given a malformed JSON array,
// When extractFindings is called,
// Then an error is returned.
func TestExtractFindings_ReturnsErrorForMalformedJSON(t *testing.T) {
	_, err := extractFindings(`[{"title":"broken"`)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestExtractFindings_HandlesUnbalancedClosingBracketInString verifies that
// extractFindings correctly parses JSON when a string field value contains an
// unbalanced ']' character (no preceding '[' in that string).
// The old bracket-depth parser would treat this ']' as closing the array,
// truncating the JSON and causing a parse error.
// Given a finding whose remediation text contains ']' without a preceding '[',
// When extractFindings is called,
// Then the finding is parsed correctly.
func TestExtractFindings_HandlesUnbalancedClosingBracketInString(t *testing.T) {
	input := `[{"title":"Unchecked return","severity":"required","file":"util.go","line":7,"attack_vector":"ignored error","remediation":"Check ] and handle errors"}]`
	findings, err := extractFindings(input)
	if err != nil {
		t.Fatalf("extractFindings: unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	want := "Check ] and handle errors"
	if findings[0].Remediation != want {
		t.Errorf("remediation = %q, want %q", findings[0].Remediation, want)
	}
}

// --- auditFindingDescription tests ---

// TestAuditFindingDescription_ContainsAllFields verifies that auditFindingDescription
// includes severity, location, attack vector, and remediation.
// Given a fully-populated AuditFinding,
// When auditFindingDescription is called,
// Then the output contains all four fields.
func TestAuditFindingDescription_ContainsAllFields(t *testing.T) {
	f := AuditFinding{
		Title:        "SQL injection",
		Severity:     "blocking",
		File:         "internal/db/query.go",
		Line:         58,
		AttackVector: "unsanitized sort parameter concatenated into SQL",
		Remediation:  "use a whitelist for sort column names",
	}
	desc := auditFindingDescription(f)
	if !strings.Contains(desc, "blocking") {
		t.Error("description does not contain severity 'blocking'")
	}
	if !strings.Contains(desc, "internal/db/query.go:58") {
		t.Error("description does not contain location with line number")
	}
	if !strings.Contains(desc, "unsanitized sort parameter") {
		t.Error("description does not contain attack vector")
	}
	if !strings.Contains(desc, "whitelist") {
		t.Error("description does not contain remediation")
	}
}

// TestAuditFindingDescription_OmitsLineWhenZero verifies that when Line is 0,
// the location does not include a line number.
// Given an AuditFinding with Line=0,
// When auditFindingDescription is called,
// Then the location entry shows file without ":0".
func TestAuditFindingDescription_OmitsLineWhenZero(t *testing.T) {
	f := AuditFinding{
		Severity:     "required",
		File:         "config.go",
		Line:         0,
		AttackVector: "vector",
		Remediation:  "fix",
	}
	desc := auditFindingDescription(f)
	if strings.Contains(desc, ":0") {
		t.Error("description should not contain ':0' when line is zero")
	}
	if !strings.Contains(desc, "config.go") {
		t.Error("description should still contain the file name")
	}
}

// TestAuditFindingDescription_HandlesEmptyOptionalFields verifies that
// auditFindingDescription works correctly when file and optional fields are empty.
// Given an AuditFinding with only Severity set,
// When auditFindingDescription is called,
// Then the output contains severity and no extra empty lines.
func TestAuditFindingDescription_HandlesEmptyOptionalFields(t *testing.T) {
	f := AuditFinding{Severity: "suggestion"}
	desc := auditFindingDescription(f)
	if !strings.Contains(desc, "suggestion") {
		t.Error("description should contain severity")
	}
	// Should not have empty Location/Attack vector/Remediation lines
	if strings.Contains(desc, "Location:") {
		t.Error("description should not contain 'Location:' when file is empty")
	}
}

// --- auditSystemPrompt tests ---

// TestAuditSystemPrompt_ContainsRequiredSecurityDomains verifies that
// auditSystemPrompt covers the key security vulnerability domains.
// When the audit system prompt is inspected,
// Then it contains references to auth, injection, secrets, and data exposure.
func TestAuditSystemPrompt_ContainsRequiredSecurityDomains(t *testing.T) {
	domains := []string{
		"Authentication",
		"Injection",
		"Secrets",
		"Data Exposure",
		"blocking",
		"required",
		"suggestion",
	}
	for _, domain := range domains {
		if !strings.Contains(auditSystemPrompt, domain) {
			t.Errorf("auditSystemPrompt does not mention %q", domain)
		}
	}
}

// TestAuditSystemPrompt_SpecifiesJSONOutputFormat verifies that the system prompt
// instructs the agent to output a JSON array.
// When the audit system prompt is inspected,
// Then it explicitly requests JSON array output.
func TestAuditSystemPrompt_SpecifiesJSONOutputFormat(t *testing.T) {
	if !strings.Contains(auditSystemPrompt, "JSON array") {
		t.Error("auditSystemPrompt should specify JSON array output format")
	}
}

// TestAuditSystemPrompt_SpecifiesRequiredFields verifies that the system prompt
// lists all required finding fields.
// When the audit system prompt is inspected,
// Then it defines title, severity, file, line, attack_vector, and remediation.
func TestAuditSystemPrompt_SpecifiesRequiredFields(t *testing.T) {
	fields := []string{"title", "severity", "file", "line", "attack_vector", "remediation"}
	for _, field := range fields {
		if !strings.Contains(auditSystemPrompt, field) {
			t.Errorf("auditSystemPrompt does not define required field %q", field)
		}
	}
}

// --- invokeAuditAgent tests ---

// TestInvokeAuditAgent_ReturnsFindings verifies that invokeAuditAgent correctly
// invokes the agent binary and parses the findings from the JSON envelope.
// Given a preset pointing at fakeauditagent,
// When invokeAuditAgent is called with a valid repo path,
// Then a non-empty findings slice is returned.
func TestInvokeAuditAgent_ReturnsFindings(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	repoPath := t.TempDir()

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	findings, err := invokeAuditAgent(preset, repoPath, "")
	if err != nil {
		t.Fatalf("invokeAuditAgent: unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding")
	}
	if findings[0].Title != "SQL injection in query builder" {
		t.Errorf("title = %q, want %q", findings[0].Title, "SQL injection in query builder")
	}
	if findings[0].Severity != "blocking" {
		t.Errorf("severity = %q, want %q", findings[0].Severity, "blocking")
	}
}

// TestInvokeAuditAgent_ReturnsEmptySliceWhenNoFindings verifies that invokeAuditAgent
// returns an empty slice (not an error) when the agent reports no findings.
// Given fakeauditagent in empty mode,
// When invokeAuditAgent is called,
// Then an empty slice and no error are returned.
func TestInvokeAuditAgent_ReturnsEmptySliceWhenNoFindings(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	t.Setenv("FAKEAUDITAGENT_MODE", "empty")
	repoPath := t.TempDir()

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	findings, err := invokeAuditAgent(preset, repoPath, "")
	if err != nil {
		t.Fatalf("invokeAuditAgent empty: unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected empty findings, got %d", len(findings))
	}
}

// TestInvokeAuditAgent_PassesModelFlag verifies that when a model is specified
// and the preset has a ModelFlag, the model flag and value appear in the args
// sent to the agent binary.
// Given a preset with ModelFlag="--model" and model="test-model",
// When invokeAuditAgent is called,
// Then the agent binary receives --model test-model in its arguments.
func TestInvokeAuditAgent_PassesModelFlag(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	repoPath := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("FAKEAUDITAGENT_ARGS_FILE", argsFile)

	preset := provider.ProviderPreset{
		Name:      "test",
		Command:   fakeagentBin,
		ModelFlag: "--model",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := invokeAuditAgent(preset, repoPath, "test-model")
	if err != nil {
		t.Fatalf("invokeAuditAgent: %v", err)
	}

	argsData, readErr := os.ReadFile(argsFile)
	if readErr != nil {
		t.Fatalf("read args file: %v", readErr)
	}
	argsStr := string(argsData)
	if !strings.Contains(argsStr, "--model") {
		t.Error("expected --model flag in agent args")
	}
	if !strings.Contains(argsStr, "test-model") {
		t.Error("expected model value 'test-model' in agent args")
	}
}

// TestInvokeAuditAgent_PassesAddDirFlag verifies that when the preset has an
// AddDirFlag and repoPath is non-empty, --add-dir repoPath appears in the args.
// Given a preset with AddDirFlag="--add-dir",
// When invokeAuditAgent is called with a repoPath,
// Then --add-dir appears in the agent args.
func TestInvokeAuditAgent_PassesAddDirFlag(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	repoPath := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("FAKEAUDITAGENT_ARGS_FILE", argsFile)

	preset := provider.ProviderPreset{
		Name:       "test",
		Command:    fakeagentBin,
		AddDirFlag: "--add-dir",
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:        "--print",
			PromptFlag:       "-p",
			AllowedToolsFlag: "--allowedTools",
		},
	}

	_, err := invokeAuditAgent(preset, repoPath, "")
	if err != nil {
		t.Fatalf("invokeAuditAgent: %v", err)
	}

	argsData, readErr := os.ReadFile(argsFile)
	if readErr != nil {
		t.Fatalf("read args file: %v", readErr)
	}
	argsStr := string(argsData)
	if !strings.Contains(argsStr, "--add-dir") {
		t.Error("expected --add-dir flag in agent args")
	}
	if !strings.Contains(argsStr, repoPath) {
		t.Errorf("expected repoPath %q in agent args", repoPath)
	}
	if !strings.Contains(argsStr, "--allowedTools") {
		t.Error("expected --allowedTools flag in agent args")
	}
	if !strings.Contains(argsStr, "Glob,Grep,Read") {
		t.Error("expected --allowedTools value 'Glob,Grep,Read' in agent args — write tools must not be granted")
	}
}

// TestInvokeAuditAgent_ErrorEnvelope_ReturnsError verifies that invokeAuditAgent
// returns an error when the agent envelope has is_error=true.
// Given fakeauditagent in error_envelope mode,
// When invokeAuditAgent is called,
// Then an error is returned.
func TestInvokeAuditAgent_ErrorEnvelope_ReturnsError(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	t.Setenv("FAKEAUDITAGENT_MODE", "error_envelope")
	repoPath := t.TempDir()

	preset := provider.ProviderPreset{
		Name:    "test",
		Command: fakeagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := invokeAuditAgent(preset, repoPath, "")
	if err == nil {
		t.Fatal("expected error for error envelope, got nil")
	}
	if !strings.Contains(err.Error(), "audit agent returned error") {
		t.Errorf("error %q does not mention 'audit agent returned error'", err.Error())
	}
}

// TestInvokeAuditAgent_MissingEnvVar_ReturnsError verifies that invokeAuditAgent
// returns an error when a required env var from EnvPassthrough is not set.
// Given a preset with EnvPassthrough=["REQUIRED_KEY"] and that env var unset,
// When invokeAuditAgent is called,
// Then an error mentioning the missing variable is returned.
func TestInvokeAuditAgent_MissingEnvVar_ReturnsError(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	t.Setenv("REQUIRED_AUDIT_KEY", "")
	os.Unsetenv("REQUIRED_AUDIT_KEY")

	preset := provider.ProviderPreset{
		Name:           "test",
		Command:        fakeagentBin,
		EnvPassthrough: []string{"REQUIRED_AUDIT_KEY"},
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := invokeAuditAgent(preset, t.TempDir(), "")
	if err == nil {
		t.Fatal("expected error for missing env var, got nil")
	}
	if !strings.Contains(err.Error(), "REQUIRED_AUDIT_KEY") {
		t.Errorf("error %q does not mention missing env var", err.Error())
	}
}

// TestInvokeAuditAgent_AgentExitFailure verifies that invokeAuditAgent returns a
// descriptive error when the agent binary exits with a non-zero status code.
// Given a preset pointing at failagent (always exits 1 with a known stderr message),
// When invokeAuditAgent is called,
// Then an error is returned containing the exit code and the stderr text.
func TestInvokeAuditAgent_AgentExitFailure(t *testing.T) {
	failagentBin := buildTestBin(t, "failagent", "github.com/MichielDean/cistern/internal/testutil/failagent")
	repoPath := t.TempDir()

	preset := provider.ProviderPreset{
		Name:    "test-fail",
		Command: failagentBin,
		NonInteractive: provider.NonInteractiveConfig{
			PrintFlag:  "--print",
			PromptFlag: "-p",
		},
	}

	_, err := invokeAuditAgent(preset, repoPath, "")
	if err == nil {
		t.Fatal("expected error when agent exits non-zero, got nil")
	}
	if !strings.Contains(err.Error(), "exit 1") && !strings.Contains(err.Error(), "agent crashed") {
		t.Errorf("error %q does not mention exit code or stderr text", err.Error())
	}
}

// --- auditRunCmd flag tests ---

// TestAuditRunCmd_MissingRepo_ReturnsError verifies that ct audit run without
// --repo returns a clear error.
// Given no --repo flag,
// When ct audit run is called,
// Then an error mentioning '--repo is required' is returned.
func TestAuditRunCmd_MissingRepo_ReturnsError(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Cleanup(func() {
		auditRunRepo = ""
		auditRunDryRun = false
		auditRunModel = ""
		auditRunPriority = 1
	})

	err := execCmd(t, "audit", "run")
	if err == nil {
		t.Fatal("expected error when --repo is missing, got nil")
	}
	if !strings.Contains(err.Error(), "--repo is required") {
		t.Errorf("error %q does not mention '--repo is required'", err.Error())
	}
}

// TestAuditRunCmd_UnknownRepo_ReturnsError verifies that ct audit run --repo nonexistent
// returns an error when the repo is not in the config.
// Given config with "MyRepo" and --repo nonexistent,
// When ct audit run is called,
// Then an error mentioning the unknown repo is returned.
func TestAuditRunCmd_UnknownRepo_ReturnsError(t *testing.T) {
	cfgPath := writeTestConfig(t, "MyRepo")
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Cleanup(func() {
		auditRunRepo = ""
		auditRunDryRun = false
		auditRunModel = ""
		auditRunPriority = 1
	})

	err := execCmd(t, "audit", "run", "--repo", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown repo, got nil")
	}
	if !strings.Contains(err.Error(), "unknown repo nonexistent") {
		t.Errorf("error %q does not mention 'unknown repo nonexistent'", err.Error())
	}
}

// TestAuditRunCmd_WorktreeNotFound verifies that ct audit run returns a clear error
// when the repository worktree does not exist on disk.
// Given a config with "TestRepo" and HOME set to a temp dir without the _primary dir,
// When ct audit run --repo TestRepo is executed,
// Then an error containing 'worktree not found' is returned.
func TestAuditRunCmd_WorktreeNotFound(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	// HOME points at a temp dir that has no .cistern/sandboxes/TestRepo/_primary
	home := t.TempDir()
	db := filepath.Join(t.TempDir(), "test.db")
	cfgPath := writeTestConfigWithAgent(t, "TestRepo", fakeagentBin)
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		auditRunRepo = ""
		auditRunDryRun = false
		auditRunModel = ""
		auditRunPriority = 1
	})

	err := execCmd(t, "audit", "run", "--repo", "TestRepo")
	if err == nil {
		t.Fatal("expected error when worktree does not exist, got nil")
	}
	if !strings.Contains(err.Error(), "worktree not found") {
		t.Errorf("error %q does not mention 'worktree not found'", err.Error())
	}
}

// --- printAuditFindings tests ---

// TestPrintAuditFindings_PrintsAllFindings verifies that printAuditFindings outputs
// each finding with its severity, location, attack vector, and remediation.
// Given two findings,
// When printAuditFindings is called,
// Then both findings are present in stdout.
func TestPrintAuditFindings_PrintsAllFindings(t *testing.T) {
	findings := []AuditFinding{
		{Title: "Finding One", Severity: "blocking", File: "main.go", Line: 5, AttackVector: "vector A", Remediation: "fix A"},
		{Title: "Finding Two", Severity: "required", File: "util.go", Line: 12, AttackVector: "vector B", Remediation: "fix B"},
	}

	out := captureStdout(t, func() {
		_ = printAuditFindings(findings)
	})

	if !strings.Contains(out, "Finding One") {
		t.Error("output missing 'Finding One'")
	}
	if !strings.Contains(out, "blocking") {
		t.Error("output missing severity 'blocking'")
	}
	if !strings.Contains(out, "Finding Two") {
		t.Error("output missing 'Finding Two'")
	}
	if !strings.Contains(out, "dry run") {
		t.Error("output should mention 'dry run'")
	}
}

// --- end-to-end audit run with fakeauditagent ---

// TestAuditRunCmd_DryRun_PrintsFindingsWithoutFiling verifies the end-to-end
// flow for ct audit run --dry-run: findings are printed but no droplets are filed.
// Given fakeauditagent and a valid repo config with matching worktree dir,
// When ct audit run --repo <repo> --dry-run is executed,
// Then findings are printed and no droplets exist in the database.
func TestAuditRunCmd_DryRun_PrintsFindingsWithoutFiling(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")

	// Create a fake repo worktree directory so Stat() passes.
	home := t.TempDir()
	repoWorktree := filepath.Join(home, ".cistern", "sandboxes", "TestRepo", "_primary")
	if err := os.MkdirAll(repoWorktree, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	db := filepath.Join(t.TempDir(), "test.db")
	cfgPath := writeTestConfigWithAgent(t, "TestRepo", fakeagentBin)
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	// Override UserHomeDir by overriding the worktree directly in invokeAuditAgent
	// via a custom HOME so filepath.Join(home, ".cistern",...) resolves correctly.
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		auditRunRepo = ""
		auditRunDryRun = false
		auditRunModel = ""
		auditRunPriority = 1
	})

	out := captureStdout(t, func() {
		err := execCmd(t, "audit", "run", "--repo", "TestRepo", "--dry-run")
		if err != nil {
			t.Errorf("audit run dry-run: unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "dry run") {
		t.Errorf("expected 'dry run' in output, got: %s", out)
	}
	if !strings.Contains(out, "SQL injection in query builder") {
		t.Errorf("expected finding title in output, got: %s", out)
	}

	// Verify nothing was filed.
	c, err := cistern.New(db, "")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer c.Close()
	items, err := c.List("TestRepo", "")
	if err != nil {
		t.Fatalf("c.List: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected no droplets filed with --dry-run, got %d", len(items))
	}
}

// TestAuditRunCmd_FilesDropletsForFindings verifies the end-to-end flow for
// ct audit run without --dry-run: each finding becomes a droplet.
// Given fakeauditagent returning one finding and a valid repo config,
// When ct audit run --repo <repo> is executed,
// Then one droplet is filed in the database with the finding title and description.
func TestAuditRunCmd_FilesDropletsForFindings(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")

	home := t.TempDir()
	repoWorktree := filepath.Join(home, ".cistern", "sandboxes", "TestRepo", "_primary")
	if err := os.MkdirAll(repoWorktree, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	db := filepath.Join(t.TempDir(), "test.db")
	cfgPath := writeTestConfigWithAgent(t, "TestRepo", fakeagentBin)
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		auditRunRepo = ""
		auditRunDryRun = false
		auditRunModel = ""
		auditRunPriority = 1
	})

	err := execCmd(t, "audit", "run", "--repo", "TestRepo")
	if err != nil {
		t.Fatalf("audit run: unexpected error: %v", err)
	}

	c, err := cistern.New(db, "")
	if err != nil {
		t.Fatalf("cistern.New: %v", err)
	}
	defer c.Close()
	items, err := c.List("TestRepo", "")
	if err != nil {
		t.Fatalf("c.List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 droplet filed, got %d", len(items))
	}
	item := items[0]
	if item.Title != "SQL injection in query builder" {
		t.Errorf("droplet title = %q, want %q", item.Title, "SQL injection in query builder")
	}
	if !strings.Contains(item.Description, "blocking") {
		t.Error("droplet description should contain severity 'blocking'")
	}
	if !strings.Contains(item.Description, "internal/db/query.go") {
		t.Error("droplet description should contain the file path")
	}
}

// TestAuditRunCmd_MultipleFindings_SummaryShowsCorrectSeverityPerFinding verifies
// that each line in the "Audit complete" summary shows the severity belonging to
// that specific finding, not an adjacent one. This exercises the index-alignment
// fix in runAuditRun (filed[] and findings[] were previously indexed together,
// which diverges when any c.Add call fails).
// Given fakeauditagent in "multi" mode returning 3 findings with distinct severities,
// When ct audit run completes,
// Then the summary contains blocking, required, and suggestion exactly once each.
func TestAuditRunCmd_MultipleFindings_SummaryShowsCorrectSeverityPerFinding(t *testing.T) {
	fakeagentBin := buildTestBin(t, "fakeauditagent", "github.com/MichielDean/cistern/internal/testutil/fakeauditagent")
	t.Setenv("FAKEAUDITAGENT_MODE", "multi")

	home := t.TempDir()
	repoWorktree := filepath.Join(home, ".cistern", "sandboxes", "TestRepo", "_primary")
	if err := os.MkdirAll(repoWorktree, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	db := filepath.Join(t.TempDir(), "test.db")
	cfgPath := writeTestConfigWithAgent(t, "TestRepo", fakeagentBin)
	t.Setenv("CT_CONFIG", cfgPath)
	t.Setenv("CT_DB", db)
	t.Setenv("CT_NO_ASCII_LOGO", "1")
	t.Setenv("HOME", home)
	t.Cleanup(func() {
		auditRunRepo = ""
		auditRunDryRun = false
		auditRunModel = ""
		auditRunPriority = 1
	})

	out := captureStdout(t, func() {
		err := execCmd(t, "audit", "run", "--repo", "TestRepo")
		if err != nil {
			t.Errorf("audit run: unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "Filed 3 finding(s)") {
		t.Errorf("expected 'Filed 3 finding(s)' in output, got: %s", out)
	}
	for _, sev := range []string{"blocking", "required", "suggestion"} {
		if !strings.Contains(out, "["+sev+"]") {
			t.Errorf("summary missing [%s]: %s", sev, out)
		}
	}
}

// writeTestConfigWithAgent writes a cistern.yaml that overrides the agent command
// for the given repo via a per-repo provider block. This causes resolveFilterPreset
// to use agentCmd instead of the real claude binary.
func writeTestConfigWithAgent(t *testing.T, repoName, agentCmd string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cistern.yaml")
	// Use a per-repo provider override so only Command is changed; all other
	// fields (NonInteractive, AddDirFlag, ModelFlag) inherit from the built-in
	// "claude" preset.
	content := "repos:\n" +
		"  - name: " + repoName + "\n" +
		"    cataractae: 1\n" +
		"    provider:\n" +
		"      command: " + agentCmd + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeTestConfigWithAgent: %v", err)
	}
	return path
}
