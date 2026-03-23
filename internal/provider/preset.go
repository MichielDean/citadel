// Package provider defines ProviderPreset — the data model that describes how
// to launch any agent CLI supported by Cistern.
package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

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
	// PermissionsFlag is the CLI flag used to grant additional permissions.
	PermissionsFlag string `json:"permissions_flag,omitempty"`
	// InstructionsFile is the filename the agent reads for task instructions (e.g. "CLAUDE.md").
	InstructionsFile string `json:"instructions_file,omitempty"`
	// ReadyPromptPrefix is text that signals the agent is ready to receive input.
	ReadyPromptPrefix string `json:"ready_prompt_prefix,omitempty"`
	// ReadyDelayMs is milliseconds to wait after spawn before sending the first prompt.
	ReadyDelayMs int `json:"ready_delay_ms,omitempty"`
	// ResumeFlag is the CLI flag used to resume a previous session (e.g. "--resume").
	ResumeFlag string `json:"resume_flag,omitempty"`
	// ResumeStyle controls whether resuming uses a flag or a positional subcommand.
	ResumeStyle ResumeStyle `json:"resume_style,omitempty"`
}

// builtins is the canonical set of provider presets shipped with Cistern.
var builtins = []ProviderPreset{
	{
		Name:             "claude",
		Command:          "claude",
		Args:             []string{"--dangerously-skip-permissions"},
		EnvPassthrough:   []string{"ANTHROPIC_API_KEY"},
		ModelFlag:        "--model",
		AddDirFlag:       "--add-dir",
		InstructionsFile: "CLAUDE.md",
	},
	{
		Name:             "codex",
		Command:          "codex",
		Args:             []string{"--dangerously-bypass-approvals-and-sandbox"},
		EnvPassthrough:   []string{"OPENAI_API_KEY"},
		InstructionsFile: "AGENTS.md",
		ResumeStyle:      ResumeStyleSubcommand,
	},
	{
		Name:             "gemini",
		Command:          "gemini",
		Args:             []string{"--yolo"},
		EnvPassthrough:   []string{"GEMINI_API_KEY"},
		ModelFlag:        "--model",
		InstructionsFile: "GEMINI.md",
	},
	{
		Name:             "copilot",
		Command:          "copilot",
		Args:             []string{"--yolo"},
		EnvPassthrough:   []string{"GH_TOKEN"},
		InstructionsFile: "AGENTS.md",
		ReadyDelayMs:     5000,
	},
	{
		Name:             "opencode",
		Command:          "opencode",
		InstructionsFile: "AGENTS.md",
	},
}

// Builtins returns a deep copy of the built-in provider preset slice.
// Callers may safely modify the returned slice and its fields without affecting the originals.
func Builtins() []ProviderPreset {
	out := make([]ProviderPreset, len(builtins))
	for i, p := range builtins {
		p.Args = slices.Clone(p.Args)
		p.EnvPassthrough = slices.Clone(p.EnvPassthrough)
		p.ProcessNames = slices.Clone(p.ProcessNames)
		out[i] = p
	}
	return out
}

// LoadUserPresets reads a JSON array of ProviderPreset values from path and
// merges them on top of the built-in presets. A user entry with a Name that
// matches a built-in replaces the built-in; entries with unknown names are
// appended. If path does not exist the built-ins are returned unchanged.
func LoadUserPresets(path string) ([]ProviderPreset, error) {
	presets := Builtins()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return presets, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provider: read %s: %w", path, err)
	}

	var user []ProviderPreset
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("provider: parse %s: %w", path, err)
	}

	for _, u := range user {
		idx := slices.IndexFunc(presets, func(p ProviderPreset) bool {
			return p.Name == u.Name
		})
		if idx >= 0 {
			presets[idx] = u
		} else {
			presets = append(presets, u)
		}
	}

	return presets, nil
}
