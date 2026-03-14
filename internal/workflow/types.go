package workflow

// StepType classifies what runs in a workflow step.
type StepType string

const (
	StepTypeAgent     StepType = "agent"
	StepTypeAutomated StepType = "automated"
	StepTypeGate      StepType = "gate"
	StepTypeHuman     StepType = "human"
)

// ContextLevel controls what context an agent step receives.
type ContextLevel string

const (
	ContextFullCodebase ContextLevel = "full_codebase"
	ContextDiffOnly     ContextLevel = "diff_only"
	ContextSpecOnly     ContextLevel = "spec_only"
)

// WorkflowStep defines a single step in a workflow pipeline.
type WorkflowStep struct {
	Name           string       `yaml:"name"`
	Type           StepType     `yaml:"type"`
	Role           string       `yaml:"role,omitempty"`
	Model          string       `yaml:"model,omitempty"`
	Context        ContextLevel `yaml:"context,omitempty"`

	TimeoutMinutes int          `yaml:"timeout_minutes,omitempty"`
	OnPass         string       `yaml:"on_pass,omitempty"`
	OnFail         string       `yaml:"on_fail,omitempty"`
	OnRevision     string       `yaml:"on_revision,omitempty"`
	OnEscalate     string       `yaml:"on_escalate,omitempty"`
}

// RoleDefinition defines an agent role in YAML.
type RoleDefinition struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	Instructions string `yaml:"instructions"`
}

// Workflow is a named sequence of steps parsed from a YAML file.
type Workflow struct {
	Name  string                    `yaml:"name"`
	Steps []WorkflowStep            `yaml:"steps"`
	Roles map[string]RoleDefinition `yaml:"roles,omitempty"`
}

// RepoConfig defines a repository managed by the farm.
type RepoConfig struct {
	Name         string   `yaml:"name"`
	URL          string   `yaml:"url"`
	WorkflowPath string   `yaml:"workflow_path"`
	Workers      int      `yaml:"workers"`
	Names        []string `yaml:"names,omitempty"`
	Prefix       string   `yaml:"prefix"`
}

// FarmConfig is the top-level configuration for a Citadel instance.
type FarmConfig struct {
	Repos                 []RepoConfig `yaml:"repos"`
	MaxTotalWorkers       int          `yaml:"max_total_workers"`
	HandoffTokenThreshold int          `yaml:"handoff_token_threshold"`
	RetentionDays         int          `yaml:"retention_days"`
	CleanupInterval       string       `yaml:"cleanup_interval"`
}
