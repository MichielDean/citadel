package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MichielDean/cistern/internal/cistern"
	"github.com/MichielDean/cistern/internal/provider"
)

// DropletProposal is one proposed droplet from the LLM refinement pass.
type DropletProposal struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Complexity  string   `json:"complexity"`
	DependsOn   []string `json:"depends_on"`
}

const filterSystemPrompt = `You are a software project planning assistant in the Cistern agentic pipeline.

Cistern vocabulary:
  droplet   — a unit of work (like a ticket or story)
  aqueduct  — a workflow pipeline that processes droplets
  cataractae — a gate/step in an aqueduct (e.g., implement, review, test)

Complexity guide:
  standard (1) — single focused feature, straightforward implementation
  full     (2) — multi-part feature or moderate complexity
  critical (3) — breaking change, major refactor, multi-system coordination

When file tools are available, explore the repository before writing proposals:
  - Use Glob to discover the project layout and find relevant files
  - Use Grep to find existing similar commands, data models, or patterns
  - Use Read to read INSTRUCTIONS.md files and understand cataractae conventions
  Grounding proposals in the actual codebase avoids duplicating existing work and
  ensures descriptions reference real schema names, flags, and conventions.

Your task: Given a rough idea (title and optional description), reason carefully about:
  - Scope and acceptance criteria
  - Whether the idea is too large and should be split into multiple focused droplets
  - Appropriate complexity level
  - Dependencies between proposed droplets

Output ONLY a valid JSON array of droplet proposals — no markdown, no explanation, no code fences.
Each proposal object must have exactly these fields:

[
  {
    "title": "short imperative title (max 72 chars)",
    "description": "clear acceptance criteria and key implementation notes",
    "complexity": "standard|full|critical",
    "depends_on": []
  }
]

If the idea naturally decomposes into multiple focused droplets, propose all of them.
In "depends_on", reference the exact "title" value of an earlier droplet in this same
array to express ordering (e.g. if droplet 2 requires droplet 1 to be delivered first).
Keep each droplet focused and deliverable by a single engineer in a reasonable timeframe.`

// runNonInteractive invokes the configured agent binary in non-interactive
// (single-shot) mode to turn a rough idea into well-specified droplet
// proposals. It builds the command from the preset's NonInteractive config,
// passes a combined prompt via PromptFlag, and parses stdout via extractProposals.
func runNonInteractive(preset provider.ProviderPreset, systemPrompt, userPrompt string) ([]DropletProposal, error) {
	// Validate that required env vars from the preset are set.
	for _, key := range preset.EnvPassthrough {
		if os.Getenv(key) == "" {
			return nil, fmt.Errorf("%s is not set", key)
		}
	}

	// Build the combined prompt (system + user).
	combinedPrompt := systemPrompt
	if userPrompt != "" {
		combinedPrompt += "\n\n" + userPrompt
	}

	// Build args: [Subcommand] [preset.Args...] [PrintFlag] [PromptFlag combinedPrompt]
	var args []string
	if preset.NonInteractive.Subcommand != "" {
		args = append(args, preset.NonInteractive.Subcommand)
	}
	args = append(args, preset.Args...)
	if preset.NonInteractive.PrintFlag != "" {
		args = append(args, preset.NonInteractive.PrintFlag)
	}
	if preset.NonInteractive.PromptFlag != "" {
		args = append(args, preset.NonInteractive.PromptFlag)
	}
	args = append(args, combinedPrompt)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, preset.Command, args...)

	// Inherit parent env; append any extra vars from the preset.
	if len(preset.ExtraEnv) > 0 {
		env := os.Environ()
		for k, v := range preset.ExtraEnv {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("agent exec failed (exit %d): %s", ee.ExitCode(), strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("agent exec failed: %w", err)
	}

	return extractProposals(string(out))
}

// extractProposals parses a JSON array of DropletProposals from LLM output.
// It handles JSON embedded in prose or markdown code fences.
func extractProposals(text string) ([]DropletProposal, error) {
	text = strings.TrimSpace(text)

	// Strip markdown code fences (```json ... ``` or ``` ... ```)
	if idx := strings.Index(text, "```"); idx != -1 {
		after := text[idx+3:]
		// Skip language hint line if present
		if nl := strings.Index(after, "\n"); nl != -1 {
			after = after[nl+1:]
		}
		if end := strings.Index(after, "```"); end != -1 {
			text = strings.TrimSpace(after[:end])
		}
	}

	// Locate the JSON array boundaries using bracket depth so trailing text
	// containing ']' (e.g. markdown links) doesn't expand past the real array.
	start := strings.Index(text, "[")
	if start == -1 {
		return nil, fmt.Errorf("no JSON array found in LLM response")
	}
	depth := 0
	end := -1
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("no JSON array found in LLM response")
	}

	var proposals []DropletProposal
	if err := json.Unmarshal([]byte(text[start:end+1]), &proposals); err != nil {
		return nil, fmt.Errorf("failed to parse proposals JSON: %w", err)
	}
	if len(proposals) == 0 {
		return nil, fmt.Errorf("LLM returned no proposals")
	}
	return proposals, nil
}

// complexityToInt converts a complexity name to its integer level (default: 2/full).
func complexityToInt(s string) int {
	cx, err := parseComplexity(s)
	if err != nil {
		return 2
	}
	return cx
}

// addProposals creates cistern droplets for the given proposals (in order),
// wiring up depends_on by matching proposal titles to newly created IDs.
// Each successfully added droplet ID is printed to stdout.
func addProposals(c *cistern.Client, proposals []DropletProposal, repo string, priority int) error {
	titleToID := make(map[string]string)
	for _, p := range proposals {
		cx := complexityToInt(p.Complexity)

		var deps []string
		for _, depTitle := range p.DependsOn {
			if id, ok := titleToID[depTitle]; ok {
				deps = append(deps, id)
			}
		}

		item, err := c.Add(repo, p.Title, p.Description, priority, cx, deps...)
		if err != nil {
			return fmt.Errorf("failed to add %q: %w", p.Title, err)
		}
		titleToID[p.Title] = item.ID
		fmt.Println(item.ID)
	}
	return nil
}

// --- Interactive TUI ---

type filterPhase int

const (
	phaseReview  filterPhase = iota // reviewing proposals one by one
	phaseEdit                       // editing the current proposal's title
	phaseSummary                    // show confirmed list, ask for final confirm
	phaseDone                       // finished
)

type filterModel struct {
	proposals []DropletProposal
	cursor    int         // index of currently viewed proposal
	confirmed []bool      // true = will add, false = skipped
	decided   []bool      // true = user has made a decision for this proposal
	phase     filterPhase
	editBuf   string // buffer for inline title editing
	quitting  bool
}

func newFilterModel(proposals []DropletProposal) filterModel {
	return filterModel{
		proposals: proposals,
		confirmed: make([]bool, len(proposals)),
		decided:   make([]bool, len(proposals)),
	}
}

// confirmedProposals returns only the proposals the user confirmed.
func (m filterModel) confirmedProposals() []DropletProposal {
	var out []DropletProposal
	for i, p := range m.proposals {
		if m.confirmed[i] {
			out = append(out, p)
		}
	}
	return out
}

func (m filterModel) Init() tea.Cmd { return nil }

func (m filterModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseReview:
		return m.updateReview(msg)
	case phaseEdit:
		return m.updateEdit(msg)
	case phaseSummary:
		return m.updateSummary(msg)
	}
	return m, tea.Quit
}

func (m filterModel) updateReview(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q", "ctrl+c":
		m.quitting = true
		m.phase = phaseDone
		return m, tea.Quit

	case "enter", " ":
		// Confirm current proposal
		m.confirmed[m.cursor] = true
		m.decided[m.cursor] = true
		return m.advance()

	case "s":
		// Skip current proposal
		m.confirmed[m.cursor] = false
		m.decided[m.cursor] = true
		return m.advance()

	case "e":
		// Edit title inline
		m.editBuf = m.proposals[m.cursor].Title
		m.phase = phaseEdit
		return m, nil
	}
	return m, nil
}

// advance moves to the next undecided proposal, or to the summary phase.
func (m filterModel) advance() (tea.Model, tea.Cmd) {
	// Find next undecided
	for i := m.cursor + 1; i < len(m.proposals); i++ {
		if !m.decided[i] {
			m.cursor = i
			return m, nil
		}
	}
	// All decided — go to summary
	m.phase = phaseSummary
	return m, nil
}

func (m filterModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "enter":
		newTitle := strings.TrimSpace(m.editBuf)
		if newTitle != "" {
			oldTitle := m.proposals[m.cursor].Title
			m.proposals[m.cursor].Title = newTitle
			// Keep depends_on references consistent when a title is renamed.
			for i := range m.proposals {
				for j, dep := range m.proposals[i].DependsOn {
					if dep == oldTitle {
						m.proposals[i].DependsOn[j] = newTitle
					}
				}
			}
		}
		m.phase = phaseReview
		return m, nil
	case "esc":
		m.phase = phaseReview
		return m, nil
	case "backspace", "ctrl+h":
		if len(m.editBuf) > 0 {
			m.editBuf = m.editBuf[:len(m.editBuf)-1]
		}
	default:
		// Append printable characters
		if len(key.Runes) > 0 {
			m.editBuf += string(key.Runes)
		}
	}
	return m, nil
}

func (m filterModel) updateSummary(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "enter", "y":
		m.phase = phaseDone
		return m, tea.Quit
	case "q", "n", "ctrl+c":
		m.quitting = true
		m.phase = phaseDone
		return m, tea.Quit
	}
	return m, nil
}

var (
	refineStyleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#9db1db"))
	refineStyleLabel   = lipgloss.NewStyle().Faint(true)
	refineStyleBadge   = lipgloss.NewStyle().Bold(true)
	refineStyleKeys    = lipgloss.NewStyle().Faint(true)
	refineStyleConfirm = lipgloss.NewStyle().Foreground(lipgloss.Color("#57d57a"))
	refineStyleSkip    = lipgloss.NewStyle().Faint(true)
)

var complexityColors = map[string]string{
	"standard": "#9db1db",
	"full":     "#f0c86b",
	"critical": "#e06c75",
}

func complexityBadge(cx string) string {
	col, ok := complexityColors[cx]
	if !ok {
		col = "#ffffff"
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(col)).Render(cx)
}

func (m filterModel) View() string {
	if m.phase == phaseDone {
		return ""
	}

	var b strings.Builder

	switch m.phase {
	case phaseReview:
		p := m.proposals[m.cursor]
		n := len(m.proposals)
		b.WriteString(refineStyleHeader.Render(fmt.Sprintf("Proposal %d of %d", m.cursor+1, n)))
		b.WriteString("\n\n")
		b.WriteString(refineStyleLabel.Render("Title:      "))
		b.WriteString(p.Title)
		b.WriteString("\n")
		b.WriteString(refineStyleLabel.Render("Complexity: "))
		b.WriteString(complexityBadge(p.Complexity))
		b.WriteString("\n")
		if len(p.DependsOn) > 0 {
			b.WriteString(refineStyleLabel.Render("Depends on: "))
			b.WriteString(strings.Join(p.DependsOn, ", "))
			b.WriteString("\n")
		}
		if p.Description != "" {
			b.WriteString("\n")
			b.WriteString(refineStyleLabel.Render("Description:\n"))
			// Wrap description to 72 chars
			for _, line := range strings.Split(wordWrap(p.Description, 72), "\n") {
				b.WriteString("  " + line + "\n")
			}
		}
		b.WriteString("\n")
		b.WriteString(refineStyleKeys.Render("Enter=confirm  s=skip  e=edit title  q=quit"))

	case phaseEdit:
		p := m.proposals[m.cursor]
		b.WriteString(refineStyleHeader.Render("Edit title"))
		b.WriteString("\n\n")
		b.WriteString(refineStyleLabel.Render("Current: "))
		b.WriteString(p.Title)
		b.WriteString("\n")
		b.WriteString(refineStyleLabel.Render("New:     "))
		b.WriteString(m.editBuf)
		b.WriteString("█")
		b.WriteString("\n\n")
		b.WriteString(refineStyleKeys.Render("Enter=save  Esc=cancel"))

	case phaseSummary:
		confirmed := m.confirmedProposals()
		if len(confirmed) == 0 {
			b.WriteString(refineStyleSkip.Render("No proposals confirmed. Nothing will be added."))
			b.WriteString("\n\n")
			b.WriteString(refineStyleKeys.Render("Enter=exit  q=exit"))
			return b.String()
		}
		b.WriteString(refineStyleHeader.Render(fmt.Sprintf("Ready to add %d droplet(s):", len(confirmed))))
		b.WriteString("\n\n")
		for i, p := range confirmed {
			b.WriteString(fmt.Sprintf("  %d. %s %s\n",
				i+1,
				p.Title,
				refineStyleLabel.Render("["+p.Complexity+"]"),
			))
		}
		b.WriteString("\n")
		b.WriteString(refineStyleKeys.Render("Enter/y=add all  q/n=cancel"))
	}

	return b.String()
}

// wordWrap wraps text at word boundaries.
func wordWrap(text string, width int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var lines []string
	current := words[0]
	for _, w := range words[1:] {
		if len(current)+1+len(w) <= width {
			current += " " + w
		} else {
			lines = append(lines, current)
			current = w
		}
	}
	lines = append(lines, current)
	return strings.Join(lines, "\n")
}

// runRefineInteractive presents a Bubble Tea TUI for the user to review, edit,
// confirm, or skip each proposal, then adds the confirmed ones.
func runFilterInteractive(c *cistern.Client, proposals []DropletProposal, repo string, priority int) error {
	model := newFilterModel(proposals)
	p := tea.NewProgram(model, tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	final, ok := result.(filterModel)
	if !ok {
		return fmt.Errorf("unexpected TUI result type")
	}
	if final.quitting {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return nil
	}

	confirmed := final.confirmedProposals()
	if len(confirmed) == 0 {
		fmt.Fprintln(os.Stderr, "No proposals confirmed. Nothing added.")
		return nil
	}

	return addProposals(c, confirmed, repo, priority)
}

// runRefineNonInteractive adds all proposals immediately without prompting.
func runFilterNonInteractive(c *cistern.Client, proposals []DropletProposal, repo string, priority int) error {
	return addProposals(c, proposals, repo, priority)
}
