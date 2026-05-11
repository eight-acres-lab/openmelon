package userconfig

import (
	"os"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

// ResolvedProvider is the effective connection config for one provider.
type ResolvedProvider struct {
	Provider  string
	APIKey    string
	BaseURL   string
	KeySource string
	URLSource string
}

// ResolveProvider resolves provider API key + base URL from project and
// global config, while preserving the existing credentials/env fallback
// behavior.
//
// Precedence:
//   - <project>/.openmelon/project.json:providers.<provider>
//   - ~/.openmelon/config.json:providers.<provider>
//   - <project>/.openmelon/credentials.json api_keys
//   - ~/.openmelon/credentials.json api_keys
//   - environment variables (OPENAI_API_KEY, OPENAI_BASE_URL, etc.)
//
// Empty BaseURL means the provider constructor should use its built-in
// default.
func ResolveProvider(workdir, provider string) ResolvedProvider {
	out := ResolvedProvider{Provider: provider}

	if workdir != "" {
		if p, err := projectx.Load(workdir); err == nil {
			if pc, ok := p.Providers[provider]; ok {
				if pc.APIKey != "" {
					out.APIKey = pc.APIKey
					out.KeySource = "project.config"
				}
				if pc.BaseURL != "" {
					out.BaseURL = pc.BaseURL
					out.URLSource = "project.config"
				}
			}
		}
	}

	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		if pc, ok := cfg.Providers[provider]; ok {
			if out.APIKey == "" && pc.APIKey != "" {
				out.APIKey = pc.APIKey
				out.KeySource = "global.config"
			}
			if out.BaseURL == "" && pc.BaseURL != "" {
				out.BaseURL = pc.BaseURL
				out.URLSource = "global.config"
			}
		}
	}

	if out.APIKey == "" {
		if k, src := ResolveAPIKey(workdir, provider); k != "" {
			out.APIKey = k
			out.KeySource = string(src) + ".credentials"
		}
	}

	if out.APIKey == "" {
		if k := os.Getenv(apiKeyEnv(provider)); k != "" {
			out.APIKey = k
			out.KeySource = "env"
		}
	}
	if out.BaseURL == "" {
		if u := os.Getenv(baseURLEnv(provider)); u != "" {
			out.BaseURL = u
			out.URLSource = "env"
		}
	}

	return out
}

func apiKeyEnv(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	default:
		return "OPENAI_API_KEY"
	}
}

func baseURLEnv(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_BASE_URL"
	case "openrouter":
		return "OPENROUTER_BASE_URL"
	default:
		return "OPENAI_BASE_URL"
	}
}
