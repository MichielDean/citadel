package aqueduct

// CataractaType classifies what runs in a workflow step.
type CataractaType string

const (
	CataractaTypeAgent     CataractaType = "agent"
	CataractaTypeAutomated CataractaType = "automated"
	CataractaTypeGate      CataractaType = "gate"
	CataractaTypeHuman     CataractaType = "human"
)

// ContextLevel controls what context an agent step receives.
type ContextLevel string

const (
	ContextFullCodebase ContextLevel = "full_codebase"
	ContextDiffOnly     ContextLevel = "diff_only"
	ContextSpecOnly     ContextLevel = "spec_only"
)

// SkillRef references a skill by name and the URL to its SKILL.md file.
type SkillRef struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"` // raw GitHub URL to SKILL.md
}

// WorkflowCataracta defines a single step in an aqueduct.
type WorkflowCataracta struct {
	Name    string       `yaml:"name"`
	Type    CataractaType     `yaml:"type"`
	Identity string       `yaml:"identity,omitempty"`
	Model   string       `yaml:"model,omitempty"`
	Context ContextLevel `yaml:"context,omitempty"`

	TimeoutMinutes int        `yaml:"timeout_minutes,omitempty"`
	SkipFor        []int      `yaml:"skip_for,omitempty"` // complexity levels that skip this step
	Skills         []SkillRef `yaml:"skills,omitempty"`
	OnPass         string     `yaml:"on_pass,omitempty"`
	OnFail         string     `yaml:"on_fail,omitempty"`
	OnRecirculate  string     `yaml:"on_recirculate,omitempty"`
	OnEscalate     string     `yaml:"on_escalate,omitempty"`
}

// CataractaDefinition defines an agent role in YAML.
type CataractaDefinition struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	Instructions string `yaml:"instructions"`
}

// ComplexityLevel defines skip rules for a single complexity tier.
type ComplexityLevel struct {
	Level        int      `yaml:"level"`
	SkipCataractae  []string `yaml:"skip_cataractae"`
	RequireHuman bool     `yaml:"require_human,omitempty"`
}

// ComplexityConfig holds the four complexity tiers for a aqueduct.
type ComplexityConfig struct {
	Trivial  ComplexityLevel `yaml:"trivial"`
	Standard ComplexityLevel `yaml:"standard"`
	Full     ComplexityLevel `yaml:"full"`
	Critical ComplexityLevel `yaml:"critical"`
}

// SkipCataractaeForLevel returns cataracta names that should be skipped for the given
// complexity level, derived from each cataracta's skip_for field.
func (wf *Workflow) SkipCataractaeForLevel(level int) []string {
	var skipped []string
	for _, cataracta := range wf.Cataractae {
		for _, cx := range cataracta.SkipFor {
			if cx == level {
				skipped = append(skipped, cataracta.Name)
				break
			}
		}
	}
	return skipped
}

// SkipCataractaeForLevel on ComplexityConfig is kept for backward compat but delegates.
func (cc ComplexityConfig) SkipCataractaeForLevel(level int) []string {
	switch level {
	case 1:
		return cc.Trivial.SkipCataractae
	case 2:
		return cc.Standard.SkipCataractae
	case 4:
		return cc.Critical.SkipCataractae
	default:
		return cc.Full.SkipCataractae
	}
}

// RequireHumanForLevel returns whether a human gate is required for a given complexity level.
func (cc ComplexityConfig) RequireHumanForLevel(level int) bool {
	switch level {
	case 4:
		return cc.Critical.RequireHuman
	default:
		return false
	}
}

// Workflow is a named sequence of cataractae parsed from a YAML file.
type Workflow struct {
	Name       string                    `yaml:"name"`
	Cataractae    []WorkflowCataracta          `yaml:"cataractae"`
	CataractaDefinitions map[string]CataractaDefinition `yaml:"cataracta_definitions,omitempty"`
	Complexity ComplexityConfig          `yaml:"complexity"`
}

// RepoConfig defines a repository managed by the farm.
type RepoConfig struct {
	Name         string   `yaml:"name"`
	URL          string   `yaml:"url"`
	WorkflowPath string   `yaml:"workflow_path"`
	Cataractae      int      `yaml:"cataractae"`
	Names        []string `yaml:"names,omitempty"`
	Prefix       string   `yaml:"prefix"`
}

// DroughtHook defines an action to run when the scheduler enters drought (idle) state.
type DroughtHook struct {
	Name    string `yaml:"name"`
	Action  string `yaml:"action"`                    // built-in: "cataractae_generate", "worktree_prune", "db_vacuum" | "shell"
	Command string `yaml:"command,omitempty"`         // only for action: shell
	Timeout int    `yaml:"timeout_seconds,omitempty"` // default 30s
}

// AqueductConfig is the top-level configuration for a Cistern instance.
type AqueductConfig struct {
	Repos                 []RepoConfig  `yaml:"repos"`
	MaxCataractae         int           `yaml:"max_cataractae"`
	HandoffTokenThreshold int           `yaml:"handoff_token_threshold"`
	RetentionDays         int           `yaml:"retention_days"`
	CleanupInterval       string        `yaml:"cleanup_interval"`
	// HeartbeatInterval controls how often the Castellarius scans in-progress
	// droplets for orphaned or stalled sessions. Accepts Go duration strings
	// (e.g. "30s", "1m"). Defaults to "30s" when empty.
	HeartbeatInterval     string        `yaml:"heartbeat_interval,omitempty"`
	DroughtHooks          []DroughtHook `yaml:"drought_hooks,omitempty"`
}
