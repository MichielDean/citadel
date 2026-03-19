// Package automated implements deterministic workflow steps that run shell
// commands with zero AI tokens. Each step type is a method on Executor that
// takes DropletContext and returns StepOutcome.
//
// Step types:
//   - PRCreate: creates a GitHub PR from droplet metadata
//   - CIGate: polls PR checks until all pass or timeout
//   - Merge: squash-merges a PR and verifies the merged state
package gates

import (
	"context"
	"fmt"
	"os/exec"
)

// DropletContext holds droplet information needed by automated steps.
type DropletContext struct {
	// ID is the droplet identifier.
	ID string

	// Title is the droplet title, used as the PR title.
	Title string

	// Description is the droplet description, used as the PR body.
	Description string

	// Branch is the head branch for PRs. If empty, detected from WorkDir.
	Branch string

	// BaseBranch is the target branch (e.g., "main").
	BaseBranch string

	// WorkDir is the git repository working directory.
	WorkDir string

	// Metadata holds key-value data from the droplet, used to pass data
	// between steps (e.g., pr_url from PRCreate to CIGate).
	Metadata map[string]any
}

// StepOutcome is the result of an automated step execution.
type StepOutcome struct {
	// Result is "pass" or "fail".
	Result string `json:"result"`

	// Notes is a human-readable summary of what happened.
	Notes string `json:"notes"`

	// Annotations holds key-value data produced by the step.
	// The caller is responsible for persisting these to droplet metadata.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Result values.
const (
	ResultPass        = "pass"
	ResultFail        = "fail"
	ResultRecirculate = "recirculate"
)

// Annotation keys produced by automated steps.
const (
	AnnoPRURL    = "pr_url"
	AnnoPRNumber = "pr_number"
)

// Metadata keys read from DropletContext.Metadata.
const (
	MetaPRURL = "pr_url"
)

// ExecFunc is the signature for executing shell commands.
type ExecFunc func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// Executor runs automated steps. The ExecFn field allows injection of a
// test double for shell commands.
type Executor struct {
	ExecFn ExecFunc
}

// New creates an Executor that runs real shell commands.
func New() *Executor {
	return &Executor{ExecFn: DefaultExec}
}

// DefaultExec runs a command via os/exec.
func DefaultExec(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// Noop is a passthrough step that always returns pass.
// Used for smoke testing the queue→scheduler→outcome→close loop.
func (e *Executor) Noop(_ context.Context, bc DropletContext) *StepOutcome {
	return &StepOutcome{
		Result: ResultPass,
		Notes:  fmt.Sprintf("noop: item %s passed through", bc.ID),
	}
}

// RunStep dispatches an automated step by name.
// pr-create, ci-gate, and merge were removed from the pipeline in favour of
// the delivery agent cataracta. They remain available here for testing and
// potential future use, but are not called from the live pipeline.
// Unknown step names are treated as noop (passthrough).
func (e *Executor) RunStep(ctx context.Context, stepName string, bc DropletContext) *StepOutcome {
	switch stepName {
	case "noop":
		return e.Noop(ctx, bc)
	default:
		return e.Noop(ctx, bc)
	}
}

// metaString reads a string value from droplet metadata.
func metaString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
