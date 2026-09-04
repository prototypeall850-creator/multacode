package provider

// ZenPreset builds an OpenAICompatible client for OpenCode Zen.
// Zen speaks the OpenAI Responses API, so BaseURL points at the
// responses endpoint (auto-detected by OpenAICompatible.useResponses).
//
// Caveat (plan.md): models with a -free suffix may appear in the list,
// but actual access still depends on the Zen account/key.
func ZenPreset(apiKey, defaultModel string) *OpenAICompatible {
	if defaultModel == "" {
		defaultModel = "glm-4.7-free"
	}
	return &OpenAICompatible{
		BaseURL:      "https://opencode.ai/zen/v1/responses",
		ModelsURL:    "https://opencode.ai/zen/v1/models",
		APIKey:       apiKey,
		DefaultModel: defaultModel,
	}
}
