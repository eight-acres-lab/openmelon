package llm

import "fmt"

// New returns a Client for the requested provider.
//
// provider must be one of: "anthropic", "openai", "openrouter".
// apiKey is optional — if empty, the constructor reads the provider's
// canonical env var (ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY).
// defaultModel is optional — if empty, the constructor picks a sensible
// default per provider.
func New(provider, apiKey, defaultModel string) (Client, error) {
	switch provider {
	case "anthropic":
		return NewAnthropic(apiKey, defaultModel)
	case "openai":
		return NewOpenAI(apiKey, defaultModel)
	case "openrouter":
		return NewOpenRouter(apiKey, defaultModel)
	default:
		return nil, fmt.Errorf("llm: unknown provider %q (supported: anthropic, openai, openrouter)", provider)
	}
}
