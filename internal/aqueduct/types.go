package aqueduct

import "sort"

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
	Name     string         `yaml:"name"`
	Type     CataractaeType `yaml:"type"`
	Identity string         `yaml:"identity,omitempty"`
	Model    *string        `yaml:"model,omitempty"`
	Context  ContextLevel   `yaml:"context,omitempty"`

	TimeoutMinutes int        `yaml:"timeout_minutes,omitempty"`
	Skills         []SkillRef `yaml:"skills,omitempty"`
	OnPass         string     `yaml:"on_pass,omitempty"`
	OnFail         string     `yaml:"on_fail,omitempty"`
	OnRecirculate  string     `yaml:"on_recirculate,omitempty"`
	OnPool         string     `yaml:"on_pool,omitempty"`
}

// ComplexityLevel configures a single complexity tier.
type ComplexityLevel struct {
	Level        int  `yaml:"level"`
	RequireHuman bool `yaml:"require_human,omitempty"`
}

// ComplexityConfig holds the three complexity tiers for a aqueduct.
type ComplexityConfig struct {
	Standard ComplexityLevel `yaml:"standard"`
	Full     ComplexityLevel `yaml:"full"`
	Critical ComplexityLevel `yaml:"critical"`
}

// UniqueIdentities returns the deduplicated, sorted list of agent identity
// strings from the workflow's cataractae steps.
func (wf *Workflow) UniqueIdentities() []string {
	seen := map[string]bool{}
	var ids []string
	for _, step := range wf.Cataractae {
		if step.Identity != "" && !seen[step.Identity] {
			seen[step.Identity] = true
			ids = append(ids, step.Identity)
		}
	}
	sort.Strings(ids)
	return ids
}

// RequireHumanForLevel returns whether a human gate is required for a given complexity level.
func (cc ComplexityConfig) RequireHumanForLevel(level int) bool {
	return level == 3 && cc.Critical.RequireHuman
}

// Workflow is a named sequence of cataractae parsed from a YAML file.
type Workflow struct {
	Name       string               `yaml:"name"`
	Cataractae []WorkflowCataractae `yaml:"cataractae"`
	Complexity ComplexityConfig     `yaml:"complexity"`
}

// ProviderConfig holds the provider block that can appear at the top level of
// AqueductConfig or on an individual RepoConfig. Non-empty fields are applied on
// top of the resolved built-in preset during provider resolution.
type ProviderConfig struct {
	// Name is the built-in preset name (e.g. "claude", "codex") or "custom".
	// Defaults to "claude" when omitted at both levels.
	Name string `yaml:"name,omitempty"`
	// Model is the default model passed via the preset's ModelFlag at launch time.
	// An empty string means "use the preset's own default".
	Model string `yaml:"model,omitempty"`
	// Command overrides the preset's executable (e.g. point at a wrapper script).
	Command string `yaml:"command,omitempty"`
	// Args are extra arguments appended to the preset's fixed args.
	Args []string `yaml:"args,omitempty"`
	// Env maps additional environment variable names to values injected into the
	// agent process alongside the preset's EnvPassthrough list.
	Env map[string]string `yaml:"env,omitempty"`
}

// RepoConfig defines a repository managed by the Castellarius.
type RepoConfig struct {
	Name         string   `yaml:"name"`
	URL          string   `yaml:"url"`
	WorkflowPath string   `yaml:"workflow_path"`
	Cataractae   int      `yaml:"cataractae"`
	Names        []string `yaml:"names,omitempty"`
	Prefix       string   `yaml:"prefix"`
	// Provider overrides the top-level provider config for this repo only.
	Provider *ProviderConfig `yaml:"provider,omitempty"`
}

// DroughtHook defines an action to run when the scheduler enters drought (idle) state.
type DroughtHook struct {
	Name             string `yaml:"name"`
	Action           string `yaml:"action"`                       // built-in: "git_sync", "cataractae_generate", "worktree_prune", "db_vacuum", "events_prune", "tmp_cleanup", "restart_self" | "shell"
	Command          string `yaml:"command,omitempty"`            // only for action: shell
	Timeout          int    `yaml:"timeout_seconds,omitempty"`    // default 30s
	KeepDays         int    `yaml:"keep_days,omitempty"`          // for events_prune: days to retain (default 30)
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

// LLMConfig configures the LLM API backend used for AI-assisted Cistern
// features such as ct droplet add --filter. This is separate from the agent
// CLI provider (Provider), which controls interactive agent sessions.
type LLMConfig struct {
	// Provider is the LLM API provider name.
	// Known values: "anthropic", "openai", "openrouter", "ollama", "custom".
	// Defaults to "anthropic" when omitted.
	Provider string `yaml:"provider,omitempty"`
	// BaseURL overrides the provider's default API base URL.
	// Required when Provider is "custom".
	BaseURL string `yaml:"base_url,omitempty"`
	// Model overrides the provider's default model.
	Model string `yaml:"model,omitempty"`
	// APIKeyEnv is the environment variable name holding the API key.
	// When empty, the provider's standard key variable is used.
	APIKeyEnv string `yaml:"api_key_env,omitempty"`
}

// ArchitectiConfig configures the Architecti autonomous diagnosis agent that
// examines pooled droplets. Architecti is always active as a
// serial queue drainer — no enable flag or threshold required. The scheduler
// enqueues a droplet on every pooled transition (one-to-one guarantee)
// and drains the queue serially in the background.
type ArchitectiConfig struct {
	// MaxFilesPerRun caps the number of files architecti may examine per
	// invocation. Must be > 0.
	MaxFilesPerRun int `yaml:"max_files_per_run"`
}

// AqueductConfig is the top-level configuration for a Cistern instance.
type AqueductConfig struct {
	Repos                 []RepoConfig `yaml:"repos"`
	MaxCataractae         int          `yaml:"max_cataractae,omitempty"` // deprecated: no-op, cap is per-repo (pool size)
	HandoffTokenThreshold int          `yaml:"handoff_token_threshold"`
	RetentionDays         int          `yaml:"retention_days"`
	CleanupInterval       string       `yaml:"cleanup_interval"`
	// HeartbeatInterval controls how often the Castellarius scans in-progress
	// droplets for orphaned or stalled sessions. Accepts Go duration strings
	// (e.g. "30s", "1m"). Defaults to "30s" when empty.
	HeartbeatInterval string        `yaml:"heartbeat_interval,omitempty"`
	DroughtHooks      []DroughtHook `yaml:"drought_hooks,omitempty"`
	// RateLimit configures rate limiting for the delivery cataractae API endpoint.
	// Omit to use the built-in defaults (60 req/min per IP, 120 req/min per token).
	RateLimit *RateLimitConfig `yaml:"rate_limit,omitempty"`
	// DeliveryAddr is the TCP listen address for the delivery cataractae HTTP
	// server (e.g. ":8080"). An empty string disables the HTTP server.
	DeliveryAddr string `yaml:"delivery_addr,omitempty"`
	// Provider sets the default agent provider for all repos. Individual repos
	// may override this with their own provider block.
	// When omitted, the "claude" built-in preset is used.
	Provider *ProviderConfig `yaml:"provider,omitempty"`
	// LLM configures the LLM API backend for AI-assisted features such as
	// ct droplet add --filter. When omitted, the default anthropic preset is used.
	LLM *LLMConfig `yaml:"llm,omitempty"`

	// DrainTimeoutMinutes is the maximum time (in minutes) the Castellarius
	// will wait for in-flight sessions to signal an outcome after receiving
	// SIGTERM. If the timeout fires, exit is forced and stuck IDs are logged.
	// Defaults to 5 when omitted or 0.
	DrainTimeoutMinutes int `yaml:"drain_timeout_minutes,omitempty"`

	// StallThresholdMinutes is the number of minutes of inactivity across all
	// three progress signals (newest note, worktree mtime, session log mtime)
	// before a droplet is considered stalled. Defaults to 45 when absent or 0.
	StallThresholdMinutes int `yaml:"stall_threshold_minutes,omitempty"`

	// DashboardFontFamily is the CSS font-family string used by the Cistern
	// dashboard UI. Defaults to a monospace stack when empty.
	DashboardFontFamily string `yaml:"dashboard_font_family,omitempty"`

	// Architecti configures the autonomous diagnosis agent that examines
	// pooled droplets. Omit to disable.
	Architecti *ArchitectiConfig `yaml:"architecti,omitempty"`
}
