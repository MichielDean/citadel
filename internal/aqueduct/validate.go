package aqueduct

import (
	"fmt"
	"strings"
)

var validCataractaeTypes = map[CataractaeType]bool{
	CataractaeTypeAgent:     true,
	CataractaeTypeAutomated: true,
	CataractaeTypeGate:      true,
	CataractaeTypeHuman:     true,
}

var validContextLevels = map[ContextLevel]bool{
	ContextFullCodebase: true,
	ContextDiffOnly:     true,
	ContextSpecOnly:     true,
}

// Validate checks a Workflow for structural errors.
func Validate(w *Workflow) error {
	if w.Name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if len(w.Cataractae) == 0 {
		return fmt.Errorf("workflow %q has no cataractae", w.Name)
	}

	cataractaeNames := make(map[string]bool, len(w.Cataractae))
	for _, s := range w.Cataractae {
		if s.Name == "" {
			return fmt.Errorf("workflow %q: cataractae name is required", w.Name)
		}
		if cataractaeNames[s.Name] {
			return fmt.Errorf("workflow %q: duplicate cataractae name %q", w.Name, s.Name)
		}
		cataractaeNames[s.Name] = true
	}

	for _, s := range w.Cataractae {
		if err := validateCataractae(w, s, cataractaeNames); err != nil {
			return err
		}
	}

	if err := checkCircularRoutes(w); err != nil {
		return err
	}

	return nil
}

func validateCataractae(w *Workflow, s WorkflowCataractae, cataractaeNames map[string]bool) error {
	// Default type to agent if not specified.
	if s.Type == "" {
		s.Type = CataractaeTypeAgent
	}

	if !validCataractaeTypes[s.Type] {
		return fmt.Errorf("workflow %q cataractae %q: unknown type %q", w.Name, s.Name, s.Type)
	}

	if s.Context != "" && !validContextLevels[s.Context] {
		return fmt.Errorf("workflow %q cataractae %q: unknown context %q", w.Name, s.Name, s.Context)
	}

	if s.Model != nil && strings.TrimSpace(*s.Model) == "" {
		return fmt.Errorf("workflow %q cataractae %q: model must be a non-empty string when set", w.Name, s.Name)
	}

	// Validate cataractae references in routing fields.
	for _, ref := range cataractaeRefs(s) {
		if ref.target == "" {
			continue
		}
		if !isTerminal(ref.target) && !cataractaeNames[ref.target] {
			return fmt.Errorf("workflow %q cataractae %q: %s references unknown cataractae %q", w.Name, s.Name, ref.field, ref.target)
		}
	}

	return nil
}

type cataractaeRef struct {
	field  string
	target string
}

func cataractaeRefs(s WorkflowCataractae) []cataractaeRef {
	return []cataractaeRef{
		{"on_pass", s.OnPass},
		{"on_fail", s.OnFail},
		{"on_recirculate", s.OnRecirculate},
		{"on_pool", s.OnPool},
	}
}

// isTerminal returns true for built-in terminal states that are not step names.
func isTerminal(name string) bool {
	switch strings.ToLower(name) {
	case "done", "pooled", "human", "pool":
		return true
	}
	return false
}

// ValidateAqueductConfig checks a AqueductConfig for structural errors.
func ValidateAqueductConfig(cfg *AqueductConfig) error {
	if len(cfg.Repos) == 0 {
		return fmt.Errorf("cistern config: at least one repo is required")
	}

	repoNames := make(map[string]bool, len(cfg.Repos))
	prefixes := make(map[string]string, len(cfg.Repos))

	for i, repo := range cfg.Repos {
		if repo.Name == "" {
			return fmt.Errorf("cistern config: repo[%d] name is required", i)
		}
		if repoNames[repo.Name] {
			return fmt.Errorf("cistern config: duplicate repo name %q", repo.Name)
		}
		repoNames[repo.Name] = true

		if repo.Prefix != "" {
			if other, ok := prefixes[repo.Prefix]; ok {
				return fmt.Errorf("cistern config: repos %q and %q share prefix %q", other, repo.Name, repo.Prefix)
			}
			prefixes[repo.Prefix] = repo.Name
		}

		// Determine effective cataractae count.
		cataractae := repo.Cataractae
		if len(repo.Names) > 0 {
			if cataractae > 0 && cataractae != len(repo.Names) {
				return fmt.Errorf("cistern config: repo %q: cataractae=%d but names has %d entries", repo.Name, cataractae, len(repo.Names))
			}
			cataractae = len(repo.Names)
		}
		if cataractae <= 0 {
			return fmt.Errorf("cistern config: repo %q: cataractae must be > 0", repo.Name)
		}
	}

	if a := cfg.Architecti; a != nil {
		if a.MaxFilesPerRun <= 0 {
			return fmt.Errorf("cistern config: architecti.max_files_per_run must be > 0")
		}
	}

	return nil
}

// checkCircularRoutes detects dead-end cycles: groups of steps where no step
// has any route to a terminal state. Intentional loops (e.g., implement ->
// review -> implement) are allowed as long as some path exits the cycle.
func checkCircularRoutes(w *Workflow) error {
	// A step "can terminate" if it has any route to a terminal state, or if it
	// has a route to another step that can terminate. We compute this via
	// backward propagation from terminal-reachable steps.

	cataractaeSet := make(map[string]bool, len(w.Cataractae))
	// routes maps step name -> all targets (including terminals).
	routes := make(map[string][]string, len(w.Cataractae))
	for _, s := range w.Cataractae {
		cataractaeSet[s.Name] = true
		for _, ref := range cataractaeRefs(s) {
			if ref.target != "" {
				routes[s.Name] = append(routes[s.Name], ref.target)
			}
		}
	}

	// Mark steps that can reach a terminal. Start with steps that directly
	// route to a terminal, then propagate backward.
	canTerminate := make(map[string]bool, len(w.Cataractae))

	// Seed: steps with at least one terminal route.
	for name, targets := range routes {
		for _, t := range targets {
			if isTerminal(t) {
				canTerminate[name] = true
				break
			}
		}
	}

	// Also seed steps with no routes at all (implicit terminal — step just stops).
	for _, s := range w.Cataractae {
		if len(routes[s.Name]) == 0 {
			canTerminate[s.Name] = true
		}
	}

	// Reverse adjacency for backward propagation.
	revAdj := make(map[string][]string, len(w.Cataractae))
	for name, targets := range routes {
		for _, t := range targets {
			if cataractaeSet[t] {
				revAdj[t] = append(revAdj[t], name)
			}
		}
	}

	// BFS backward from terminable steps.
	queue := make([]string, 0, len(canTerminate))
	for name := range canTerminate {
		queue = append(queue, name)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, pred := range revAdj[cur] {
			if !canTerminate[pred] {
				canTerminate[pred] = true
				queue = append(queue, pred)
			}
		}
	}

	// Any step that cannot reach a terminal is part of a dead-end cycle.
	for _, s := range w.Cataractae {
		if !canTerminate[s.Name] {
			return fmt.Errorf("workflow %q: circular route detected: step %q has no path to a terminal state", w.Name, s.Name)
		}
	}

	return nil
}
