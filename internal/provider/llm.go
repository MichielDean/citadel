// Package provider defines ProviderPreset — the data model that describes how
// to launch any agent CLI supported by Cistern.
package provider

// LLMProvider describes a backing LLM API endpoint used for Cistern's refine
// and other AI-assisted workflows. It is separate from ProviderPreset, which
// describes the agent CLI tool launched per session.
type LLMProvider struct {
	// Name is the canonical identifier (e.g. "anthropic").
	Name string
	// BaseURL is the default base URL for API calls (e.g. "https://api.anthropic.com").
	BaseURL string
	// ApiKeyEnv is the environment variable name that holds the API key.
	// Empty for providers that do not require a key (e.g. local Ollama).
	ApiKeyEnv string
	// DefaultModel is the model name sent in requests when none is configured.
	DefaultModel string
}

// llmBuiltins is the canonical set of built-in LLM provider configurations.
var llmBuiltins = []LLMProvider{
	{
		Name:         "anthropic",
		BaseURL:      "https://api.anthropic.com",
		ApiKeyEnv:    "ANTHROPIC_API_KEY",
		DefaultModel: "claude-haiku-4-5-20251001",
	},
	{
		Name:         "openai",
		BaseURL:      "https://api.openai.com",
		ApiKeyEnv:    "OPENAI_API_KEY",
		DefaultModel: "gpt-4o",
	},
	{
		Name:         "openrouter",
		BaseURL:      "https://openrouter.ai/api/v1",
		ApiKeyEnv:    "OPENROUTER_API_KEY",
		DefaultModel: "openai/gpt-4o",
	},
	{
		Name:         "ollama",
		BaseURL:      "http://localhost:11434",
		ApiKeyEnv:    "",
		DefaultModel: "llama3",
	},
}

// LLMBuiltins returns a copy of the built-in LLM provider configurations.
// Callers may safely modify the returned slice without affecting the originals.
func LLMBuiltins() []LLMProvider {
	out := make([]LLMProvider, len(llmBuiltins))
	copy(out, llmBuiltins)
	return out
}
