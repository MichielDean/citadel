package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichielDean/bullet-farm/internal/queue"
	"github.com/MichielDean/bullet-farm/internal/workflow"
)

// ContextParams holds everything needed to prepare a step's execution context.
type ContextParams struct {
	Level      workflow.ContextLevel
	SandboxDir string
	Item       *queue.WorkItem
	Step       *workflow.WorkflowStep
	Notes      []queue.StepNote
}

// PrepareContext sets up the working directory for a step based on its context level.
// Returns the directory to use as the working dir, and a cleanup function.
//
// Context levels:
//   - full_codebase: uses the sandbox dir directly (no cleanup needed)
//   - diff_only:     creates a tmpdir with only diff.patch — no repo access
//   - spec_only:     creates a tmpdir with only spec.md — no repo access
func PrepareContext(p ContextParams) (dir string, cleanup func(), err error) {
	noop := func() {}

	switch p.Level {
	case workflow.ContextFullCodebase, "":
		// Write CONTEXT.md into the sandbox root.
		ctxPath := filepath.Join(p.SandboxDir, "CONTEXT.md")
		if err := writeContextFile(ctxPath, p); err != nil {
			return "", noop, err
		}
		return p.SandboxDir, noop, nil

	case workflow.ContextDiffOnly:
		return prepareDiffOnly(p)

	case workflow.ContextSpecOnly:
		return prepareSpecOnly(p)

	default:
		return "", noop, fmt.Errorf("unknown context level: %q", p.Level)
	}
}

// prepareDiffOnly creates a tmpdir containing only diff.patch and CONTEXT.md.
// The agent has no access to the full repo — isolation enforced by filesystem.
func prepareDiffOnly(p ContextParams) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "bf-diff-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create diff tmpdir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	// Generate diff from sandbox.
	diff, err := generateDiff(p.SandboxDir)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}

	diffPath := filepath.Join(tmpDir, "diff.patch")
	if err := os.WriteFile(diffPath, diff, 0644); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write diff.patch: %w", err)
	}

	ctxPath := filepath.Join(tmpDir, "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return tmpDir, cleanup, nil
}

// prepareSpecOnly creates a tmpdir with only spec.md and CONTEXT.md.
// The agent sees only the item description and step notes — no code.
func prepareSpecOnly(p ContextParams) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "bf-spec-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create spec tmpdir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	specPath := filepath.Join(tmpDir, "spec.md")
	spec := buildSpecContent(p.Item)
	if err := os.WriteFile(specPath, []byte(spec), 0644); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write spec.md: %w", err)
	}

	ctxPath := filepath.Join(tmpDir, "CONTEXT.md")
	if err := writeContextFile(ctxPath, p); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return tmpDir, cleanup, nil
}

// writeContextFile writes CONTEXT.md with item info and prior step notes.
func writeContextFile(path string, p ContextParams) error {
	var b strings.Builder

	b.WriteString("# Context\n\n")
	b.WriteString(fmt.Sprintf("## Item: %s\n\n", p.Item.ID))
	b.WriteString(fmt.Sprintf("**Title:** %s\n", p.Item.Title))
	b.WriteString(fmt.Sprintf("**Status:** %s\n", p.Item.Status))
	b.WriteString(fmt.Sprintf("**Priority:** %d\n", p.Item.Priority))
	if p.Item.Assignee != "" {
		b.WriteString(fmt.Sprintf("**Assignee:** %s\n", p.Item.Assignee))
	}
	b.WriteString("\n")

	if p.Item.Description != "" {
		b.WriteString("### Description\n\n")
		b.WriteString(p.Item.Description)
		b.WriteString("\n\n")
	}

	b.WriteString(fmt.Sprintf("## Current Step: %s\n\n", p.Step.Name))
	b.WriteString(fmt.Sprintf("- **Type:** %s\n", p.Step.Type))
	if p.Step.Role != "" {
		b.WriteString(fmt.Sprintf("- **Role:** %s\n", p.Step.Role))
	}
	if p.Step.Context != "" {
		b.WriteString(fmt.Sprintf("- **Context:** %s\n", p.Step.Context))
	}
	if p.Step.MaxIterations > 0 {
		b.WriteString(fmt.Sprintf("- **Max iterations:** %d\n", p.Step.MaxIterations))
	}
	b.WriteString("\n")

	if len(p.Notes) > 0 {
		b.WriteString("## Prior Step Notes\n\n")
		for _, n := range p.Notes {
			if n.StepName != "" {
				b.WriteString(fmt.Sprintf("### From: %s\n\n", n.StepName))
			}
			b.WriteString(n.Content)
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Output\n\n")
	b.WriteString("Write your result to `outcome.json` in this directory when done.\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"result\": \"pass|fail|revision|escalate\",\n")
	b.WriteString("  \"notes\": \"explanation of what you did or found\",\n")
	b.WriteString("  \"annotations\": {}\n")
	b.WriteString("}\n")
	b.WriteString("```\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// generateDiff captures all committed changes on the item's feature branch vs
// origin/main. The implementer is required to commit before writing outcome.json,
// so this will always produce a non-empty diff for a completed implementation.
func generateDiff(sandboxDir string) ([]byte, error) {
	cmd := exec.Command("git", "diff", "origin/main...HEAD")
	cmd.Dir = sandboxDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff in %s: %w", sandboxDir, err)
	}
	return out, nil
}

// buildSpecContent creates a markdown spec from the item description.
func buildSpecContent(item *queue.WorkItem) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# %s\n\n", item.Title))
	b.WriteString(fmt.Sprintf("**ID:** %s\n", item.ID))
	b.WriteString(fmt.Sprintf("**Priority:** %d\n\n", item.Priority))
	if item.Description != "" {
		b.WriteString("## Description\n\n")
		b.WriteString(item.Description)
		b.WriteString("\n\n")
	}
	return b.String()
}
