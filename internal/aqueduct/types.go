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

// SkillRef references a locally installed skill by name.
//
// In-repo skills (e.g. a SKILL.md that lives in the same repo the agents work
// on) may set Path to a repo-relative path — the runner copies it directly from
// the agent's sandbox worktree without any network access.
//
// External skills must be installed ahead of time via `ct skills install <name>
// <url>` and are referenced by name only. The runtime never fetches skills
// automatically; it only reads from ~/.cistern/skills/<name>/SKILL.md.
type SkillRef struct {
	Name string `yaml:"name"`
	Path string `yaml:"path,omitempty"` // repo-relative path (in-repo skills only)
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

// RateLimitConfig configures rate limiting for the delivery cataracta API endpoint.
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
	// RateLimit configures rate limiting for the delivery cataracta API endpoint.
	// Omit to use the built-in defaults (60 req/min per IP, 120 req/min per token).
	RateLimit             *RateLimitConfig `yaml:"rate_limit,omitempty"`
	// DeliveryAddr is the TCP listen address for the delivery cataracta HTTP
	// server (e.g. ":8080"). An empty string disables the HTTP server.
	DeliveryAddr          string           `yaml:"delivery_addr,omitempty"`
}
