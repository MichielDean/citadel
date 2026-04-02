// Package provider defines ProviderPreset — the data model that describes how
// to launch any agent CLI supported by Cistern.
package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
)

// NonInteractiveConfig describes how to invoke an agent CLI in single-shot
// (non-interactive) mode, used by filtration. An empty struct means the preset
// does not define non-interactive invocation.
type NonInteractiveConfig struct {
	// Subcommand is the positional subcommand inserted after Command
	// (e.g. "exec" for codex, "run" for opencode). Empty means no subcommand.
	Subcommand string `json:"subcommand,omitempty"`
	// PrintFlag causes the agent to print its response to stdout and exit
	// (e.g. "--print" for claude). Empty means no flag is needed.
	PrintFlag string `json:"print_flag,omitempty"`
	// PromptFlag is the flag used to pass the combined prompt (e.g. "-p").
	PromptFlag string `json:"prompt_flag,omitempty"`
	// AllowedToolsFlag is the CLI flag used to restrict which tools the agent
	// may call during a non-interactive session (e.g. "--allowedTools" for claude).
	// When non-empty, Cistern appends this flag with "Glob,Grep,Read" so the
	// agent can explore the repository read-only but cannot modify files.
	AllowedToolsFlag string `json:"allowed_tools_flag,omitempty"`
}

// ResumeStyle controls how a resumed session is expressed on the CLI.
type ResumeStyle string

const (
	// ResumeStyleFlag passes the session ID as a flag: --resume=<id>.
	ResumeStyleFlag ResumeStyle = "flag"
	// ResumeStyleSubcommand passes the session ID as a subcommand: <cmd> resume <id>.
	ResumeStyleSubcommand ResumeStyle = "subcommand"
)

// ProviderPreset describes how to launch and interact with a specific agent CLI.
type ProviderPreset struct {
	// Name is the canonical identifier for this provider (e.g. "claude").
	Name string `json:"name"`
	// Command is the executable to invoke (e.g. "claude").
	Command string `json:"command"`
	// Args are fixed arguments always appended to the command.
	Args []string `json:"args,omitempty"`
	// EnvPassthrough lists environment variable names to forward into the agent process.
	EnvPassthrough []string `json:"env_passthrough,omitempty"`
	// ProcessNames lists known OS process names for this agent (used for detection).
	ProcessNames []string `json:"process_names,omitempty"`
	// ModelFlag is the CLI flag used to select the model (e.g. "--model").
	ModelFlag string `json:"model_flag,omitempty"`
	// AddDirFlag is the CLI flag used to add a working directory (e.g. "--add-dir").
	AddDirFlag string `json:"add_dir_flag,omitempty"`
	// PromptFlag is the CLI flag used to pass the prompt to the agent (e.g. "-p").
	// When empty, no prompt flag is appended and the prompt must be delivered via
	// an alternative mechanism (stdin, instructions file, etc.).
	PromptFlag string `json:"prompt_flag,omitempty"`
	// PermissionsFlag is the CLI flag used to grant additional permissions.
	PermissionsFlag string `json:"permissions_flag,omitempty"`
	// InstructionsFile is the filename the agent reads for task instructions (e.g. "CLAUDE.md").
	InstructionsFile string `json:"instructions_file,omitempty"`
	// ReadyPromptPrefix is text that signals the agent is ready to receive input.
	ReadyPromptPrefix string `json:"ready_prompt_prefix,omitempty"`
	// ResumeFlag is the CLI flag used to resume a previous session (e.g. "--resume").
	ResumeFlag string `json:"resume_flag,omitempty"`
	// ResumeStyle controls whether resuming uses a flag or a positional subcommand.
	ResumeStyle ResumeStyle `json:"resume_style,omitempty"`
	// ContinueFlag is the CLI flag used to continue the most recent session in the
	// working directory (e.g. "--continue" for claude). When set, Cistern uses this
	// flag instead of re-injecting the full prompt when resuming a previously-started
	// session. The agent already has full context from the prior run.
	ContinueFlag string `json:"continue_flag,omitempty"`
	// ExtraEnv maps additional environment variable names to values injected into
	// the agent process. These are set in addition to (and may override) EnvPassthrough.
	ExtraEnv map[string]string `json:"extra_env,omitempty"`
	// DefaultModel is the model value passed via ModelFlag when launching the agent.
	// An empty string means the agent's own default is used.
	DefaultModel string `json:"default_model,omitempty"`
	// NonInteractive describes how to invoke this agent in single-shot
	// (non-interactive) mode for filtration.
	NonInteractive NonInteractiveConfig `json:"non_interactive,omitempty"`
	// SupportsAddDir indicates whether this provider supports the AddDirFlag for
	// filesystem-based context injection (e.g. SKILL.md, instructions files).
	// When false, context must be injected as text in the prompt preamble.
	SupportsAddDir bool `json:"supports_add_dir,omitempty"`
}

// InstrFile returns InstructionsFile, defaulting to "CLAUDE.md" when empty.
func (p ProviderPreset) InstrFile() string {
	if p.InstructionsFile != "" {
		return p.InstructionsFile
	}
	return "CLAUDE.md"
}

// builtins is the canonical set of provider presets shipped with Cistern.
var builtins = []ProviderPreset{
	{
		Name:             "claude",
		Command:          "claude",
		Args:             []string{"--dangerously-skip-permissions"},
		ProcessNames:     []string{"claude", "node"},
		ModelFlag:        "--model",
		AddDirFlag:       "--add-dir",
		PromptFlag:       "-p",
		ContinueFlag:     "--continue",
		ResumeFlag:       "--resume",
		InstructionsFile: "CLAUDE.md",
		SupportsAddDir:   true,
		NonInteractive:   NonInteractiveConfig{PrintFlag: "--print", PromptFlag: "-p", AllowedToolsFlag: "--allowedTools"},
	},
	{
		Name:             "codex",
		Command:          "codex",
		Args:             []string{"--dangerously-bypass-approvals-and-sandbox"},
		EnvPassthrough:   []string{"OPENAI_API_KEY"},
		InstructionsFile: "AGENTS.md",
		ResumeStyle:      ResumeStyleSubcommand,
		NonInteractive:   NonInteractiveConfig{Subcommand: "exec", PromptFlag: "-p"},
	},
	{
		Name:             "gemini",
		Command:          "gemini",
		Args:             []string{"--yolo"},
		EnvPassthrough:   []string{"GEMINI_API_KEY"},
		ModelFlag:        "--model",
		InstructionsFile: "GEMINI.md",
		NonInteractive:   NonInteractiveConfig{PromptFlag: "-p"},
	},
	{
		Name:             "copilot",
		Command:          "copilot",
		Args:             []string{"--yolo"},
		PromptFlag:       "-p",
		EnvPassthrough:   []string{"GH_TOKEN"},
		InstructionsFile: "AGENTS.md",
		NonInteractive:   NonInteractiveConfig{PromptFlag: "-p"},
	},
	{
		Name:             "opencode",
		Command:          "opencode",
		InstructionsFile: "AGENTS.md",
		NonInteractive:   NonInteractiveConfig{Subcommand: "run", PromptFlag: "-p"},
	},
}

// cloneSliceFields deep-copies all slice fields of a ProviderPreset so the
// copy does not alias the original's backing arrays.
func cloneSliceFields(p *ProviderPreset) {
	p.Args = slices.Clone(p.Args)
	p.EnvPassthrough = slices.Clone(p.EnvPassthrough)
	p.ProcessNames = slices.Clone(p.ProcessNames)
}

// Builtins returns a deep copy of the built-in provider preset slice.
// Callers may safely modify the returned slice and its fields without affecting the originals.
func Builtins() []ProviderPreset {
	out := make([]ProviderPreset, len(builtins))
	for i, p := range builtins {
		cloneSliceFields(&p)
		p.ExtraEnv = maps.Clone(p.ExtraEnv)
		out[i] = p
	}
	return out
}

// MergePresets applies overrides on top of base and returns the merged slice.
// Entries in overrides that match a base preset by Name replace it; entries
// with unknown names are appended. Neither slice is modified.
func MergePresets(base, overrides []ProviderPreset) []ProviderPreset {
	result := make([]ProviderPreset, len(base))
	for i, p := range base {
		cloneSliceFields(&p)
		result[i] = p
	}
	for _, u := range overrides {
		idx := slices.IndexFunc(result, func(p ProviderPreset) bool {
			return p.Name == u.Name
		})
		cloneSliceFields(&u)
		if idx >= 0 {
			result[idx] = u
		} else {
			result = append(result, u)
		}
	}
	return result
}

// ResolvePreset returns the built-in preset matching name.
// If name is empty or no preset matches, the "claude" preset is returned as
// the default fallback.
func ResolvePreset(name string) ProviderPreset {
	builtins := Builtins()
	for _, p := range builtins {
		if p.Name == name {
			return p
		}
	}
	// Default: fall back to the claude preset by explicit name lookup.
	for _, p := range builtins {
		if p.Name == "claude" {
			return p
		}
	}
	// Unreachable: claude is always in the built-in set.
	return builtins[0]
}

// LoadUserPresets reads a JSON array of ProviderPreset values from path and
// merges them on top of the built-in presets. A user entry with a Name that
// matches a built-in replaces the built-in; entries with unknown names are
// appended. If path does not exist the built-ins are returned unchanged.
func LoadUserPresets(path string) ([]ProviderPreset, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Builtins(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("provider: read %s: %w", path, err)
	}

	var user []ProviderPreset
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("provider: parse %s: %w", path, err)
	}

	return MergePresets(Builtins(), user), nil
}
