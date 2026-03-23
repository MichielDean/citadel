package aqueduct

import (
	"fmt"

	"github.com/MichielDean/cistern/internal/provider"
)

// ResolveProvider returns the effective ProviderPreset for the named repo.
//
// Resolution order: built-in preset → top-level AqueductConfig.Provider overrides
// → repo-specific RepoConfig.Provider overrides.
//
// The preset name is resolved with repo-level taking precedence over top-level,
// which takes precedence over the default ("claude"). The special name "custom"
// starts from an empty preset instead of a built-in.
//
// If repoName does not match any configured repo, only the top-level provider
// config is applied. An empty repoName behaves the same way.
//
// An error is returned if the resolved preset name is not a known built-in and
// is not "custom".
func (cfg *AqueductConfig) ResolveProvider(repoName string) (provider.ProviderPreset, error) {
	// Determine effective preset name: repo level > top level > default.
	name := "claude"
	if cfg.Provider != nil && cfg.Provider.Name != "" {
		name = cfg.Provider.Name
	}

	var repoProvider *ProviderConfig
	for i := range cfg.Repos {
		if cfg.Repos[i].Name == repoName {
			repoProvider = cfg.Repos[i].Provider
			break
		}
	}
	if repoProvider != nil && repoProvider.Name != "" {
		name = repoProvider.Name
	}

	// Resolve the base preset.
	var result provider.ProviderPreset
	if name == "custom" {
		result = provider.ProviderPreset{Name: "custom"}
	} else {
		found := false
		for _, p := range provider.Builtins() {
			if p.Name == name {
				result = p
				found = true
				break
			}
		}
		if !found {
			return provider.ProviderPreset{}, fmt.Errorf("aqueduct: unknown provider %q", name)
		}
	}

	// Apply top-level overrides onto the base preset.
	if cfg.Provider != nil {
		applyProviderOverrides(&result, cfg.Provider)
	}

	// Apply repo-level overrides on top.
	if repoProvider != nil {
		applyProviderOverrides(&result, repoProvider)
	}

	return result, nil
}

// applyProviderOverrides applies non-zero fields from cfg onto the preset p.
//   - A non-empty Command replaces p.Command.
//   - Args are appended to p.Args.
//   - Env entries are merged into p.ExtraEnv; later calls override earlier ones.
//
// The Name field is intentionally not applied here — it is resolved before this
// function is called.
func applyProviderOverrides(p *provider.ProviderPreset, cfg *ProviderConfig) {
	if cfg.Command != "" {
		p.Command = cfg.Command
	}
	if len(cfg.Args) > 0 {
		p.Args = append(p.Args, cfg.Args...)
	}
	for k, v := range cfg.Env {
		if p.ExtraEnv == nil {
			p.ExtraEnv = make(map[string]string)
		}
		p.ExtraEnv[k] = v
	}
}

// ValidateModelForProvider checks whether a workflow cataractae's Model field can
// be used with the given provider preset.
//
// If the preset has no ModelFlag, the model value cannot be passed to the agent
// CLI and will be silently ignored at launch time. This function returns a
// descriptive warning string in that case so callers can surface it to the user.
// An empty return value means no issue was found.
func ValidateModelForProvider(step WorkflowCataractae, preset provider.ProviderPreset) string {
	if step.Model == nil {
		return ""
	}
	if preset.ModelFlag == "" {
		return fmt.Sprintf(
			"cataractae %q: provider %q has no model flag; model %q will be ignored",
			step.Name, preset.Name, *step.Model,
		)
	}
	return ""
}
