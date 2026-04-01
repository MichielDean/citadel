package cataractae

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
)

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initTestRepo creates a git repo with an initial commit and sets
// refs/remotes/origin/main to HEAD — simulating a fetched remote without
// needing a real one. Returns the temp directory path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	runGit(t, dir, "update-ref", "refs/remotes/origin/main", runGit(t, dir, "rev-parse", "HEAD"))

	return dir
}

// TestGenerateDiff_NonEmptyWithChanges is an end-to-end regression test for
// ci-s5eg9: review got an empty diff.patch because generateDiff
// was called on the worker's own sandbox (on main) instead of the per-droplet
// worktree (on feat/<id> with committed changes).
//
// This test verifies that generateDiff produces a non-empty diff when the
// sandbox directory contains committed changes on a feature branch vs
// origin/main. It is the "closed loop" counterpart to
// TestDispatch_DiffOnlyStepGetsSandboxDir, which only verifies that the
// correct path is passed — not that the diff itself is non-empty.
func TestGenerateDiff_NonEmptyWithChanges(t *testing.T) {
	dir := initTestRepo(t)

	// Create feature branch and commit a new file — simulates an implementer pass.
	runGit(t, dir, "checkout", "-b", "feat/ci-s5eg9-test")
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "feat: add feature.go")

	// generateDiff must return a non-empty diff containing the new file.
	diff, err := generateDiff(dir)
	if err != nil {
		t.Fatalf("generateDiff: %v", err)
	}
	if len(diff) == 0 {
		t.Fatal("generateDiff returned empty diff — diff_only reviewer would see empty diff.patch (regression: ci-s5eg9)")
	}
	if !strings.Contains(string(diff), "feature.go") {
		t.Errorf("generateDiff output should contain 'feature.go'; got:\n%s", diff)
	}
}

// TestGenerateDiff_EmptyOnMain verifies that generateDiff returns an empty
// diff (not an error) when the sandbox is on the same commit as origin/main.
// This is a boundary test: no-changes produces empty bytes, not an error.
// The actual regression guard for ci-s5eg9 is TestGenerateDiff_NonEmptyWithChanges.
func TestGenerateDiff_EmptyOnMain(t *testing.T) {
	dir := initTestRepo(t)

	diff, err := generateDiff(dir)
	if err != nil {
		t.Fatalf("generateDiff: %v", err)
	}
	if len(diff) != 0 {
		t.Errorf("expected empty diff when HEAD == origin/main; got %d bytes:\n%s", len(diff), diff)
	}
}

// TestWriteContextFile_RecentStepNotes_ExcludesOtherCataractae verifies that the
// 'Recent Step Notes' section only contains notes from the current step's cataractae,
// not notes from other steps (ci-tgj96).
//
// Given: notes from multiple cataractae (implement + deliver)
// When:  writeContextFile is called with step "implement"
// Then:  'Recent Step Notes' contains only the implement notes, not deliver notes
func TestWriteContextFile_RecentStepNotes_ExcludesOtherCataractae(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-test", Title: "Test item", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: "agent"}

	// Notes ordered newest-first; "deliver" notes are foreign to the current step.
	notes := []cistern.CataractaeNote{
		{CataractaeName: "implement", Content: "implement note A"},
		{CataractaeName: "deliver", Content: "deliver note X"},
		{CataractaeName: "implement", Content: "implement note B"},
		{CataractaeName: "deliver", Content: "deliver note Y"},
	}

	p := ContextParams{
		Item:  item,
		Step:  step,
		Notes: notes,
	}

	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	// Extract only the Recent Step Notes section to avoid false matches in other sections.
	sectionStart := strings.Index(got, "## Recent Step Notes")
	if sectionStart == -1 {
		t.Fatal("'## Recent Step Notes' section not found in CONTEXT.md")
	}
	// Trim everything before the section.
	section := got[sectionStart:]

	if !strings.Contains(section, "implement note A") {
		t.Error("expected own-cataractae note 'implement note A' in Recent Step Notes")
	}
	if !strings.Contains(section, "implement note B") {
		t.Error("expected own-cataractae note 'implement note B' in Recent Step Notes")
	}
	if strings.Contains(section, "deliver note X") {
		t.Error("cross-cataractae note 'deliver note X' must not appear in Recent Step Notes")
	}
	if strings.Contains(section, "deliver note Y") {
		t.Error("cross-cataractae note 'deliver note Y' must not appear in Recent Step Notes")
	}
}

// TestWriteContextFile_RecentStepNotes_EmptyWhenNoOwnNotes verifies that the
// 'Recent Step Notes' section is omitted entirely when the current cataractae
// has no prior notes (ci-tgj96).
//
// Given: notes exist only from a different cataractae ("deliver")
// When:  writeContextFile is called with step "implement"
// Then:  no 'Recent Step Notes' section appears at all
func TestWriteContextFile_RecentStepNotes_EmptyWhenNoOwnNotes(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-test2", Title: "Test item 2", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: "agent"}

	notes := []cistern.CataractaeNote{
		{CataractaeName: "deliver", Content: "deliver note only"},
	}

	p := ContextParams{
		Item:  item,
		Step:  step,
		Notes: notes,
	}

	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	if strings.Contains(got, "## Recent Step Notes") {
		t.Error("'## Recent Step Notes' section must not appear when no own-cataractae notes exist")
	}
	if strings.Contains(got, "deliver note only") {
		t.Error("cross-cataractae note 'deliver note only' must not appear anywhere in Recent Step Notes")
	}
}

// TestWriteContextFile_ManualNotes_ShownSeparately verifies that notes with
// CataractaeName=="manual" appear in a dedicated "## Manual Notes" section
// and are NOT silently dropped by the step-name filter (ci-tgj96).
//
// Given: notes from implement, deliver, and manual sources
// When:  writeContextFile is called with step "implement"
// Then:  manual notes appear in "## Manual Notes", implement notes appear in
//
//	"## Recent Step Notes", and deliver notes appear in neither section.
func TestWriteContextFile_ManualNotes_ShownSeparately(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-test3", Title: "Test manual notes", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: "agent"}

	notes := []cistern.CataractaeNote{
		{CataractaeName: "manual", Content: "operator annotation: critical refinement needed"},
		{CataractaeName: "implement", Content: "implement note A"},
		{CataractaeName: "deliver", Content: "deliver note X"},
		{CataractaeName: "manual", Content: "operator annotation: second note"},
	}

	p := ContextParams{
		Item:  item,
		Step:  step,
		Notes: notes,
	}

	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	// Manual notes must appear in the dedicated Manual Notes section.
	manualIdx := strings.Index(got, "## Manual Notes")
	if manualIdx == -1 {
		t.Fatal("'## Manual Notes' section not found — manual notes would be invisible (regression: ci-tgj96)")
	}
	manualSection := got[manualIdx:]
	if !strings.Contains(manualSection, "operator annotation: critical refinement needed") {
		t.Error("expected first manual note in '## Manual Notes' section")
	}
	if !strings.Contains(manualSection, "operator annotation: second note") {
		t.Error("expected second manual note in '## Manual Notes' section")
	}

	// Own-step notes must still appear in Recent Step Notes.
	recentIdx := strings.Index(got, "## Recent Step Notes")
	if recentIdx == -1 {
		t.Fatal("'## Recent Step Notes' section not found")
	}
	if !strings.Contains(got[recentIdx:], "implement note A") {
		t.Error("expected own-cataractae note 'implement note A' in '## Recent Step Notes'")
	}

	// Cross-cataractae step notes must not appear anywhere.
	if strings.Contains(got, "deliver note X") {
		t.Error("cross-cataractae note 'deliver note X' must not appear anywhere in CONTEXT.md")
	}
}

// TestWriteContextFile_SchedulerNotes_ShownSeparately verifies that notes with
// CataractaeName=="scheduler" appear in a dedicated "## Scheduler Notes" section
// and are NOT silently dropped by the step-name filter (ci-tgj96).
//
// Given: notes from implement, deliver, manual, and scheduler sources
// When:  writeContextFile is called with step "implement"
// Then:  scheduler notes appear in "## Scheduler Notes", implement notes appear
//
//	in "## Recent Step Notes", manual notes in "## Manual Notes", and
//	deliver notes appear in none of these sections.
func TestWriteContextFile_SchedulerNotes_ShownSeparately(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-test4", Title: "Test scheduler notes", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "implement", Type: "agent"}

	notes := []cistern.CataractaeNote{
		{CataractaeName: "scheduler", Content: "scheduler: zombie detected, recirculating"},
		{CataractaeName: "implement", Content: "implement note A"},
		{CataractaeName: "deliver", Content: "deliver note X"},
		{CataractaeName: "scheduler", Content: "scheduler: timeout notice"},
		{CataractaeName: "manual", Content: "operator annotation"},
	}

	p := ContextParams{
		Item:  item,
		Step:  step,
		Notes: notes,
	}

	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	// Scheduler notes must appear in the dedicated Scheduler Notes section.
	schedulerIdx := strings.Index(got, "## Scheduler Notes")
	if schedulerIdx == -1 {
		t.Fatal("'## Scheduler Notes' section not found — scheduler notes would be invisible (regression: ci-tgj96)")
	}
	schedulerSection := got[schedulerIdx:]
	if !strings.Contains(schedulerSection, "scheduler: zombie detected, recirculating") {
		t.Error("expected first scheduler note in '## Scheduler Notes' section")
	}
	if !strings.Contains(schedulerSection, "scheduler: timeout notice") {
		t.Error("expected second scheduler note in '## Scheduler Notes' section")
	}

	// Own-step notes must still appear in Recent Step Notes.
	recentIdx := strings.Index(got, "## Recent Step Notes")
	if recentIdx == -1 {
		t.Fatal("'## Recent Step Notes' section not found")
	}
	if !strings.Contains(got[recentIdx:], "implement note A") {
		t.Error("expected own-cataractae note 'implement note A' in '## Recent Step Notes'")
	}

	// Manual notes must still appear in Manual Notes section.
	if !strings.Contains(got, "## Manual Notes") {
		t.Error("'## Manual Notes' section not found")
	}
	if !strings.Contains(got, "operator annotation") {
		t.Error("expected manual note 'operator annotation' in CONTEXT.md")
	}

	// Cross-cataractae step notes must not appear anywhere.
	if strings.Contains(got, "deliver note X") {
		t.Error("cross-cataractae note 'deliver note X' must not appear anywhere in CONTEXT.md")
	}
}

// TestRevisionCycleNotes_ReviewerExcludesOtherReviewerCataractae verifies that
// when a reviewer cataractae calls revisionCycleNotes, it only receives its own
// prior notes — not notes from other reviewer-like cataractae (ci-0y5ha fix 2).
//
// Given: notes from "security" and "qa" — both reviewer-like cataractae
// When:  revisionCycleNotes is called with step="security"
// Then:  only "security" notes are returned; "qa" notes are excluded
func TestRevisionCycleNotes_ReviewerExcludesOtherReviewerCataractae(t *testing.T) {
	notes := []cistern.CataractaeNote{
		{CataractaeName: "security", Content: "Missing auth check in login handler"},
		{CataractaeName: "qa", Content: "Unit test coverage is insufficient"},
	}
	step := &aqueduct.WorkflowCataractae{Name: "security", Identity: "security", Type: "agent"}

	got := revisionCycleNotes(notes, step)

	if len(got) != 1 {
		t.Fatalf("revisionCycleNotes returned %d notes, want 1; got: %v", len(got), got)
	}
	if got[0].CataractaeName != "security" {
		t.Errorf("expected security note, got CataractaeName=%q", got[0].CataractaeName)
	}
}

// TestRevisionCycleNotes_ReviewerMatchesByIdentity verifies that when
// CataractaeName matches the step's Identity (not Name), the note is included.
// This is needed when notes are stored under the identity ("reviewer") but the
// step name is different ("review").
//
// Given: note with CataractaeName="reviewer", step.Name="review", step.Identity="reviewer"
// When:  revisionCycleNotes is called for the "review" step
// Then:  the note is returned (identity match)
func TestRevisionCycleNotes_ReviewerMatchesByIdentity(t *testing.T) {
	notes := []cistern.CataractaeNote{
		{CataractaeName: "reviewer", Content: "Needs better documentation"},
	}
	step := &aqueduct.WorkflowCataractae{Name: "review", Identity: "reviewer", Type: "agent"}

	got := revisionCycleNotes(notes, step)

	if len(got) != 1 {
		t.Fatalf("revisionCycleNotes returned %d notes, want 1; got: %v", len(got), got)
	}
	if got[0].Content != "Needs better documentation" {
		t.Errorf("unexpected note content: %q", got[0].Content)
	}
}

// TestWriteContextFile_Phase1OnlyContainsOwnIssues verifies that a reviewer's
// Phase 1 section only shows issues flagged by that reviewer — not issues from
// other cataractae (ci-0y5ha fix 1 & 3).
//
// Given: "security" step with OpenIssues from both "security" and "qa" flaggers
// When:  writeContextFile is called for the "security" step
// Then:  Phase 1 contains only the security issue; qa issue is NOT in Phase 1
//
//	qa issue appears in a clearly labeled read-only background section
func TestWriteContextFile_Phase1OnlyContainsOwnIssues(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-0y5ha-t1", Title: "Test", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "security", Identity: "security", Type: "agent"}

	issues := []cistern.DropletIssue{
		{ID: "sec-abc01", FlaggedBy: "security", Description: "SQL injection in login endpoint"},
		{ID: "qa-abc02", FlaggedBy: "qa", Description: "Missing unit tests for auth module"},
	}

	p := ContextParams{Item: item, Step: step, OpenIssues: issues}
	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	// Phase 1 must exist and show the security issue.
	if !strings.Contains(got, "TWO-PHASE REVIEW") {
		t.Fatal("expected TWO-PHASE REVIEW template when reviewer has own issues")
	}
	phase2Idx := strings.Index(got, "### Phase 2")
	if phase2Idx == -1 {
		t.Fatal("Phase 2 section not found")
	}
	phase1Section := got[:phase2Idx]

	if !strings.Contains(phase1Section, "SQL injection in login endpoint") {
		t.Error("security's own issue must appear in Phase 1")
	}
	if strings.Contains(phase1Section, "Missing unit tests for auth module") {
		t.Error("qa issue must NOT appear in Phase 1 of security's context (cross-contamination: ci-0y5ha)")
	}

	// qa issue must appear in a read-only background section, not silently dropped.
	if !strings.Contains(got, "Missing unit tests for auth module") {
		t.Error("qa issue must appear in a read-only background section")
	}
	// The background section must include the qa issue ID.
	if !strings.Contains(got, "qa-abc02") {
		t.Error("qa issue ID must appear in the background section")
	}
}

// TestWriteContextFile_RecentStepNotes_MatchesByIdentity verifies that notes
// stored under step.Identity appear in "Recent Step Notes" even when
// step.Identity != step.Name (ci-0y5ha: ownNotes partition fix).
//
// Given: a step with Name="review" and Identity="reviewer", and a note with
//
//	CataractaeName="reviewer" (CLI-sourced notes use Identity, not Name)
//
// When:  writeContextFile is called
// Then:  the note appears in "## Recent Step Notes"
func TestWriteContextFile_RecentStepNotes_MatchesByIdentity(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-0y5ha-id", Title: "Identity test", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "review", Identity: "reviewer", Type: "agent"}

	notes := []cistern.CataractaeNote{
		{CataractaeName: "reviewer", Content: "note stored under identity"},
		{CataractaeName: "review", Content: "note stored under name"},
		{CataractaeName: "implement", Content: "foreign note must be excluded"},
	}

	p := ContextParams{Item: item, Step: step, Notes: notes}
	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	sectionStart := strings.Index(got, "## Recent Step Notes")
	if sectionStart == -1 {
		t.Fatal("'## Recent Step Notes' section not found")
	}
	section := got[sectionStart:]

	if !strings.Contains(section, "note stored under identity") {
		t.Error("note stored under step.Identity must appear in Recent Step Notes (ci-0y5ha fix)")
	}
	if !strings.Contains(section, "note stored under name") {
		t.Error("note stored under step.Name must appear in Recent Step Notes")
	}
	if strings.Contains(section, "foreign note must be excluded") {
		t.Error("foreign note must NOT appear in Recent Step Notes")
	}
}

// TestWriteContextFile_ExternalRef_WrittenToContextMd verifies that when a
// droplet has an ExternalRef set, writeContextFile emits it in the exact format
// that the delivery cataractae shell script greps for:
//   grep '^\*\*External Ref:\*\*' CONTEXT.md | awk '{print $3}'
//
// Given: a droplet with ExternalRef "jira:DPF-456"
// When:  writeContextFile is called
// Then:  CONTEXT.md contains the line "**External Ref:** jira:DPF-456"
//
// Also verifies the negative case: when ExternalRef is empty, no External Ref
// line is written (the delivery cataractae would read an empty REF_KEY).
func TestWriteContextFile_ExternalRef_WrittenToContextMd(t *testing.T) {
	step := &aqueduct.WorkflowCataractae{Name: "deliver", Type: "agent"}

	t.Run("ExternalRef set", func(t *testing.T) {
		item := &cistern.Droplet{ID: "ci-ikbj2-t1", Title: "Imported task", Status: "in_progress", ExternalRef: "jira:DPF-456"}
		p := ContextParams{Item: item, Step: step}
		ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
		if err := writeContextFile(ctxPath, p); err != nil {
			t.Fatalf("writeContextFile: %v", err)
		}
		content, err := os.ReadFile(ctxPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		got := string(content)
		// The delivery cataractae greps for exactly this pattern:
		//   grep '^\*\*External Ref:\*\*' CONTEXT.md | awk '{print $3}'
		// awk '{print $3}' returns the third whitespace-separated field, which is
		// the value. The line must be exactly "**External Ref:** jira:DPF-456".
		want := "**External Ref:** jira:DPF-456"
		if !strings.Contains(got, want) {
			t.Errorf("CONTEXT.md missing %q — delivery shell would parse empty REF_KEY\ngot:\n%s", want, got)
		}
	})

	t.Run("ExternalRef empty", func(t *testing.T) {
		item := &cistern.Droplet{ID: "ci-ikbj2-t2", Title: "Native task", Status: "in_progress", ExternalRef: ""}
		p := ContextParams{Item: item, Step: step}
		ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
		if err := writeContextFile(ctxPath, p); err != nil {
			t.Fatalf("writeContextFile: %v", err)
		}
		content, err := os.ReadFile(ctxPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		got := string(content)
		if strings.Contains(got, "**External Ref:**") {
			t.Error("CONTEXT.md must not contain '**External Ref:**' when ExternalRef is empty")
		}
	})
}

// TestWriteContextFile_NoTwoPhaseWhenOnlyForeignIssues verifies that the
// two-phase review template is NOT shown when all OpenIssues belong to other
// cataractae — the current reviewer has no own issues to verify (ci-0y5ha fix 3).
//
// Given: "security" step with OpenIssues ONLY from "qa" (security has none)
// When:  writeContextFile is called for the "security" step
// Then:  no TWO-PHASE REVIEW header; no Phase 1 resolve/reject instructions
//
//	qa issue appears in a clearly labeled read-only background section
func TestWriteContextFile_NoTwoPhaseWhenOnlyForeignIssues(t *testing.T) {
	item := &cistern.Droplet{ID: "ci-0y5ha-t2", Title: "Test", Status: "in_progress"}
	step := &aqueduct.WorkflowCataractae{Name: "security", Identity: "security", Type: "agent"}

	// Only qa issues — security has none of its own.
	issues := []cistern.DropletIssue{
		{ID: "qa-xyz01", FlaggedBy: "qa", Description: "Test coverage below threshold"},
	}

	p := ContextParams{Item: item, Step: step, OpenIssues: issues}
	ctxPath := filepath.Join(t.TempDir(), "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}

	content, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(content)

	// Must NOT trigger the two-phase template (no own issues).
	if strings.Contains(got, "TWO-PHASE REVIEW") {
		t.Error("security must NOT get two-phase review when it has no own issues (ci-0y5ha fix 3)")
	}
	if strings.Contains(got, "ct droplet issue resolve") {
		t.Error("security must NOT be instructed to resolve qa's issues")
	}
	if strings.Contains(got, "ct droplet issue reject") {
		t.Error("security must NOT be instructed to reject qa's issues")
	}

	// qa issue must still be visible as background context.
	if !strings.Contains(got, "Test coverage below threshold") {
		t.Error("qa issue must appear in read-only background section even when security has no own issues")
	}
}

func TestReadSkillDescription_SkipsFrontmatter(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			// After skipping frontmatter and the heading, the first body line is returned.
			name:    "WithFrontmatter_ReturnsFirstBodyLine",
			content: "---\nname: my-skill\ndescription: Actual description here\ntype: user\n---\n\n# My Skill\n\nSome body text.\n",
			want:    "Some body text.",
		},
		{
			// Frontmatter is skipped; heading after frontmatter is also skipped; body line returned.
			name:    "WithFrontmatter_SkipsHeadingAfterFrontmatter",
			content: "---\nname: cataractae-protocol\ndescription: Protocol for cataractae agents\n---\n\n# Cataractae Protocol\n\nThe real description starts here.\n",
			want:    "The real description starts here.",
		},
		{
			name:    "WithoutFrontmatter_ReturnsFirstContentLine",
			content: "# My Skill\n\nThe description line.\n",
			want:    "The description line.",
		},
		{
			name:    "FrontmatterOnly_FallsBackToDir",
			content: "---\nname: empty\n---\n",
			want:    "", // signals fallback to dir name
		},
		{
			// Opening --- must be the very first line to be treated as frontmatter.
			name:    "DashesNotFirstLine_NotTreatedAsFrontmatter",
			content: "The real first line.\n---\nsome other content\n",
			want:    "The real first line.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			skillDir := filepath.Join(dir, "my-skill")
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(skillDir, "SKILL.md")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}

			got := readSkillDescription(path)

			if tc.want == "" {
				// FrontmatterOnly case: falls back to directory name
				if got != "my-skill" {
					t.Errorf("want fallback %q, got %q", "my-skill", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestReadSkillDescription_WithFrontmatter_DoesNotReturnDashes(t *testing.T) {
	// Regression test for ci-bz0t3: SKILL.md files with YAML frontmatter were
	// returning "---" as the description because the function did not skip the
	// frontmatter block. All 5 skills in the repo have frontmatter.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "cataractae-protocol")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(skillDir, "SKILL.md")
	content := "---\nname: cataractae-protocol\ndescription: Discourse protocol for cataractae agents\ntype: reference\n---\n\n# Cataractae Protocol\n\nThis skill describes the protocol.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := readSkillDescription(path)

	if got == "---" {
		t.Error("readSkillDescription must not return '---' for SKILL.md files with YAML frontmatter")
	}
	if got == "" {
		t.Error("readSkillDescription must return a non-empty description")
	}
}
