package cataracta

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichielDean/cistern/internal/aqueduct"
	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/skills"
)

// xmlEscape returns s with XML special characters escaped so it is safe to
// embed inside XML element content. This prevents prompt injection via
// crafted skill names or SKILL.md descriptions.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		// EscapeText only fails on invalid UTF-8; fall back to raw string.
		return s
	}
	return buf.String()
}

// ContextParams holds everything needed to prepare a step's execution context.
type ContextParams struct {
	Level      aqueduct.ContextLevel
	SandboxDir string
	Item       *cistern.Droplet
	Step       *aqueduct.WorkflowCataracta
	Notes      []cistern.CataractaNote
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
	case aqueduct.ContextFullCodebase, "":
		// Write CONTEXT.md into the sandbox root.
		ctxPath := filepath.Join(p.SandboxDir, "CONTEXT.md")
		if err := writeContextFile(ctxPath, p); err != nil {
			return "", noop, err
		}
		return p.SandboxDir, noop, nil

	case aqueduct.ContextDiffOnly:
		return prepareDiffOnly(p)

	case aqueduct.ContextSpecOnly:
		return prepareSpecOnly(p)

	default:
		return "", noop, fmt.Errorf("unknown context level: %q", p.Level)
	}
}

// prepareDiffOnly creates a tmpdir containing only diff.patch and CONTEXT.md.
// The agent has no access to the full repo — isolation enforced by filesystem.
func prepareDiffOnly(p ContextParams) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "ct-diff-*")
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
	tmpDir, err := os.MkdirTemp("", "ct-spec-*")
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

	// Surface revision notes at the very top when this is a recirculation.
	// Agents anchor to the first thing they read — fixes must be unmissable.
	revisionNotes := revisionCycleNotes(p.Notes)
	if len(revisionNotes) > 0 {
		b.WriteString("## ⚠️ REVISION REQUIRED — Fix these issues before anything else\n\n")
		b.WriteString("This droplet was recirculated. The following issues were found and **must** be fixed.\n")
		b.WriteString("Do not proceed to implementation until you have read and understood each issue.\n\n")
		for i, n := range revisionNotes {
			b.WriteString(fmt.Sprintf("### Issue %d (from: %s)\n\n", i+1, n.CataractaName))
			b.WriteString(n.Content)
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
	}

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
	if p.Step.Identity != "" {
		b.WriteString(fmt.Sprintf("- **Role:** %s\n", p.Step.Identity))
	}
	if p.Step.Context != "" {
		b.WriteString(fmt.Sprintf("- **Context:** %s\n", p.Step.Context))
	}

	b.WriteString("\n")

	if len(p.Notes) > 0 {
		b.WriteString("## All Prior Step Notes\n\n")
		for _, n := range p.Notes {
			if n.CataractaName != "" {
				b.WriteString(fmt.Sprintf("### From: %s\n\n", n.CataractaName))
			}
			b.WriteString(n.Content)
			b.WriteString("\n\n")
		}
	}

	if len(p.Step.Skills) > 0 {
		b.WriteString("<available_skills>\n")
		for _, skill := range p.Step.Skills {
			b.WriteString("  <skill>\n")
			b.WriteString(fmt.Sprintf("    <name>%s</name>\n", xmlEscape(skill.Name)))
			b.WriteString(fmt.Sprintf("    <description>%s</description>\n", xmlEscape(skillDescription(skill.Name))))
			b.WriteString(fmt.Sprintf("    <location>.claude/skills/%s/SKILL.md</location>\n", xmlEscape(skill.Name)))
			b.WriteString("  </skill>\n")
		}
		b.WriteString("</available_skills>\n\n")
	}

	b.WriteString("## Signaling Completion\n\n")
	b.WriteString("When your work is done, signal your outcome using the `ct` CLI:\n\n")
	b.WriteString("**Pass (work complete, move to next step):**\n")
	b.WriteString(fmt.Sprintf("    ct droplet pass %s\n\n", p.Item.ID))
	b.WriteString("**Recirculate (needs rework — send back upstream):**\n")
	b.WriteString(fmt.Sprintf("    ct droplet recirculate %s\n", p.Item.ID))
	b.WriteString(fmt.Sprintf("    ct droplet recirculate %s --to implement\n\n", p.Item.ID))
	b.WriteString("**Block (genuinely blocked, cannot proceed):**\n")
	b.WriteString(fmt.Sprintf("    ct droplet block %s\n\n", p.Item.ID))
	b.WriteString("Add notes before signaling:\n")
	b.WriteString(fmt.Sprintf("    ct droplet note %s \"What you did / found\"\n\n", p.Item.ID))
	b.WriteString("The `ct` binary is on your PATH.\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// skillDescription reads the cached SKILL.md for name and returns the first
// non-heading, non-empty line as a brief description. Falls back to name.
func skillDescription(name string) string {
	data, err := os.ReadFile(skills.LocalPath(name))
	if err != nil {
		return name
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return name
}

// generateDiff captures all committed changes on the item's feature branch vs
// origin/main. The implementer is required to commit before signaling pass,
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
func buildSpecContent(item *cistern.Droplet) string {
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

// revisionCycleNotes returns the notes from the most recent recirculate cycle —
// i.e. all notes appended since the last "pass" or "block" note from a cataracta.
// These are surfaced at the top of CONTEXT.md so the implementer sees them first.
func revisionCycleNotes(notes []cistern.CataractaNote) []cistern.CataractaNote {
	// Walk backwards to find the start of the latest recirculate cycle.
	// A new cycle begins after any note whose content starts with "pass" or contains "No issues".
	var cycle []cistern.CataractaNote
	for i := len(notes) - 1; i >= 0; i-- {
		n := notes[i]
		content := strings.TrimSpace(n.Content)
		isPassSignal := strings.HasPrefix(strings.ToLower(content), "no issues") ||
			strings.HasPrefix(strings.ToLower(content), "fix already in place") ||
			strings.HasPrefix(strings.ToLower(content), "all") ||
			strings.HasPrefix(strings.ToLower(content), "implemented") ||
			strings.HasPrefix(strings.ToLower(content), "manually verified")
		if isPassSignal {
			break
		}
		// Prepend so order is oldest-first within the cycle.
		cycle = append([]cistern.CataractaNote{n}, cycle...)
	}
	// Only return notes from reviewer/security/qa cataractae — not implementer self-notes.
	var filtered []cistern.CataractaNote
	for _, n := range cycle {
		name := strings.ToLower(n.CataractaName)
		if strings.Contains(name, "review") || strings.Contains(name, "qa") || strings.Contains(name, "security") {
			filtered = append(filtered, n)
		}
	}
	return filtered
}
