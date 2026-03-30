package aqueduct

import (
	"bytes"
	"log/slog"
	"text/template"
)

// ValidOutcome describes a ct droplet command that is valid for a step.
type ValidOutcome struct {
	Command     string // e.g. "ct droplet pass <id>"
	Description string // e.g. "work complete, advancing to review"
}

// StepTemplateContext holds the per-step variables available under {{.Step}} in templates.
type StepTemplateContext struct {
	// Name is the step identifier (e.g. "implement").
	Name string
	// Position is the 0-based index of this step in the pipeline.
	Position int
	// IsFirst is true when this is the first step in the pipeline.
	IsFirst bool
	// IsLast is true when this is the last step in the pipeline.
	IsLast bool
	// OnPass is the next step name on success, or "done" if last.
	OnPass string
	// OnFail is the fail target, or "pooled" if not configured.
	OnFail string
	// OnRecirculate is the recirculate target, or "" if not configured.
	// Template authors should use {{if .Step.OnRecirculate}} to gate this section.
	OnRecirculate string
	// OnPool is the pool target, or "human" if not configured.
	OnPool string
	// ValidOutcomes lists the ct droplet commands valid for this step.
	// pass is always included; recirculate is included only when OnRecirculate != "".
	ValidOutcomes []ValidOutcome
	// SkippedFor lists complexity levels for which this step is skipped.
	// Currently always empty — per-step complexity skipping is not yet modelled in aqueduct.yaml.
	SkippedFor []string
}

// DropletTemplateContext holds the per-droplet variables available under {{.Droplet}} in templates.
type DropletTemplateContext struct {
	ID          string
	Title       string
	Description string
	Complexity  int
}

// TemplateContext is the top-level data passed to CLAUDE.md template rendering.
// It maps to the three top-level template namespaces: .Step, .Droplet, .Pipeline.
type TemplateContext struct {
	Step    StepTemplateContext
	Droplet DropletTemplateContext
	// Pipeline is the ordered slice of all step names in the workflow.
	Pipeline []string
}

// BuildStepTemplateContext derives a StepTemplateContext from a workflow and a step.
// It computes position, IsFirst/IsLast flags, applies routing defaults, and
// constructs ValidOutcomes based on which routing fields are set.
func BuildStepTemplateContext(wf *Workflow, step *WorkflowCataractae) StepTemplateContext {
	var position int
	var isFirst, isLast bool
	for i, s := range wf.Cataractae {
		if s.Name == step.Name {
			position = i
			isFirst = i == 0
			isLast = i == len(wf.Cataractae)-1
			break
		}
	}

	onPass := step.OnPass
	if onPass == "" {
		onPass = "done"
	}
	onFail := step.OnFail
	if onFail == "" {
		onFail = "pooled"
	}
	onPool := step.OnPool
	if onPool == "" {
		onPool = "human"
	}

	var outcomes []ValidOutcome
	// pass is always valid
	outcomes = append(outcomes, ValidOutcome{
		Command:     "ct droplet pass <id>",
		Description: "work complete, advancing to " + onPass,
	})
	// recirculate only if configured
	if step.OnRecirculate != "" {
		outcomes = append(outcomes, ValidOutcome{
			Command:     "ct droplet recirculate <id>",
			Description: "send back to " + step.OnRecirculate + " with specific findings",
		})
	}
	// pool is always valid
	outcomes = append(outcomes, ValidOutcome{
		Command:     "ct droplet pool <id>",
		Description: "cannot proceed, explain why",
	})

	return StepTemplateContext{
		Name:          step.Name,
		Position:      position,
		IsFirst:       isFirst,
		IsLast:        isLast,
		OnPass:        onPass,
		OnFail:        onFail,
		OnRecirculate: step.OnRecirculate,
		OnPool:        onPool,
		ValidOutcomes: outcomes,
	}
}

// BuildPipeline returns the ordered slice of step names from a workflow.
func BuildPipeline(wf *Workflow) []string {
	names := make([]string, len(wf.Cataractae))
	for i, s := range wf.Cataractae {
		names[i] = s.Name
	}
	return names
}

// RenderTemplate renders content as a Go text/template using ctx as the data.
//
// If the content contains no template markers, it is returned unchanged.
// If template parsing or execution fails, the raw content is returned and a
// warning is logged — template errors must never prevent a session from spawning.
func RenderTemplate(content string, ctx TemplateContext) string {
	tmpl, err := template.New("role").Parse(content)
	if err != nil {
		slog.Default().Warn("template: parse error — using raw content", "error", err)
		return content
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		slog.Default().Warn("template: render error — using raw content", "error", err)
		return content
	}
	return buf.String()
}
