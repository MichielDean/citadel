package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
	"github.com/spf13/cobra"
)

// AuditFinding represents one security finding returned by the audit agent.
type AuditFinding struct {
	Title        string `json:"title"`
	Severity     string `json:"severity"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	AttackVector string `json:"attack_vector"`
	Remediation  string `json:"remediation"`
}

// auditSystemPrompt instructs the agent to perform a full-codebase security
// audit and return findings as a JSON array. The agent has full read-only
// access to the repository via --add-dir.
const auditSystemPrompt = `You are a security auditor performing a full-codebase security audit.

Your task: scan the entire repository for systemic security vulnerabilities. Focus on:

1. Authentication & Authorization
   - Missing or incorrect auth checks on endpoints/handlers
   - Privilege escalation and RBAC violations
   - Session handling flaws, JWT issues

2. Injection
   - SQL injection (string concatenation in queries, missing parameterization)
   - Command injection (unsanitized input in exec/system calls)
   - Path traversal (user input in file paths without sanitization)
   - XSS (unescaped user input in HTML/template output)

3. Secrets & Credentials
   - Hardcoded secrets, API keys, passwords, tokens in source
   - Secrets logged to stdout/stderr/files

4. Data Exposure
   - Sensitive fields in API responses
   - Verbose error messages leaking internal state
   - Missing access controls on data queries (IDOR)

5. Resource Safety
   - Unbounded allocations from user-controlled input (DoS vector)
   - Missing rate limiting on authentication endpoints
   - Missing timeouts on external calls

Severity classification:
  blocking   — exploitable in production with material impact (data breach, auth bypass, RCE)
  required   — security weakness that should be fixed (missing validation, weak crypto, IDOR)
  suggestion — defense-in-depth improvement (additional logging, stricter CSP, input length limits)

Use Glob, Grep, and Read tools to explore the codebase thoroughly.

Output ONLY a valid JSON array of findings — no markdown, no explanation, no code fences.
If no findings exist, output an empty array: []

Each finding must have exactly these fields:
[
  {
    "title": "short imperative description (max 72 chars)",
    "severity": "blocking|required|suggestion",
    "file": "path/to/file.go",
    "line": 42,
    "attack_vector": "how an attacker would exploit this",
    "remediation": "specific fix required"
  }
]`

var (
	auditRunRepo     string
	auditRunDryRun   bool
	auditRunModel    string
	auditRunPriority int
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Security audit commands",
}

var auditRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a whole-codebase security audit and file findings as droplets",
	Long: `ct audit run spawns a security audit agent against the full codebase.

The agent has read-only access to the repository and scans for systemic
vulnerabilities: auth surfaces, SQL query patterns, command execution,
secret handling, input validation, IDOR vectors, and rate limiting gaps.

Each finding is filed as a standard cistern droplet so it flows through
the normal pipeline. Use --dry-run to print findings without filing them.`,
	RunE: runAuditRun,
}

func runAuditRun(cmd *cobra.Command, args []string) error {
	if auditRunRepo == "" {
		return fmt.Errorf("--repo is required")
	}

	repo, err := resolveCanonicalRepo(auditRunRepo)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	repoPath := filepath.Join(home, ".cistern", "sandboxes", repo, "_primary")

	if _, statErr := os.Stat(repoPath); os.IsNotExist(statErr) {
		return fmt.Errorf("repo worktree not found at %s — is %q synced?", repoPath, repo)
	}

	preset := resolveFilterPreset(repo)
	if preset.Command == "" {
		return fmt.Errorf("no agent command configured for repo %q", repo)
	}

	fmt.Fprintf(os.Stderr, "Running security audit on %s...\n", repo)

	findings, err := invokeAuditAgent(preset, repoPath, auditRunModel)
	if err != nil {
		return fmt.Errorf("audit agent failed: %w", err)
	}

	if len(findings) == 0 {
		fmt.Println("Audit complete. No findings.")
		return nil
	}

	if auditRunDryRun {
		return printAuditFindings(findings)
	}

	c, err := cistern.New(resolveDBPath(), inferPrefix(repo))
	if err != nil {
		return fmt.Errorf("cistern: %w", err)
	}
	defer c.Close()

	// filedEntry pairs a successfully filed droplet with the severity from the
	// originating finding. This avoids an index mismatch between filed[] and
	// findings[] when c.Add fails for some entries.
	type filedEntry struct {
		droplet  *cistern.Droplet
		severity string
	}
	var filed []filedEntry
	for _, f := range findings {
		desc := auditFindingDescription(f)
		item, addErr := c.Add(repo, f.Title, desc, auditRunPriority, 1)
		if addErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to file finding %q: %v\n", f.Title, addErr)
			continue
		}
		filed = append(filed, filedEntry{droplet: item, severity: f.Severity})
	}

	fmt.Printf("Audit complete. Filed %d finding(s):\n", len(filed))
	for _, ff := range filed {
		fmt.Printf("  %s  %s [%s]\n", ff.droplet.ID, ff.droplet.Title, ff.severity)
	}
	return nil
}

// invokeAuditAgent invokes the configured agent in non-interactive mode with
// full read-only access to repoPath. It returns the parsed list of findings.
// If model is non-empty and the preset defines a ModelFlag, the model override
// is applied.
func invokeAuditAgent(preset provider.ProviderPreset, repoPath, model string) ([]AuditFinding, error) {
	for _, key := range preset.EnvPassthrough {
		if os.Getenv(key) == "" {
			return nil, fmt.Errorf("%s is not set", key)
		}
	}

	// Build args: [Subcommand] [preset.Args...] [ModelFlag model] [--add-dir repoPath]
	//             [--allowedTools Glob,Grep,Read] [PrintFlag] [--output-format json]
	//             [PromptFlag auditSystemPrompt]
	var cmdArgs []string
	if preset.NonInteractive.Subcommand != "" {
		cmdArgs = append(cmdArgs, preset.NonInteractive.Subcommand)
	}
	cmdArgs = append(cmdArgs, preset.Args...)
	if model != "" && preset.ModelFlag != "" {
		cmdArgs = append(cmdArgs, preset.ModelFlag, model)
	}
	if repoPath != "" && preset.AddDirFlag != "" {
		cmdArgs = append(cmdArgs, preset.AddDirFlag, repoPath)
	}
	if preset.NonInteractive.AllowedToolsFlag != "" {
		cmdArgs = append(cmdArgs, preset.NonInteractive.AllowedToolsFlag, "Glob,Grep,Read")
	}
	if preset.NonInteractive.PrintFlag != "" {
		cmdArgs = append(cmdArgs, preset.NonInteractive.PrintFlag)
	}
	cmdArgs = append(cmdArgs, "--output-format", "json")
	if preset.NonInteractive.PromptFlag != "" {
		cmdArgs = append(cmdArgs, preset.NonInteractive.PromptFlag)
	}
	cmdArgs = append(cmdArgs, auditSystemPrompt)

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()

	agentCmd := exec.CommandContext(ctx, preset.Command, cmdArgs...)
	if len(preset.ExtraEnv) > 0 {
		env := os.Environ()
		for k, v := range preset.ExtraEnv {
			env = append(env, k+"="+v)
		}
		agentCmd.Env = env
	}

	out, err := agentCmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("audit agent exec failed (exit %d): %s", ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("audit agent exec failed: %w", err)
	}

	// Try to unwrap a JSON envelope (--output-format json path).
	var envelope claudeJSONOutput
	if jsonErr := json.Unmarshal(out, &envelope); jsonErr == nil {
		if envelope.IsError {
			return nil, fmt.Errorf("audit agent returned error: %s", envelope.Result)
		}
		return extractFindings(envelope.Result)
	}

	// Fallback: parse raw output as findings array.
	return extractFindings(string(out))
}

// extractFindings parses a JSON array of AuditFinding values from agent output.
// It handles JSON embedded in prose or markdown code fences.
// An empty array is valid and returns (nil, nil).
func extractFindings(text string) ([]AuditFinding, error) {
	text = strings.TrimSpace(text)

	// Strip markdown code fences (```json ... ``` or ``` ... ```)
	if idx := strings.Index(text, "```"); idx != -1 {
		after := text[idx+3:]
		if nl := strings.Index(after, "\n"); nl != -1 {
			after = after[nl+1:]
		}
		if end := strings.Index(after, "```"); end != -1 {
			text = strings.TrimSpace(after[:end])
		}
	}

	// Locate the JSON array using bracket depth.
	// inString and escape track whether we are inside a JSON string so that
	// '[' and ']' characters within string values do not affect the depth count.
	start := strings.Index(text, "[")
	if start == -1 {
		return nil, fmt.Errorf("no JSON array found in audit agent response")
	}
	depth := 0
	end := -1
	inString := false
	escape := false
loop:
	for i := start; i < len(text); i++ {
		ch := text[i]
		if escape {
			escape = false
			continue
		}
		if inString {
			if ch == '\\' {
				escape = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
				break loop
			}
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("no JSON array found in audit agent response")
	}

	var findings []AuditFinding
	if err := json.Unmarshal([]byte(text[start:end+1]), &findings); err != nil {
		return nil, fmt.Errorf("failed to parse findings JSON: %w", err)
	}
	return findings, nil
}

// auditFindingDescription formats an AuditFinding into a droplet description
// that includes all structured fields for the remediation pipeline.
func auditFindingDescription(f AuditFinding) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Severity: %s\n", f.Severity)
	if f.File != "" {
		if f.Line > 0 {
			fmt.Fprintf(&sb, "Location: %s:%d\n", f.File, f.Line)
		} else {
			fmt.Fprintf(&sb, "Location: %s\n", f.File)
		}
	}
	if f.AttackVector != "" {
		fmt.Fprintf(&sb, "Attack vector: %s\n", f.AttackVector)
	}
	if f.Remediation != "" {
		fmt.Fprintf(&sb, "Remediation: %s\n", f.Remediation)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// printAuditFindings writes findings to stdout in a human-readable format
// without filing them as droplets (used with --dry-run).
func printAuditFindings(findings []AuditFinding) error {
	fmt.Printf("Audit findings (%d) — dry run, not filed:\n\n", len(findings))
	for i, f := range findings {
		fmt.Printf("%d. [%s] %s\n", i+1, f.Severity, f.Title)
		if f.File != "" {
			if f.Line > 0 {
				fmt.Printf("   Location: %s:%d\n", f.File, f.Line)
			} else {
				fmt.Printf("   Location: %s\n", f.File)
			}
		}
		if f.AttackVector != "" {
			fmt.Printf("   Attack:   %s\n", f.AttackVector)
		}
		if f.Remediation != "" {
			fmt.Printf("   Fix:      %s\n", f.Remediation)
		}
		fmt.Println()
	}
	return nil
}

func init() {
	auditRunCmd.Flags().StringVar(&auditRunRepo, "repo", "", "target repository to audit (required)")
	auditRunCmd.Flags().BoolVar(&auditRunDryRun, "dry-run", false, "print findings without filing droplets")
	auditRunCmd.Flags().StringVar(&auditRunModel, "model", "", "override the default model for the audit session")
	auditRunCmd.Flags().IntVar(&auditRunPriority, "priority", 1, "priority assigned to filed finding droplets (1=high)")
	auditCmd.AddCommand(auditRunCmd)
	rootCmd.AddCommand(auditCmd)
}
