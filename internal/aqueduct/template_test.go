package aqueduct

import (
	"strings"
	"testing"
)

// --- RenderTemplate tests ---

// TestRenderTemplate_ImplementerWithoutRecirculate_RecirculateSectionAbsent verifies
// that when a step has no OnRecirculate, the {{if .Step.OnRecirculate}} block is
// absent from the rendered output and the else-branch is shown instead.
func TestRenderTemplate_ImplementerWithoutRecirculate_RecirculateSectionAbsent(t *testing.T) {
	wf := &Workflow{
		Name: "feature",
		Cataractae: []WorkflowCataractae{
			{Name: "implement", Type: CataractaeTypeAgent, Identity: "implementer",
				OnPass: "review", OnFail: "pooled"},
			{Name: "review", Type: CataractaeTypeAgent, Identity: "reviewer",
				OnPass: "done", OnRecirculate: "implement"},
		},
	}
	step := &wf.Cataractae[0] // implement — no OnRecirculate

	content := `## Outcomes
{{if .Step.OnRecirculate}}
- ct droplet recirculate {{.Droplet.ID}} — send back to {{.Step.OnRecirculate}} with specific findings
{{else}}
(recirculate is not valid for this step — there is no upstream to return to)
{{end}}
- ct droplet pass {{.Droplet.ID}} — work complete, advancing to {{.Step.OnPass}}`

	ctx := TemplateContext{
		Step:     BuildStepTemplateContext(wf, step),
		Droplet:  DropletTemplateContext{ID: "ct-abc123", Title: "test item"},
		Pipeline: BuildPipeline(wf),
	}

	result := RenderTemplate(content, ctx)

	if strings.Contains(result, "ct droplet recirculate ct-abc123") {
		t.Error("rendered output should not contain recirculate command when OnRecirculate is empty")
	}
	if !strings.Contains(result, "recirculate is not valid for this step") {
		t.Error("rendered output should contain 'recirculate is not valid' message")
	}
	if !strings.Contains(result, "ct droplet pass ct-abc123") {
		t.Error("rendered output should contain pass command with droplet ID")
	}
	if !strings.Contains(result, "advancing to review") {
		t.Error("rendered output should contain OnPass target 'review'")
	}
}

// TestRenderTemplate_ReviewerWithRecirculate_RecirculateSectionPresent verifies
// that when a step has OnRecirculate set, the recirculate section is included
// in the rendered output with the correct target step name.
func TestRenderTemplate_ReviewerWithRecirculate_RecirculateSectionPresent(t *testing.T) {
	wf := &Workflow{
		Name: "feature",
		Cataractae: []WorkflowCataractae{
			{Name: "implement", Type: CataractaeTypeAgent, Identity: "implementer",
				OnPass: "review", OnFail: "pooled"},
			{Name: "review", Type: CataractaeTypeAgent, Identity: "reviewer",
				OnPass: "done", OnRecirculate: "implement"},
		},
	}
	step := &wf.Cataractae[1] // review — OnRecirculate: "implement"

	content := `## Outcomes
{{if .Step.OnRecirculate}}
- ct droplet recirculate {{.Droplet.ID}} — send back to {{.Step.OnRecirculate}} with specific findings
{{else}}
(recirculate is not valid for this step — there is no upstream to return to)
{{end}}
- ct droplet pass {{.Droplet.ID}} — work complete, advancing to {{.Step.OnPass}}`

	ctx := TemplateContext{
		Step:     BuildStepTemplateContext(wf, step),
		Droplet:  DropletTemplateContext{ID: "ct-def456", Title: "test item"},
		Pipeline: BuildPipeline(wf),
	}

	result := RenderTemplate(content, ctx)

	if !strings.Contains(result, "ct droplet recirculate ct-def456") {
		t.Error("rendered output should contain recirculate command when OnRecirculate is set")
	}
	if !strings.Contains(result, "send back to implement") {
		t.Error("rendered output should contain OnRecirculate target 'implement'")
	}
	if strings.Contains(result, "recirculate is not valid for this step") {
		t.Error("rendered output should not contain 'recirculate is not valid' when recirculate IS valid")
	}
}

// TestRenderTemplate_PipelineContainsAllStepNamesInOrder verifies that {{.Pipeline}}
// contains all step names from the workflow in their definition order.
func TestRenderTemplate_PipelineContainsAllStepNamesInOrder(t *testing.T) {
	wf := &Workflow{
		Name: "feature",
		Cataractae: []WorkflowCataractae{
			{Name: "implement", Type: CataractaeTypeAgent, Identity: "implementer", OnPass: "review"},
			{Name: "review", Type: CataractaeTypeAgent, Identity: "reviewer", OnPass: "qa"},
			{Name: "qa", Type: CataractaeTypeAgent, Identity: "qa", OnPass: "done"},
		},
	}
	step := &wf.Cataractae[0]

	content := `Pipeline: {{range .Pipeline}}{{.}} {{end}}`

	ctx := TemplateContext{
		Step:     BuildStepTemplateContext(wf, step),
		Droplet:  DropletTemplateContext{ID: "ct-xyz"},
		Pipeline: BuildPipeline(wf),
	}

	// Verify the pipeline slice itself
	if len(ctx.Pipeline) != 3 {
		t.Fatalf("pipeline has %d steps, want 3", len(ctx.Pipeline))
	}
	if ctx.Pipeline[0] != "implement" {
		t.Errorf("pipeline[0] = %q, want %q", ctx.Pipeline[0], "implement")
	}
	if ctx.Pipeline[1] != "review" {
		t.Errorf("pipeline[1] = %q, want %q", ctx.Pipeline[1], "review")
	}
	if ctx.Pipeline[2] != "qa" {
		t.Errorf("pipeline[2] = %q, want %q", ctx.Pipeline[2], "qa")
	}

	// Verify the rendered output contains all names
	result := RenderTemplate(content, ctx)
	for _, name := range ctx.Pipeline {
		if !strings.Contains(result, name) {
			t.Errorf("rendered pipeline should contain %q", name)
		}
	}
}

// --- BuildStepTemplateContext tests ---

func TestBuildStepTemplateContext_Position_IsFirstIsLast(t *testing.T) {
	wf := &Workflow{
		Name: "test",
		Cataractae: []WorkflowCataractae{
			{Name: "first", Type: CataractaeTypeAgent, Identity: "impl", OnPass: "second"},
			{Name: "second", Type: CataractaeTypeAgent, Identity: "rev", OnPass: "last"},
			{Name: "last", Type: CataractaeTypeAgent, Identity: "qa", OnPass: "done"},
		},
	}

	first := BuildStepTemplateContext(wf, &wf.Cataractae[0])
	if !first.IsFirst {
		t.Error("first step should have IsFirst=true")
	}
	if first.IsLast {
		t.Error("first step should have IsLast=false")
	}
	if first.Position != 0 {
		t.Errorf("first step Position = %d, want 0", first.Position)
	}

	middle := BuildStepTemplateContext(wf, &wf.Cataractae[1])
	if middle.IsFirst {
		t.Error("middle step should have IsFirst=false")
	}
	if middle.IsLast {
		t.Error("middle step should have IsLast=false")
	}
	if middle.Position != 1 {
		t.Errorf("middle step Position = %d, want 1", middle.Position)
	}

	last := BuildStepTemplateContext(wf, &wf.Cataractae[2])
	if last.IsFirst {
		t.Error("last step should have IsFirst=false")
	}
	if !last.IsLast {
		t.Error("last step should have IsLast=true")
	}
	if last.Position != 2 {
		t.Errorf("last step Position = %d, want 2", last.Position)
	}
}

func TestBuildStepTemplateContext_RoutingDefaults(t *testing.T) {
	// When OnFail/OnPool are not set, defaults should be applied.
	wf := &Workflow{
		Name: "test",
		Cataractae: []WorkflowCataractae{
			{Name: "step", Type: CataractaeTypeGate, OnPass: "done"},
		},
	}
	ctx := BuildStepTemplateContext(wf, &wf.Cataractae[0])

	if ctx.OnPass != "done" {
		t.Errorf("OnPass = %q, want %q", ctx.OnPass, "done")
	}
	if ctx.OnFail != "pooled" {
		t.Errorf("OnFail = %q, want %q", ctx.OnFail, "pooled")
	}
	if ctx.OnPool != "human" {
		t.Errorf("OnPool = %q, want %q", ctx.OnPool, "human")
	}
	if ctx.OnRecirculate != "" {
		t.Errorf("OnRecirculate = %q, want empty", ctx.OnRecirculate)
	}
}

func TestBuildStepTemplateContext_ValidOutcomes_RecirculateOnlyWhenConfigured(t *testing.T) {
	wf := &Workflow{
		Name: "test",
		Cataractae: []WorkflowCataractae{
			{Name: "impl", Type: CataractaeTypeAgent, Identity: "implementer",
				OnPass: "review", OnFail: "pooled"},
			{Name: "review", Type: CataractaeTypeAgent, Identity: "reviewer",
				OnPass: "done", OnRecirculate: "impl"},
		},
	}

	// Implementer: no recirculate configured
	implCtx := BuildStepTemplateContext(wf, &wf.Cataractae[0])
	for _, o := range implCtx.ValidOutcomes {
		if strings.Contains(o.Command, "recirculate") {
			t.Error("implementer ValidOutcomes should not contain recirculate when OnRecirculate is empty")
		}
	}

	// Reviewer: recirculate is configured
	revCtx := BuildStepTemplateContext(wf, &wf.Cataractae[1])
	hasRecirculate := false
	for _, o := range revCtx.ValidOutcomes {
		if strings.Contains(o.Command, "recirculate") {
			hasRecirculate = true
			if !strings.Contains(o.Description, "impl") {
				t.Errorf("recirculate description should mention target 'impl', got %q", o.Description)
			}
		}
	}
	if !hasRecirculate {
		t.Error("reviewer ValidOutcomes should contain recirculate when OnRecirculate is set")
	}
}

func TestBuildStepTemplateContext_ValidOutcomes_PoolAlwaysPresent(t *testing.T) {
	wf := &Workflow{
		Name: "test",
		Cataractae: []WorkflowCataractae{
			{Name: "impl", Type: CataractaeTypeAgent, Identity: "implementer",
				OnPass: "review", OnFail: "pooled"},
			{Name: "review", Type: CataractaeTypeAgent, Identity: "reviewer",
				OnPass: "done", OnPool: "human-review"},
		},
	}

	// Implementer: pool is always present even when OnPool is not configured.
	implCtx := BuildStepTemplateContext(wf, &wf.Cataractae[0])
	hasPool := false
	for _, o := range implCtx.ValidOutcomes {
		if strings.Contains(o.Command, "pool") {
			hasPool = true
		}
	}
	if !hasPool {
		t.Error("implementer ValidOutcomes should always contain pool")
	}

	// Reviewer: pool is present with command.
	revCtx := BuildStepTemplateContext(wf, &wf.Cataractae[1])
	hasPool = false
	for _, o := range revCtx.ValidOutcomes {
		if strings.Contains(o.Command, "pool") {
			hasPool = true
		}
	}
	if !hasPool {
		t.Error("reviewer ValidOutcomes should contain pool when OnPool is set")
	}
}

// TestRenderTemplate_StaticContent_PassesThrough verifies backward compatibility:
// files with no template markers are returned unchanged.
func TestRenderTemplate_StaticContent_PassesThrough(t *testing.T) {
	content := "# Role: Implementer\n\nThis is plain text with no template markers."
	ctx := TemplateContext{
		Step:     StepTemplateContext{Name: "implement"},
		Droplet:  DropletTemplateContext{ID: "ct-123"},
		Pipeline: []string{"implement"},
	}

	result := RenderTemplate(content, ctx)
	if result != content {
		t.Errorf("static content should pass through unchanged, got %q", result)
	}
}

// TestRenderTemplate_BadSyntax_FallsBackToRawContent verifies that a template
// parse error returns the raw content (never fails the spawn).
func TestRenderTemplate_BadSyntax_FallsBackToRawContent(t *testing.T) {
	content := "Hello {{.Step.Name} unclosed"
	ctx := TemplateContext{
		Step:    StepTemplateContext{Name: "implement"},
		Droplet: DropletTemplateContext{ID: "ct-123"},
	}

	result := RenderTemplate(content, ctx)
	if result != content {
		t.Errorf("bad template syntax should return raw content, got %q", result)
	}
}

// TestBuildPipeline_EmptyWorkflow returns empty slice for workflow with no steps.
func TestBuildPipeline_EmptyWorkflow(t *testing.T) {
	wf := &Workflow{Name: "empty", Cataractae: []WorkflowCataractae{}}
	pipeline := BuildPipeline(wf)
	if len(pipeline) != 0 {
		t.Errorf("empty workflow pipeline should have 0 entries, got %d", len(pipeline))
	}
}

// TestRenderTemplate_DropletFields verifies droplet template variables are rendered.
func TestRenderTemplate_DropletFields(t *testing.T) {
	wf := &Workflow{
		Name:       "test",
		Cataractae: []WorkflowCataractae{{Name: "s", Type: CataractaeTypeGate, OnPass: "done"}},
	}
	content := `ID={{.Droplet.ID}} Title={{.Droplet.Title}} Complexity={{.Droplet.Complexity}}`
	ctx := TemplateContext{
		Step: BuildStepTemplateContext(wf, &wf.Cataractae[0]),
		Droplet: DropletTemplateContext{
			ID:         "ct-999",
			Title:      "My Feature",
			Complexity: 2,
		},
		Pipeline: BuildPipeline(wf),
	}

	result := RenderTemplate(content, ctx)
	if !strings.Contains(result, "ID=ct-999") {
		t.Errorf("result should contain ID, got %q", result)
	}
	if !strings.Contains(result, "Title=My Feature") {
		t.Errorf("result should contain Title, got %q", result)
	}
	if !strings.Contains(result, "Complexity=2") {
		t.Errorf("result should contain Complexity, got %q", result)
	}
}
