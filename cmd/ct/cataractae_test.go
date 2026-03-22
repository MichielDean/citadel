package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/aqueduct"
)

// testWorkflowYAML is a minimal valid workflow with one agent step.
const testWorkflowYAML = `name: test
cataractae:
  - name: implement
    type: agent
    identity: tester
    on_pass: done
`

// testWorkflowNoIdentityYAML is a valid workflow with no agent identities.
const testWorkflowNoIdentityYAML = `name: test
cataractae:
  - name: gate
    type: gate
    on_pass: done
`

// testWorkflowMultiIdentityYAML has two steps sharing the same identity (dedup test).
const testWorkflowMultiIdentityYAML = `name: test
cataractae:
  - name: step1
    type: agent
    identity: alpha
    on_pass: step2
  - name: step2
    type: agent
    identity: beta
    on_pass: done
`

// setWorkflow sets the global cataractaeGenerateWorkflow and restores it after the test.
func setWorkflow(t *testing.T, path string) {
	t.Helper()
	old := cataractaeGenerateWorkflow
	cataractaeGenerateWorkflow = path
	t.Cleanup(func() { cataractaeGenerateWorkflow = old })
}

// makeWorkflowDir creates <tmpDir>/aqueduct/workflow.yaml with the given content.
// Returns tmpDir and the workflow path.
func makeWorkflowDir(t *testing.T, content string) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	aqDir := filepath.Join(tmpDir, "aqueduct")
	if err := os.MkdirAll(aqDir, 0o755); err != nil {
		t.Fatalf("mkdir aqueduct: %v", err)
	}
	wfPath := filepath.Join(aqDir, "workflow.yaml")
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return tmpDir, wfPath
}

// makeCataractaeDir creates a cataractae/<name>/ dir with PERSONA.md and INSTRUCTIONS.md.
func makeCataractaeDir(t *testing.T, tmpDir, name string) {
	t.Helper()
	dir := filepath.Join(tmpDir, "cataractae", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir cataractae/%s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PERSONA.md"), []byte("# Role: "+aqueduct.TitleCaseName(name)+"\n\nA role."), 0o644); err != nil {
		t.Fatalf("write PERSONA.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "INSTRUCTIONS.md"), []byte(`Do work. ct droplet pass <id> --notes "done"`), 0o644); err != nil {
		t.Fatalf("write INSTRUCTIONS.md: %v", err)
	}
}

// replaceStdin temporarily replaces os.Stdin with a pipe containing the given input.
func replaceStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		r.Close()
	})
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()
}

// --- cisternCataractaeDir ---

func TestCisternCataractaeDir_DerivedFromWorkflowPath(t *testing.T) {
	wfPath := "/home/user/.cistern/aqueduct/workflow.yaml"
	got := cisternCataractaeDir(wfPath)
	want := "/home/user/.cistern/cataractae"
	if got != want {
		t.Errorf("cisternCataractaeDir(%q) = %q, want %q", wfPath, got, want)
	}
}

func TestCisternCataractaeDir_NestedAqueductDir(t *testing.T) {
	wfPath := "/repo/aqueduct/workflow.yaml"
	got := cisternCataractaeDir(wfPath)
	want := "/repo/cataractae"
	if got != want {
		t.Errorf("cisternCataractaeDir(%q) = %q, want %q", wfPath, got, want)
	}
}

// --- Flag registration ---

func TestCataractaeCmd_WorkflowFlagRegistered(t *testing.T) {
	if cataractaeGenerateCmd.Flags().Lookup("workflow") == nil {
		t.Error("--workflow not registered on generate")
	}
	if cataractaeListCmd.Flags().Lookup("workflow") == nil {
		t.Error("--workflow not registered on list")
	}
	if cataractaeEditCmd.Flags().Lookup("workflow") == nil {
		t.Error("--workflow not registered on edit")
	}
	if cataractaeAddCmd.Flags().Lookup("workflow") == nil {
		t.Error("--workflow not registered on add")
	}
}

// --- runCataractaeGenerate ---

func TestRunCataractaeGenerate_GeneratesClaudeMd(t *testing.T) {
	tmpDir, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)
	makeCataractaeDir(t, tmpDir, "tester")

	if err := runCataractaeGenerate(cataractaeGenerateCmd, nil); err != nil {
		t.Fatalf("runCataractaeGenerate: %v", err)
	}

	claudePath := filepath.Join(tmpDir, "cataractae", "tester", "CLAUDE.md")
	if _, err := os.Stat(claudePath); os.IsNotExist(err) {
		t.Error("CLAUDE.md was not created")
	}
}

func TestRunCataractaeGenerate_NoOpWhenNoCataractaeDirs(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)
	// No cataractae dirs — should succeed with "no cataractae" message, zero files.
	if err := runCataractaeGenerate(cataractaeGenerateCmd, nil); err != nil {
		t.Fatalf("runCataractaeGenerate: %v", err)
	}
}

func TestRunCataractaeGenerate_ContentIncludesPersonaAndInstructions(t *testing.T) {
	tmpDir, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)

	testerDir := filepath.Join(tmpDir, "cataractae", "tester")
	if err := os.MkdirAll(testerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "PERSONA.md"), []byte("# Role: Tester\n\nA careful tester."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testerDir, "INSTRUCTIONS.md"), []byte("Run all the tests."), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runCataractaeGenerate(cataractaeGenerateCmd, nil); err != nil {
		t.Fatalf("runCataractaeGenerate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(testerDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "A careful tester.") {
		t.Error("CLAUDE.md missing persona content")
	}
	if !strings.Contains(content, "Run all the tests.") {
		t.Error("CLAUDE.md missing instructions content")
	}
	if !strings.Contains(content, "generated by ct cataractae generate") {
		t.Error("CLAUDE.md missing generated header")
	}
}

// --- runCataractaeList ---

func TestRunCataractaeList_ListsIdentities(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)

	if err := runCataractaeList(cataractaeListCmd, nil); err != nil {
		t.Fatalf("runCataractaeList: %v", err)
	}
}

func TestRunCataractaeList_EmptyWorkflow(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowNoIdentityYAML)
	setWorkflow(t, wfPath)

	if err := runCataractaeList(cataractaeListCmd, nil); err != nil {
		t.Fatalf("runCataractaeList with no identities: %v", err)
	}
}

func TestRunCataractaeList_MultipleIdentities(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowMultiIdentityYAML)
	setWorkflow(t, wfPath)

	if err := runCataractaeList(cataractaeListCmd, nil); err != nil {
		t.Fatalf("runCataractaeList multi-identity: %v", err)
	}
}

// --- runCataractaeAdd ---

func TestRunCataractaeAdd_ScaffoldsFiles(t *testing.T) {
	tmpDir, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)

	if err := runCataractaeAdd(cataractaeAddCmd, []string{"my_role"}); err != nil {
		t.Fatalf("runCataractaeAdd: %v", err)
	}

	roleDir := filepath.Join(tmpDir, "cataractae", "my_role")
	if _, err := os.Stat(filepath.Join(roleDir, "PERSONA.md")); os.IsNotExist(err) {
		t.Error("PERSONA.md not created")
	}
	if _, err := os.Stat(filepath.Join(roleDir, "INSTRUCTIONS.md")); os.IsNotExist(err) {
		t.Error("INSTRUCTIONS.md not created")
	}
}

func TestRunCataractaeAdd_ErrorOnDuplicate(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)

	if err := runCataractaeAdd(cataractaeAddCmd, []string{"my_role"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := runCataractaeAdd(cataractaeAddCmd, []string{"my_role"}); err == nil {
		t.Error("expected error on duplicate add, got nil")
	}
}

func TestRunCataractaeAdd_PersonaMdHasRoleHeader(t *testing.T) {
	tmpDir, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)

	if err := runCataractaeAdd(cataractaeAddCmd, []string{"doc_writer"}); err != nil {
		t.Fatalf("runCataractaeAdd: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "cataractae", "doc_writer", "PERSONA.md"))
	if err != nil {
		t.Fatalf("read PERSONA.md: %v", err)
	}
	if !strings.Contains(string(data), "# Role: Doc Writer") {
		t.Errorf("PERSONA.md missing role header, got:\n%s", data)
	}
}

// --- runCataractaeEdit ---

func TestRunCataractaeEdit_NoIdentities(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowNoIdentityYAML)
	setWorkflow(t, wfPath)

	// No identities → early return with nil error.
	if err := runCataractaeEdit(cataractaeEditCmd, nil); err != nil {
		t.Fatalf("runCataractaeEdit with no identities: %v", err)
	}
}

func TestRunCataractaeEdit_InvalidSelectionNonNumeric(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)
	replaceStdin(t, "abc\n")

	err := runCataractaeEdit(cataractaeEditCmd, nil)
	if err == nil {
		t.Fatal("expected error for non-numeric selection, got nil")
	}
	if !strings.Contains(err.Error(), "invalid selection") {
		t.Errorf("error = %q, want 'invalid selection'", err.Error())
	}
}

func TestRunCataractaeEdit_InvalidSelectionOutOfRange(t *testing.T) {
	_, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)
	replaceStdin(t, "99\n")

	err := runCataractaeEdit(cataractaeEditCmd, nil)
	if err == nil {
		t.Fatal("expected error for out-of-range selection, got nil")
	}
	if !strings.Contains(err.Error(), "invalid selection") {
		t.Errorf("error = %q, want 'invalid selection'", err.Error())
	}
}

func TestRunCataractaeEdit_UpdatesClaudeMdAfterEdit(t *testing.T) {
	tmpDir, wfPath := makeWorkflowDir(t, testWorkflowYAML)
	setWorkflow(t, wfPath)
	makeCataractaeDir(t, tmpDir, "tester")
	replaceStdin(t, "1\n")
	t.Setenv("EDITOR", "true") // 'true' succeeds without modifying the file

	if err := runCataractaeEdit(cataractaeEditCmd, nil); err != nil {
		t.Fatalf("runCataractaeEdit: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "cataractae", "tester", "CLAUDE.md")); os.IsNotExist(err) {
		t.Error("CLAUDE.md was not regenerated after edit")
	}
}

// --- readPersonaName ---

func TestReadPersonaName_WithRoleHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PERSONA.md")
	if err := os.WriteFile(path, []byte("# Role: My Custom Role\n\nSome description."), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readPersonaName(path, "my_role")
	if got != "My Custom Role" {
		t.Errorf("readPersonaName = %q, want %q", got, "My Custom Role")
	}
}

func TestReadPersonaName_FallbackToTitleCaseWhenMissing(t *testing.T) {
	got := readPersonaName(filepath.Join(t.TempDir(), "nonexistent.md"), "my_role")
	if got != "My Role" {
		t.Errorf("readPersonaName = %q, want %q", got, "My Role")
	}
}

func TestReadPersonaName_FallbackToTitleCaseWhenNoRoleHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PERSONA.md")
	if err := os.WriteFile(path, []byte("## Some Other Header\n\nContent"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readPersonaName(path, "docs_writer")
	if got != "Docs Writer" {
		t.Errorf("readPersonaName = %q, want %q", got, "Docs Writer")
	}
}

