package cataractae

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log/slog"
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

// reviewedCommitRecorder is satisfied by *cistern.Client and allows recording
// the HEAD commit when a review diff is generated, without importing the full
// cistern package into the context params type signature.
type reviewedCommitRecorder interface {
	SetLastReviewedCommit(dropletID, commitHash string) error
}

// ContextParams holds everything needed to prepare a step's execution context.
type ContextParams struct {
	Level      aqueduct.ContextLevel
	SandboxDir string
	Item       *cistern.Droplet
	Step       *aqueduct.WorkflowCataractae
	Notes      []cistern.CataractaeNote
	// OpenIssues is the list of open droplet_issues for this droplet.
	// For reviewer cataractae, these drive the Phase 1 verification list.
	OpenIssues []cistern.DropletIssue
	// QueueClient is used to record the HEAD commit hash after generating a
	// diff_only context. Optional — if nil, no recording is performed.
	QueueClient reviewedCommitRecorder
	// Logger is used to log non-fatal errors (e.g. SetLastReviewedCommit failures).
	// If nil, slog.Default() is used.
	Logger *slog.Logger
}

func (p ContextParams) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
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

	// Record the HEAD commit so the scheduler can detect phantom commits
	// (implement pass without any new commits since the last review).
	if p.QueueClient != nil {
		if head, err := currentHead(p.SandboxDir); err == nil {
			if err := p.QueueClient.SetLastReviewedCommit(p.Item.ID, head); err != nil {
				p.logger().Warn("context: SetLastReviewedCommit failed", "droplet", p.Item.ID, "error", err)
			}
		}
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

	b.WriteString(fmt.Sprintf("## Item: %s\n\n", p.Item.ID))
	b.WriteString(fmt.Sprintf("**Title:** %s\n", p.Item.Title))
	b.WriteString(fmt.Sprintf("**Status:** %s\n", p.Item.Status))
	b.WriteString(fmt.Sprintf("**Priority:** %d\n", p.Item.Priority))
	if p.Item.Assignee != "" {
		b.WriteString(fmt.Sprintf("**Assignee:** %s\n", p.Item.Assignee))
	}
	if p.Item.ExternalRef != "" {
		b.WriteString(fmt.Sprintf("**External Ref:** %s\n", p.Item.ExternalRef))
	}
	b.WriteString("\n")

	if p.Item.Description != "" {
		b.WriteString("### Description\n\n")
		b.WriteString(p.Item.Description)
		b.WriteString("\n\n")
	}

	if p.Level == aqueduct.ContextFullCodebase || p.Level == "" {
		if dirty, err := uncommittedFiles(p.SandboxDir); err == nil && len(dirty) > 0 {
			b.WriteString("### Uncommitted Files from Prior Session\n\n")
			b.WriteString("The worktree has uncommitted changes from a prior agent session.\n")
			b.WriteString("You MUST commit these changes before making new ones.\n")
			b.WriteString("Review the changes, ensure they are correct, then commit with a descriptive message.\n\n")
			for _, f := range dirty {
				b.WriteString(fmt.Sprintf("- `%s`\n", f))
			}
			b.WriteString("\n")
		}
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

	isReviewer := isReviewerCataractae(p.Step)
	revisionNotes := revisionCycleNotes(p.Notes, p.Step)

	// Partition open issues: own (flagged by this cataractae) vs other cataractae.
	// Fix ci-0y5ha: Phase 1 must only contain own issues; foreign issues are read-only context.
	var ownIssues, otherIssues []cistern.DropletIssue
	for _, iss := range p.OpenIssues {
		if iss.FlaggedBy == p.Step.Name || (p.Step.Identity != "" && iss.FlaggedBy == p.Step.Identity) {
			ownIssues = append(ownIssues, iss)
		} else {
			otherIssues = append(otherIssues, iss)
		}
	}

	if isReviewer && len(ownIssues) > 0 {
		// Reviewer with own DB-tracked open issues: two-phase structure.
		// Phase 1 is evidence-based verification — no opinions, just grep/test results.
		// Phase 2 is a clean fresh review of the diff for new issues.
		b.WriteString("## ⚠️ TWO-PHASE REVIEW — Read carefully before doing anything\n\n")
		b.WriteString("This droplet was recirculated after a prior review. You have TWO distinct jobs:\n\n")
		b.WriteString("### Phase 1 — Verify prior issues are resolved\n\n")
		b.WriteString("For EACH issue below, run the exact check (grep, test, cat) and call:\n")
		b.WriteString("- `ct droplet issue resolve <id> --evidence \"<command + output>\"` — if fixed\n")
		b.WriteString("- `ct droplet issue reject <id> --evidence \"<command + output>\"` — if still present\n\n")
		b.WriteString("No opinions. No pattern-matching from memory. Run the command. Paste the output.\n")
		b.WriteString("If you cannot verify with a command, state what you checked and what you found.\n\n")
		for i, iss := range ownIssues {
			b.WriteString(fmt.Sprintf("#### Issue %d — %s (flagged by: %s)\n\n", i+1, iss.ID, iss.FlaggedBy))
			b.WriteString(iss.Description)
			b.WriteString("\n\n")
		}
		b.WriteString("### Phase 2 — Fresh review of new changes\n\n")
		b.WriteString("After completing Phase 1, do a full adversarial review of the diff for NEW issues.\n")
		b.WriteString("Do NOT re-examine issues from Phase 1 — they are already handled.\n")
		b.WriteString("For each new finding: `ct droplet issue add " + p.Item.ID + " \"<description>\"`\n")
		b.WriteString("Treat this as a clean review of a fresh diff.\n\n")
		b.WriteString("---\n\n")
	} else if isReviewer && len(revisionNotes) > 0 {
		// Fallback: reviewer with free-text notes but no DB issues (legacy path).
		b.WriteString("## ⚠️ TWO-PHASE REVIEW — Read carefully before doing anything\n\n")
		b.WriteString("This droplet was recirculated after a prior review. You have TWO distinct jobs:\n\n")
		b.WriteString("### Phase 1 — Verify prior issues are resolved\n\n")
		b.WriteString("For EACH issue below, run the exact check (grep, test, cat) and output:\n")
		b.WriteString("- `RESOLVED: <evidence>` — paste the command and output proving it is fixed\n")
		b.WriteString("- `UNRESOLVED: <evidence>` — paste the command and output proving it is still present\n\n")
		b.WriteString("No opinions. No pattern-matching from memory. Run the command. Paste the output.\n")
		b.WriteString("If you cannot verify with a command, state what you checked and what you found.\n\n")
		for i, n := range revisionNotes {
			b.WriteString(fmt.Sprintf("#### Prior Issue %d (flagged by: %s)\n\n", i+1, n.CataractaeName))
			b.WriteString(n.Content)
			b.WriteString("\n\n")
		}
		b.WriteString("### Phase 2 — Fresh review of new changes\n\n")
		b.WriteString("After completing Phase 1, do a full adversarial review of the diff for NEW issues.\n")
		b.WriteString("Do NOT re-examine issues from Phase 1 — they are already handled.\n")
		b.WriteString("Treat this as a clean review of a fresh diff.\n\n")
		b.WriteString("---\n\n")
	} else if !isReviewer && len(revisionNotes) > 0 {
		// Implementer/QA with prior issues: surface fixes at the top.
		b.WriteString("## ⚠️ REVISION REQUIRED — Fix these issues before anything else\n\n")
		b.WriteString("This droplet was recirculated. The following issues were found and **must** be fixed.\n")
		b.WriteString("Do not proceed to implementation until you have read and understood each issue.\n\n")
		for i, n := range revisionNotes {
			b.WriteString(fmt.Sprintf("### Issue %d (from: %s)\n\n", i+1, n.CataractaeName))
			b.WriteString(n.Content)
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
	}

	// Background section: open issues from other cataractae — for reviewer steps only.
	// These are shown for context; the current reviewer must NOT resolve or reject them.
	// Fix ci-0y5ha: foreign issues are never mixed into Phase 1.
	if isReviewer && len(otherIssues) > 0 {
		b.WriteString("## Background — Open Issues from Other Cataractae (read-only)\n\n")
		b.WriteString("These issues were flagged by other cataractae. Do NOT resolve or reject them —\n")
		b.WriteString("they are provided for context only. Only the cataractae that flagged them can verify them.\n\n")
		for i, iss := range otherIssues {
			b.WriteString(fmt.Sprintf("### [Background] Issue %d — %s (flagged by: %s)\n\n", i+1, iss.ID, iss.FlaggedBy))
			b.WriteString(iss.Description)
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
	}

	// Partition notes in one pass:
	//   ownNotes       — same cataractae only (avoids anchoring on unrelated stages)
	//   manualNotes    — operator annotations via `ct droplet note` (never step-filtered)
	//   schedulerNotes — scheduler system notes (zombie detection, timeouts, etc.)
	var ownNotes, manualNotes, schedulerNotes []cistern.CataractaeNote
	for _, n := range p.Notes {
		switch n.CataractaeName {
		case "manual":
			manualNotes = append(manualNotes, n)
		case "scheduler":
			schedulerNotes = append(schedulerNotes, n)
		default:
			if n.CataractaeName == p.Step.Name || (p.Step.Identity != "" && n.CataractaeName == p.Step.Identity) {
				ownNotes = append(ownNotes, n)
			}
		}
	}
	if len(ownNotes) > 4 {
		ownNotes = ownNotes[:4]
	}
	writeNoteSection := func(heading string, notes []cistern.CataractaeNote) {
		if len(notes) == 0 {
			return
		}
		b.WriteString(heading + "\n\n")
		for _, n := range notes {
			b.WriteString(n.Content)
			b.WriteString("\n\n")
		}
	}
	writeNoteSection("## Recent Step Notes", ownNotes)
	writeNoteSection("## Manual Notes", manualNotes)
	writeNoteSection("## Scheduler Notes", schedulerNotes)

	injected := injectedSkillsForIdentity(cataractaeDirFn(p.SandboxDir), p.Step.Identity)
	if len(injected) > 0 || len(p.Step.Skills) > 0 {
		b.WriteString("<available_skills>\n")
		writeSkill := func(name, desc, loc string) {
			b.WriteString("  <skill>\n")
			b.WriteString(fmt.Sprintf("    <name>%s</name>\n", xmlEscape(name)))
			b.WriteString(fmt.Sprintf("    <description>%s</description>\n", xmlEscape(desc)))
			b.WriteString(fmt.Sprintf("    <location>%s</location>\n", xmlEscape(loc)))
			b.WriteString("  </skill>\n")
		}
		// Injected skills (from the identity's local skills/ dir) appear first.
		for _, sk := range injected {
			writeSkill(sk.Name, readSkillDescription(sk.Path), sk.Path)
		}
		// Then YAML-configured global skills.
		for _, skill := range p.Step.Skills {
			writeSkill(skill.Name, skillDescription(skill.Name), skills.LocalPath(skill.Name))
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
	b.WriteString("**Pool (cannot currently proceed):**\n")
	b.WriteString(fmt.Sprintf("    ct droplet pool %s\n\n", p.Item.ID))
	b.WriteString("Add notes before signaling:\n")
	b.WriteString(fmt.Sprintf("    ct droplet note %s \"What you did / found\"\n\n", p.Item.ID))
	b.WriteString("The `ct` binary is on your PATH.\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}

// cataractaeDirFn returns the cataractae directory for the given sandbox dir.
// Overridable in tests.
var cataractaeDirFn = func(sandboxDir string) string {
	return filepath.Join(sandboxDir, "cataractae")
}

// injectedSkillEntry holds the name and absolute path of a skill injected into a
// cataractae identity's local skills/ directory.
type injectedSkillEntry struct {
	Name string
	Path string
}

// injectedSkillsForIdentity scans <cataractaeDir>/<identity>/skills/ for skill directories
// that contain SKILL.md. Returns nil when identity is empty or the directory does not exist.
func injectedSkillsForIdentity(cataractaeDir, identity string) []injectedSkillEntry {
	if identity == "" {
		return nil
	}
	skillsDir := filepath.Join(cataractaeDir, identity, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var result []injectedSkillEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillPath := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		if _, statErr := os.Stat(skillPath); statErr != nil {
			continue
		}
		result = append(result, injectedSkillEntry{Name: e.Name(), Path: skillPath})
	}
	return result
}

// readSkillDescription reads the first non-empty, non-heading line from a SKILL.md
// at path as a brief description. YAML frontmatter (lines between the opening and
// closing --- delimiters at the top of the file) is skipped before scanning.
// Falls back to filepath.Base(filepath.Dir(path)) (the skill directory name) when
// the file is absent or contains only headings/frontmatter.
func readSkillDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return filepath.Base(filepath.Dir(path))
	}
	lines := strings.Split(string(data), "\n")
	inFrontmatter := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed
	}
	return filepath.Base(filepath.Dir(path))
}

// skillDescription reads the cached SKILL.md for name and returns the first
// non-heading, non-empty line as a brief description. Falls back to name.
func skillDescription(name string) string {
	return readSkillDescription(skills.LocalPath(name))
}

// uncommittedFiles returns a list of modified/staged files (excluding CONTEXT.md,
// .current-stage, and untracked files) in the given directory. Returns nil on error.
func uncommittedFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		xy := line[:2]
		if xy == "??" {
			continue
		}
		name := strings.TrimSpace(line[3:])
		if name != "CONTEXT.md" && name != ".current-stage" {
			files = append(files, name)
		}
	}
	return files, nil
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
// i.e. all notes appended since the last "pass" or "pool" note from a cataractae.
// These are surfaced at the top of CONTEXT.md so the step sees them first.
//
// step controls which notes are returned after the cycle boundary is found:
//   - If step is a reviewer cataractae: only notes whose CataractaeName exactly
//     matches step.Name or step.Identity are returned. This prevents security from
//     seeing QA's notes and vice versa (fix ci-0y5ha).
//   - Otherwise: notes from any reviewer-like cataractae (containing "review",
//     "qa", or "security") are returned — implementers need to see review feedback.
func revisionCycleNotes(notes []cistern.CataractaeNote, step *aqueduct.WorkflowCataractae) []cistern.CataractaeNote {
	// Walk newest-to-oldest to find the start of the latest recirculate cycle.
	// A new cycle begins after any note whose content starts with "pass" or contains "No issues".
	// Notes are ordered newest-first (DESC), so we iterate forward.
	var cycle []cistern.CataractaeNote
	for _, n := range notes {
		lower := strings.ToLower(strings.TrimSpace(n.Content))
		isPassSignal := strings.HasPrefix(lower, "no issues") ||
			strings.HasPrefix(lower, "fix already in place") ||
			strings.HasPrefix(lower, "all good") ||
			strings.HasPrefix(lower, "all clear") ||
			strings.HasPrefix(lower, "all tests pass") ||
			strings.HasPrefix(lower, "all checks pass") ||
			strings.HasPrefix(lower, "implemented") ||
			strings.HasPrefix(lower, "manually verified")
		if isPassSignal {
			break
		}
		cycle = append(cycle, n)
	}
	// Reverse to oldest-first order (notes arrive newest-first).
	for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
		cycle[i], cycle[j] = cycle[j], cycle[i]
	}
	var filtered []cistern.CataractaeNote
	if step != nil && isReviewerCataractae(step) {
		// Fix ci-0y5ha: reviewer cataractae only see their own prior notes — not other
		// reviewers'. Match by step name or identity (notes may be stored under either).
		for _, n := range cycle {
			if n.CataractaeName == step.Name || (step.Identity != "" && n.CataractaeName == step.Identity) {
				filtered = append(filtered, n)
			}
		}
	} else {
		// Non-reviewer (e.g. implementer): include notes from any reviewer-like cataractae
		// so that implementers see the review feedback they need to fix.
		for _, n := range cycle {
			name := strings.ToLower(n.CataractaeName)
			if strings.Contains(name, "review") || strings.Contains(name, "qa") || strings.Contains(name, "security") {
				filtered = append(filtered, n)
			}
		}
	}
	return filtered
}

// isReviewerCataractae returns true if the step is a review or QA cataractae —
// i.e. one that should use the two-phase verification protocol.
func isReviewerCataractae(step *aqueduct.WorkflowCataractae) bool {
	if step == nil {
		return false
	}
	name := strings.ToLower(step.Name)
	identity := strings.ToLower(step.Identity)
	return strings.Contains(name, "review") || strings.Contains(name, "qa") ||
		strings.Contains(identity, "review") || strings.Contains(identity, "qa") ||
		strings.Contains(name, "security") || strings.Contains(identity, "security")
}
