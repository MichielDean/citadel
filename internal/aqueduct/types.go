package aqueduct

// CataractaeType classifies what runs in a workflow step.
type CataractaeType string

const (
	CataractaeTypeAgent     CataractaeType = "agent"
	CataractaeTypeAutomated CataractaeType = "automated"
	CataractaeTypeGate      CataractaeType = "gate"
	CataractaeTypeHuman     CataractaeType = "human"
)

// ContextLevel controls what context an agent step receives.
type ContextLevel string

const (
	ContextFullCodebase ContextLevel = "full_codebase"
	ContextDiffOnly     ContextLevel = "diff_only"
	ContextSpecOnly     ContextLevel = "spec_only"
)

// SkillRef references a locally installed skill by name.
//
// All skills must be installed in ~/.cistern/skills/<name>/SKILL.md before use.
// In-repo skills are deployed automatically by the git_sync drought hook, which
// extracts skills/<name>/SKILL.md from origin/main into ~/.cistern/skills/.
// External skills are installed via `ct skills install <name> <url>`.
type SkillRef struct {
	Name string `yaml:"name"`
}

// WorkflowCataractae defines a single step in an aqueduct.
type WorkflowCataractae struct {
	Name    string       `yaml:"name"`
	Type    CataractaeType     `yaml:"type"`
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

// CataractaeDefinition defines an agent role in YAML.
type CataractaeDefinition struct {
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

// SkipCataractaeForLevel returns cataractae names that should be skipped for the given
// complexity level, derived from each cataractae's skip_for field.
func (wf *Workflow) SkipCataractaeForLevel(level int) []string {
	var skipped []string
	for _, step := range wf.Cataractae {
		for _, cx := range step.SkipFor {
			if cx == level {
				skipped = append(skipped, step.Name)
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
	Name       string               `yaml:"name"`
	Cataractae []WorkflowCataractae `yaml:"cataractae"`
	Complexity ComplexityConfig     `yaml:"complexity"`
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
	Name             string `yaml:"name"`
	Action           string `yaml:"action"`                      // built-in: "git_sync", "cataractae_generate", "worktree_prune", "db_vacuum", "events_prune", "tmp_cleanup", "restart_self" | "shell"
	Command          string `yaml:"command,omitempty"`           // only for action: shell
	Timeout          int    `yaml:"timeout_seconds,omitempty"`   // default 30s
	KeepDays         int    `yaml:"keep_days,omitempty"`         // for events_prune: days to retain (default 30)
	RestartIfUpdated bool   `yaml:"restart_if_updated,omitempty"` // for git_sync: exit cleanly if workflow changed (supervisor restarts)
}

// RateLimitConfig configures rate limiting for the delivery cataractae API endpoint.
// All limits apply within a sliding window. Zero values use the defaults noted below.
type RateLimitConfig struct {
	// PerIPRequests is the maximum number of requests allowed per source IP
	// within Window. Default: 60.
	PerIPRequests int `yaml:"per_ip_requests"`
	// PerTokenRequests is the maximum number of requests allowed per auth token
	// within Window. Default: 120.
	PerTokenRequests int `yaml:"per_token_requests"`
	// Window is the sliding window duration as a Go duration string (e.g. "1m",
	// "30s"). Default: "1m".
	Window string `yaml:"window"`
}

// AqueductConfig is the top-level configuration for a Cistern instance.
type AqueductConfig struct {
	Repos                 []RepoConfig     `yaml:"repos"`
	MaxCataractae         int              `yaml:"max_cataractae"`
	HandoffTokenThreshold int              `yaml:"handoff_token_threshold"`
	RetentionDays         int              `yaml:"retention_days"`
	CleanupInterval       string           `yaml:"cleanup_interval"`
	// HeartbeatInterval controls how often the Castellarius scans in-progress
	// droplets for orphaned or stalled sessions. Accepts Go duration strings
	// (e.g. "30s", "1m"). Defaults to "30s" when empty.
	HeartbeatInterval     string           `yaml:"heartbeat_interval,omitempty"`
	DroughtHooks          []DroughtHook    `yaml:"drought_hooks,omitempty"`
	// RateLimit configures rate limiting for the delivery cataractae API endpoint.
	// Omit to use the built-in defaults (60 req/min per IP, 120 req/min per token).
	RateLimit             *RateLimitConfig `yaml:"rate_limit,omitempty"`
	// DeliveryAddr is the TCP listen address for the delivery cataractae HTTP
	// server (e.g. ":8080"). An empty string disables the HTTP server.
	DeliveryAddr          string           `yaml:"delivery_addr,omitempty"`
}
