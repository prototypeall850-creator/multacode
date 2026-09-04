package provider

import (
	"os"
	"strings"
)

// ResolveAPIKey resolves api_key_ref to a secret value.
// Supported forms:
//   - "env:NAME"  -> os.Getenv(NAME)
//   - "auth:id"   -> auth[id]
//   - "NAME"      -> auth[NAME], else os.Getenv(NAME)
//   - ""          -> "" (no key, e.g. free endpoints)
func ResolveAPIKey(ref string, auth map[string]string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "env:") {
		return os.Getenv(strings.TrimPrefix(ref, "env:"))
	}
	if strings.HasPrefix(ref, "auth:") {
		return auth[strings.TrimPrefix(ref, "auth:")]
	}
	if auth != nil {
		if v, ok := auth[ref]; ok && v != "" {
			return v
		}
	}
	if v := os.Getenv(ref); v != "" {
		return v
	}
	// Last resort: treat ref as literal (lets users paste a key ref directly).
	if strings.HasPrefix(ref, "sk-") || strings.HasPrefix(ref, "xai-") || len(ref) > 32 {
		return ref
	}
	return ""
}
