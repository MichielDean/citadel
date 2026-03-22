package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/aqueduct"
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
