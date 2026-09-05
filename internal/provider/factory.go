package provider

import (
	"fmt"

	"multacode/internal/config"
)

// BuildProvider constructs a Provider from a ProviderConfig + auth store.
func BuildProvider(pc config.ProviderConfig, auth map[string]string) (Provider, error) {
	key := ResolveAPIKey(pc.APIKeyRef, auth)
	switch pc.Kind {
	case "openai-compatible", "openai", "":
		return &OpenAICompatible{
			BaseURL:      pc.BaseURL,
			APIKey:       key,
			DefaultModel: pc.DefaultModel,
			Headers:      pc.Headers,
			ModelsURL:    pc.ModelsURL,
		}, nil
	case "zen":
		z := ZenPreset(key, pc.DefaultModel)
		if pc.BaseURL != "" {
			z.BaseURL = pc.BaseURL
			if pc.ModelsURL == "" {
				// Custom base (e.g. Go tier) lists its own models,
				// not the default Zen catalog.
				z.ModelsURL = DeriveModelsURL(pc.BaseURL)
			}
		}
		if pc.ModelsURL != "" {
			z.ModelsURL = pc.ModelsURL
		}
		if pc.Headers != nil {
			z.Headers = pc.Headers
		}
		return z, nil
	case "anthropic":
		base := pc.BaseURL
		if base == "" {
			base = "https://api.anthropic.com"
		}
		return &Anthropic{BaseURL: base, APIKey: key, DefaultModel: pc.DefaultModel}, nil
	default:
		return nil, fmt.Errorf("unknown provider kind %q", pc.Kind)
	}
}
