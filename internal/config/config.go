package config

// Config mirrors plan.md default global policy.
type Config struct {
	DefaultProvider string           `json:"default_provider,omitempty"`
	DefaultModel    string           `json:"default_model,omitempty"`
	Providers       []ProviderConfig `json:"providers,omitempty"`
	Permission      PermissionPolicy `json:"permission,omitempty"`
	Search          SearchConfig     `json:"search,omitempty"`
}

type ProviderConfig struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"` // "openai-compatible" | "anthropic" | "zen"
	Name         string            `json:"name,omitempty"`
	BaseURL      string            `json:"base_url,omitempty"`
	APIKeyRef    string            `json:"api_key_ref,omitempty"` // env var name or "file:..." / auth key id
	ModelsURL    string            `json:"models_url,omitempty"`
	DefaultModel string            `json:"default_model,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
}

type PermissionPolicy struct {
	Read   string `json:"read"`
	Search string `json:"search"`
	Edit   string `json:"edit"`
	Shell  string `json:"shell"`
	Delete string `json:"delete"`
}

type SearchConfig struct {
	Provider  string `json:"provider,omitempty"` // brave | tavily | serper | searxng
	APIKeyRef string `json:"api_key_ref,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Permission: PermissionPolicy{
			Read:   "allow",
			Search: "allow",
			Edit:   "ask",
			Shell:  "ask",
			Delete: "ask",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	if err := loadJSON(path, &cfg); err != nil {
		return Config{}, err
	}
	// Re-apply defaults for empty policy fields (missing file case).
	def := DefaultConfig().Permission
	if cfg.Permission.Read == "" {
		cfg.Permission.Read = def.Read
	}
	if cfg.Permission.Search == "" {
		cfg.Permission.Search = def.Search
	}
	if cfg.Permission.Edit == "" {
		cfg.Permission.Edit = def.Edit
	}
	if cfg.Permission.Shell == "" {
		cfg.Permission.Shell = def.Shell
	}
	if cfg.Permission.Delete == "" {
		cfg.Permission.Delete = def.Delete
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	return saveJSON(path, cfg)
}
