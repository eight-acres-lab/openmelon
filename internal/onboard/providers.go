package onboard

// providers.go — public re-exposes of the provider/preset metadata so
// the TUI's /model and /model-image selectors can render the same
// curated lists the auth wizard uses.

// Preset is one curated model id with a one-line description.
type Preset struct {
	ID       string
	Subtitle string
}

// ProviderInfo is the public view of one provider known to openmelon.
// Mirrors the internal providerOption — kept narrow on purpose so we
// can extend providerOption later without breaking the public API.
type ProviderInfo struct {
	Slug              string
	DefaultLLMModel   string
	DefaultImageModel string
	// ImageProvider is the slug used for the image-gen client. Often
	// equal to Slug; "" means the provider has no image support.
	ImageProvider string
	LLMPresets    []Preset
	ImagePresets  []Preset
}

// Providers returns every provider openmelon knows how to talk to,
// in the canonical order shown in the auth wizard.
func Providers() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(providerOptions))
	for _, p := range providerOptions {
		out = append(out, toPublic(p))
	}
	return out
}

// ProviderBySlug looks up a provider by its slug ("openrouter" /
// "openai" / "anthropic"). Returns ok=false if not registered.
func ProviderBySlug(slug string) (ProviderInfo, bool) {
	for _, p := range providerOptions {
		if p.slug == slug {
			return toPublic(p), true
		}
	}
	return ProviderInfo{}, false
}

func toPublic(p providerOption) ProviderInfo {
	out := ProviderInfo{
		Slug:              p.slug,
		DefaultLLMModel:   p.defaultLLMModel,
		DefaultImageModel: p.defaultImgModel,
		ImageProvider:     p.imgProvider,
	}
	for _, ms := range p.llmPresets {
		out.LLMPresets = append(out.LLMPresets, Preset{ID: ms.id, Subtitle: ms.subtitle})
	}
	for _, ms := range p.imagePresets {
		out.ImagePresets = append(out.ImagePresets, Preset{ID: ms.id, Subtitle: ms.subtitle})
	}
	return out
}
