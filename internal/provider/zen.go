package provider

// ZenPreset builds an OpenAICompatible client for OpenCode Zen.
//
// Free tier (no key): POST {base}/chat/completions with the
// x-opencode-client header, exactly like the proven MultaCode classic
// client. Models list is public; -free models rotate over time, so
// /models (live fetch) is the source of truth — the default below is
// just a sane starting point.
//
// With a key, users may point BaseURL at the responses endpoint instead
// (auto-detected by useResponses).
//
// Caveat (plan.md): -free access depends on Zen-side availability;
// overloaded free models return 429/503 and Stream retries those.
func ZenPreset(apiKey, defaultModel string) *OpenAICompatible {
	if defaultModel == "" {
		defaultModel = "nemotron-3-ultra-free"
	}
	return &OpenAICompatible{
		BaseURL:   "https://opencode.ai/zen/v1/chat/completions",
		ModelsURL: "https://opencode.ai/zen/v1/models",
		APIKey:    apiKey,
		Headers: map[string]string{
			"x-opencode-client": "cli",
			"User-Agent":        "multacode",
		},
		DefaultModel: defaultModel,
	}
}
