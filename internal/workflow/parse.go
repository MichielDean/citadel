package workflow

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ParseWorkflow reads a YAML file and returns a validated Workflow.
func ParseWorkflow(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading workflow file: %w", err)
	}
	return ParseWorkflowBytes(data)
}

// ParseWorkflowBytes parses YAML bytes into a validated Workflow.
func ParseWorkflowBytes(data []byte) (*Workflow, error) {
	var w Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parsing workflow YAML: %w", err)
	}
	if err := Validate(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

// ParseFarmConfig reads a YAML file and returns a FarmConfig.
func ParseFarmConfig(path string) (*FarmConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading farm config: %w", err)
	}
	var cfg FarmConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing farm config YAML: %w", err)
	}
	if err := ValidateFarmConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GenerateRoleFiles writes CLAUDE.md files for each role defined in the workflow.
// Files are written to <rolesDir>/<roleKey>/CLAUDE.md.
func GenerateRoleFiles(w *Workflow, rolesDir string) ([]string, error) {
	if len(w.Roles) == 0 {
		return nil, nil
	}

	var written []string
	for key, role := range w.Roles {
		dir := filepath.Join(rolesDir, key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, fmt.Errorf("create role dir %s: %w", dir, err)
		}

		content := fmt.Sprintf("# Role: %s\n\n%s\n\n%s\n", role.Name, role.Description, role.Instructions)
		path := filepath.Join(dir, "CLAUDE.md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return written, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	return written, nil
}
