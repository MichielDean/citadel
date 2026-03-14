// Package automated implements deterministic workflow steps that run shell
// commands with zero AI tokens. Each step type is a method on Executor that
// takes BeadContext and returns StepOutcome.
//
// Step types:
//   - PRCreate: creates a GitHub PR from bead metadata
//   - CIGate: polls PR checks until all pass or timeout
//   - Merge: squash-merges a PR and verifies the merged state
package automated

import (
	"context"
	"fmt"
	"os/exec"
)

// BeadContext holds bead information needed by automated steps.
type BeadContext struct {
	// ID is the bead identifier.
	ID string

	// Title is the bead title, used as the PR title.
	Title string

	// Description is the bead description, used as the PR body.
	Description string

	// Branch is the head branch for PRs. If empty, detected from WorkDir.
	Branch string

	// BaseBranch is the target branch (e.g., "main").
	BaseBranch string

	// WorkDir is the git repository working directory.
	WorkDir string

	// Metadata holds key-value data from the bead, used to pass data
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
	// The caller is responsible for persisting these to bead metadata.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Result values.
const (
	ResultPass = "pass"
	ResultFail = "fail"
)

// Annotation keys produced by automated steps.
const (
	AnnoPRURL    = "pr_url"
	AnnoPRNumber = "pr_number"
)

// Metadata keys read from BeadContext.Metadata.
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
func (e *Executor) Noop(_ context.Context, bc BeadContext) *StepOutcome {
	return &StepOutcome{
		Result: ResultPass,
		Notes:  fmt.Sprintf("noop: item %s passed through", bc.ID),
	}
}

// RunStep dispatches an automated step by name.
// Unknown step names are treated as noop (passthrough).
func (e *Executor) RunStep(ctx context.Context, stepName string, bc BeadContext) *StepOutcome {
	switch stepName {
	case "noop":
		return e.Noop(ctx, bc)
	case "pr-create":
		out, err := e.PRCreate(ctx, bc)
		if err != nil {
			return &StepOutcome{Result: ResultFail, Notes: fmt.Sprintf("pr-create error: %s", err)}
		}
		return out
	case "ci-gate":
		out, err := e.CIGate(ctx, bc, 0)
		if err != nil {
			return &StepOutcome{Result: ResultFail, Notes: fmt.Sprintf("ci-gate error: %s", err)}
		}
		return out
	case "merge":
		out, err := e.Merge(ctx, bc)
		if err != nil {
			return &StepOutcome{Result: ResultFail, Notes: fmt.Sprintf("merge error: %s", err)}
		}
		return out
	default:
		return e.Noop(ctx, bc)
	}
}

// metaString reads a string value from bead metadata.
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
