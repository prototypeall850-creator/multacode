package tools

import (
	"path/filepath"
	"regexp"
	"strings"
)

// RedactSecrets masks obvious credentials in tool output before it is
// displayed or stored (plan.md secret handling).
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key\s*[:=]\s*['"]?)[^'"\s,}]+`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9\-._~+/=]+`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9\-_]{8,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-_]{8,}\b`),
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{8,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)(secret\s*[:=]\s*['"]?)[^'"\s,}]+`),
	regexp.MustCompile(`(?i)(password\s*[:=]\s*['"]?)[^'"\s,}]+`),
}

func RedactSecrets(s string) string {
	out := s
	for _, re := range secretPatterns {
		out = re.ReplaceAllString(out, `$1***`)
	}
	return strings.TrimSpace(out)
}

// isSecretPath blocks common credential files unless explicitly approved.
func isSecretPath(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	for _, s := range []string{".env", "id_rsa", "id_ed25519", "auth.json", ".pem", ".key", "secrets.json"} {
		if base == s || strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}
