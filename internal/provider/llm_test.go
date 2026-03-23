package provider

import "testing"

// TestLLMProviderDefaults verifies that each built-in LLM provider has the
// correct BaseURL and ApiKeyEnv without any cistern.yaml or env vars present.
func TestLLMProviderDefaults(t *testing.T) {
	tests := []struct {
		name          string
		wantBaseURL   string
		wantApiKeyEnv string
	}{
		{
			name:          "anthropic",
			wantBaseURL:   "https://api.anthropic.com",
			wantApiKeyEnv: "ANTHROPIC_API_KEY",
		},
		{
			name:          "openai",
			wantBaseURL:   "https://api.openai.com",
			wantApiKeyEnv: "OPENAI_API_KEY",
		},
		{
			name:          "openrouter",
			wantBaseURL:   "https://openrouter.ai/api/v1",
			wantApiKeyEnv: "OPENROUTER_API_KEY",
		},
		{
			name:          "ollama",
			wantBaseURL:   "http://localhost:11434",
			wantApiKeyEnv: "", // local — no key required
		},
	}

	providers := LLMBuiltins()
	byName := make(map[string]LLMProvider, len(providers))
	for _, p := range providers {
		byName[p.Name] = p
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := byName[tt.name]
			if !ok {
				t.Fatalf("LLMBuiltins() missing provider %q", tt.name)
			}
			if p.BaseURL != tt.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", p.BaseURL, tt.wantBaseURL)
			}
			if p.ApiKeyEnv != tt.wantApiKeyEnv {
				t.Errorf("ApiKeyEnv = %q, want %q", p.ApiKeyEnv, tt.wantApiKeyEnv)
			}
		})
	}
}

// TestLLMBuiltins_ReturnsAllFourProviders verifies the expected set of built-in
// LLM providers is present.
func TestLLMBuiltins_ReturnsAllFourProviders(t *testing.T) {
	want := []string{"anthropic", "openai", "openrouter", "ollama"}
	providers := LLMBuiltins()

	byName := make(map[string]bool)
	for _, p := range providers {
		byName[p.Name] = true
	}
	for _, name := range want {
		if !byName[name] {
			t.Errorf("LLMBuiltins() missing provider %q", name)
		}
	}
}

// TestLLMBuiltins_ReturnsCopy verifies that mutating the returned slice does
// not affect the built-ins.
func TestLLMBuiltins_ReturnsCopy(t *testing.T) {
	first := LLMBuiltins()
	first[0].BaseURL = "mutated"

	second := LLMBuiltins()
	if second[0].BaseURL == "mutated" {
		t.Error("LLMBuiltins() returned a reference to internal state, want an independent copy")
	}
}

// TestLLMProviderDefaults_DefaultModelNonEmpty verifies every built-in LLM
// provider has a non-empty DefaultModel.
func TestLLMProviderDefaults_DefaultModelNonEmpty(t *testing.T) {
	for _, p := range LLMBuiltins() {
		if p.DefaultModel == "" {
			t.Errorf("provider %q has empty DefaultModel", p.Name)
		}
	}
}
