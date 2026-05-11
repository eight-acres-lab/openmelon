package llm

import (
	"fmt"
	"os"
)

// New returns a Client for the requested provider.
//
// provider must be one of: "anthropic", "openai", "openrouter", "auto".
// apiKey, baseURL, defaultModel are all optional — empty values fall back
// to the provider's canonical env vars (e.g. OPENAI_API_KEY +
// OPENAI_BASE_URL) and built-in defaults.
//
// "auto" picks based on which API key the user has set in the
// environment — preferring Anthropic when both are set, since Claude is
// stronger at the structured-output task that the agent loop uses.
// Falls back to openai → openrouter. Returns an error with a helpful
// message if no recognized key is found.
func New(provider, apiKey, baseURL, defaultModel string) (Client, error) {
	if provider == "auto" || provider == "" {
		provider = autoDetectProvider()
		if provider == "" {
			return nil, fmt.Errorf(
				"llm: --llm=auto could not pick a provider — set one of " +
					"ANTHROPIC_API_KEY / OPENAI_API_KEY / OPENROUTER_API_KEY, " +
					"or pass --llm <provider> explicitly")
		}
	}
	switch provider {
	case "anthropic":
		return NewAnthropic(apiKey, baseURL, defaultModel)
	case "openai":
		return NewOpenAI(apiKey, baseURL, defaultModel)
	case "openrouter":
		return NewOpenRouter(apiKey, baseURL, defaultModel)
	default:
		return nil, fmt.Errorf("llm: unknown provider %q (supported: anthropic, openai, openrouter, auto)", provider)
	}
}

// autoDetectProvider picks a provider from the environment.
//
// Order of preference: anthropic → openai → openrouter. The thinking is
// that Claude is the strongest at structured-JSON output today, but if a
// user has only an OpenAI key, this gracefully degrades rather than
// erroring.
func autoDetectProvider() string {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return "anthropic"
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return "openai"
	}
	if os.Getenv("OPENROUTER_API_KEY") != "" {
		return "openrouter"
	}
	return ""
}
