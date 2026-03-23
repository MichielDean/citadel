package cataractae

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichielDean/cistern/internal/provider"
)

// TestProviderCommandStrings is a comprehensive table-driven smoke test for all
// built-in provider presets plus a custom user preset. For each preset it:
//   - Asserts the Command field is the correct binary name
//   - Asserts autonomous Args are present (and appear before -p in the command)
//   - Asserts ModelFlag + model value are appended when a model is set
//   - Asserts AddDirFlag + skillsDir are appended when AddDirFlag is non-empty
//   - Asserts EnvPassthrough contains the expected env var names
//   - Asserts InstructionsFile is the expected filename
func TestProviderCommandStrings(t *testing.T) {
	// Normalise claudePathFn so the claude preset produces a deterministic binary name.
	t.Setenv("CLAUDE_PATH", "claude")

	skillsDir := "/home/user/.cistern/skills"

	tests := []struct {
		name                 string
		preset               provider.ProviderPreset
		wantCommand          string
		wantArgs             []string // args that must appear in preset.Args
		wantEnvPassthrough   []string // env var names that must be in EnvPassthrough
		wantInstructionsFile string
		wantAddDir           bool   // AddDirFlag must appear in the built command
		wantModelFlag        string // non-empty if the preset supports a model flag
	}{
		{
			name:                 "claude",
			preset:               builtinPreset(t, "claude"),
			wantCommand:          "claude",
			wantArgs:             []string{"--dangerously-skip-permissions"},
			wantEnvPassthrough:   []string{"ANTHROPIC_API_KEY"},
			wantInstructionsFile: "CLAUDE.md",
			wantAddDir:           true,
			wantModelFlag:        "--model",
		},
		{
			name:                 "codex",
			preset:               builtinPreset(t, "codex"),
			wantCommand:          "codex",
			wantArgs:             []string{"--dangerously-bypass-approvals-and-sandbox"},
			wantEnvPassthrough:   []string{"OPENAI_API_KEY"},
			wantInstructionsFile: "AGENTS.md",
			wantAddDir:           false,
			wantModelFlag:        "", // codex has no ModelFlag
		},
		{
			name:                 "gemini",
			preset:               builtinPreset(t, "gemini"),
			wantCommand:          "gemini",
			wantArgs:             []string{"--yolo"},
			wantEnvPassthrough:   []string{"GEMINI_API_KEY"},
			wantInstructionsFile: "GEMINI.md",
			wantAddDir:           false,
			wantModelFlag:        "--model",
		},
		{
			name:                 "copilot",
			preset:               builtinPreset(t, "copilot"),
			wantCommand:          "copilot",
			wantArgs:             []string{"--yolo"},
			wantEnvPassthrough:   []string{"GH_TOKEN"},
			wantInstructionsFile: "AGENTS.md",
			wantAddDir:           false,
			wantModelFlag:        "", // copilot has no ModelFlag
		},
		{
			name:                 "opencode",
			preset:               builtinPreset(t, "opencode"),
			wantCommand:          "opencode",
			wantArgs:             []string{},
			wantEnvPassthrough:   nil,
			wantInstructionsFile: "AGENTS.md",
			wantAddDir:           false,
			wantModelFlag:        "",
		},
		{
			name: "custom user preset",
			preset: provider.ProviderPreset{
				Name:             "my-agent",
				Command:          "my-agent-bin",
				Args:             []string{"--auto"},
				EnvPassthrough:   []string{"MY_API_KEY"},
				ModelFlag:        "--model-flag",
				AddDirFlag:       "--context-dir",
				InstructionsFile: "MY_AGENT.md",
			},
			wantCommand:          "my-agent-bin",
			wantArgs:             []string{"--auto"},
			wantEnvPassthrough:   []string{"MY_API_KEY"},
			wantInstructionsFile: "MY_AGENT.md",
			wantAddDir:           true,
			wantModelFlag:        "--model-flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// --- Struct field assertions ---

			if tt.preset.Command != tt.wantCommand {
				t.Errorf("Command = %q, want %q", tt.preset.Command, tt.wantCommand)
			}

			for _, arg := range tt.wantArgs {
				if !slices.Contains(tt.preset.Args, arg) {
					t.Errorf("Args missing %q: got %v", arg, tt.preset.Args)
				}
			}

			for _, env := range tt.wantEnvPassthrough {
				if !slices.Contains(tt.preset.EnvPassthrough, env) {
					t.Errorf("EnvPassthrough missing %q: got %v", env, tt.preset.EnvPassthrough)
				}
			}

			if tt.preset.InstructionsFile != tt.wantInstructionsFile {
				t.Errorf("InstructionsFile = %q, want %q", tt.preset.InstructionsFile, tt.wantInstructionsFile)
			}

			// --- Command string assertions ---

			s := &Session{ID: "test", WorkDir: "/tmp"}
			cmd := s.buildPresetCmd(tt.preset, skillsDir)

			// Binary must be the first token.
			if !strings.HasPrefix(cmd, tt.wantCommand+" ") && cmd != tt.wantCommand {
				t.Errorf("cmd does not start with binary %q:\n  got: %s", tt.wantCommand, cmd)
			}

			// Autonomous args must appear in the command (before -p).
			for _, arg := range tt.wantArgs {
				if !strings.Contains(cmd, arg) {
					t.Errorf("cmd missing autonomous arg %q:\n  got: %s", arg, cmd)
				}
				// Args must appear before the prompt flag.
				argPos := strings.Index(cmd, arg)
				pPos := strings.LastIndex(cmd, " -p ")
				if pPos >= 0 && argPos > pPos {
					t.Errorf("arg %q appears after -p in cmd:\n  got: %s", arg, cmd)
				}
			}

			// AddDir flag + skillsDir must appear when AddDirFlag is set.
			if tt.wantAddDir {
				addDirFlag := tt.preset.AddDirFlag
				wantSubstr := addDirFlag + " '" + skillsDir + "'"
				if !strings.Contains(cmd, wantSubstr) {
					t.Errorf("cmd missing %q:\n  got: %s", wantSubstr, cmd)
				}
				// Must appear before -p.
				addDirPos := strings.Index(cmd, addDirFlag)
				pPos := strings.LastIndex(cmd, " -p ")
				if pPos >= 0 && addDirPos > pPos {
					t.Errorf("AddDirFlag appears after -p in cmd:\n  got: %s", cmd)
				}
			} else if tt.preset.AddDirFlag == "" {
				// When no AddDirFlag, verify skillsDir does not appear.
				if strings.Contains(cmd, skillsDir) {
					t.Errorf("cmd contains skillsDir %q but preset has no AddDirFlag:\n  got: %s", skillsDir, cmd)
				}
			}

			// Model flag + value must appear when wantModelFlag is set.
			if tt.wantModelFlag != "" {
				sWithModel := &Session{ID: "test", WorkDir: "/tmp", Model: "test-model"}
				cmdWithModel := sWithModel.buildPresetCmd(tt.preset, skillsDir)
				wantModelSubstr := tt.wantModelFlag + " test-model"
				if !strings.Contains(cmdWithModel, wantModelSubstr) {
					t.Errorf("cmd missing model flag %q:\n  got: %s", wantModelSubstr, cmdWithModel)
				}
				// Model flag must appear before -p.
				modelPos := strings.Index(cmdWithModel, tt.wantModelFlag)
				pPos := strings.LastIndex(cmdWithModel, " -p ")
				if pPos >= 0 && modelPos > pPos {
					t.Errorf("ModelFlag appears after -p in cmd:\n  got: %s", cmdWithModel)
				}
				// Without a model set, model flag must not appear.
				if strings.Contains(cmd, tt.wantModelFlag) {
					t.Errorf("cmd contains model flag %q even when model is empty:\n  got: %s", tt.wantModelFlag, cmd)
				}
			}
		})
	}
}

// builtinPreset is a test helper that returns the named built-in preset or fails.
func builtinPreset(t *testing.T, name string) provider.ProviderPreset {
	t.Helper()
	for _, p := range provider.Builtins() {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("built-in preset %q not found", name)
	return provider.ProviderPreset{}
}
