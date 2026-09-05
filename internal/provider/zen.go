package provider

// ZenPreset builds an OpenAICompatible client for OpenCode Zen.
// Zen speaks the OpenAI Responses API, so BaseURL points at the
// responses endpoint (auto-detected by OpenAICompatible.useResponses).
//
// Default model tracks a currently-listed free model; the live list
// changes over time, so /models (live fetch) is the source of truth.
//
// Caveat (plan.md): models with a -free suffix may appear in the list,
// but actual access still depends on the Zen account/key.
func ZenPreset(apiKey, defaultModel string) *OpenAICompatible {
	if defaultModel == "" {
		defaultModel = "deepseek-v4-flash-free"
	}
	return &OpenAICompatible{
		BaseURL:      "https://opencode.ai/zen/v1/responses",
		ModelsURL:    "https://opencode.ai/zen/v1/models",
		APIKey:       apiKey,
		DefaultModel: defaultModel,
	}
}
