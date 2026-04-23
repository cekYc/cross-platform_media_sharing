package security

import (
	"os"
	"regexp"
	"strings"

	"tg-discord-bot/internal/models"
)

// Default safe limits applied when no explicit rule config is set.
const (
	DefaultMaxFileSizeMB = 50
)

// DefaultAllowedMimeTypes returns the conservative MIME prefixes accepted by default.
func DefaultAllowedMimeTypes() []string {
	return []string{"image/", "video/", "application/pdf"}
}

// ---- Secret Hygiene ----

// tokenPatterns matches common token formats so they can be redacted from logs.
var tokenPatterns = []*regexp.Regexp{
	// Discord bot tokens (e.g. "NjE2...Mzk.Xa...wA.0f...")
	regexp.MustCompile(`[A-Za-z0-9_-]{24,}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,}`),
	// Telegram bot tokens (e.g. "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11")
	regexp.MustCompile(`\d{8,}:[A-Za-z0-9_-]{30,}`),
	// Generic hex secrets (64+ hex chars, likely SHA/HMAC keys)
	regexp.MustCompile(`[0-9a-fA-F]{64,}`),
}

// MaskSecrets replaces token-like substrings in value with "***REDACTED***".
func MaskSecrets(value string) string {
	if value == "" {
		return value
	}
	result := value
	for _, pattern := range tokenPatterns {
		result = pattern.ReplaceAllString(result, "***REDACTED***")
	}
	return result
}

// LoadSecret reads a secret from environment variables.
// It first checks for NAME_FILE (a file path whose contents become the value),
// then falls back to NAME (the plain env var).
// This supports Docker Secrets and external secret manager workflows.
func LoadSecret(envName string) string {
	// 1. Check for a _FILE variant (e.g. TELEGRAM_BOT_TOKEN_FILE)
	filePath := strings.TrimSpace(os.Getenv(envName + "_FILE"))
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err == nil {
			secret := strings.TrimSpace(string(data))
			if secret != "" {
				return secret
			}
		}
	}

	// 2. Fallback to the plain env var
	return strings.TrimSpace(os.Getenv(envName))
}

// ---- Safer Defaults ----

// ApplyFileRuleDefaults fills zero-value file rule fields with conservative
// defaults so that new pairings are safe out of the box.
func ApplyFileRuleDefaults(config models.RuleConfig) models.RuleConfig {
	if config.MaxFileSizeMB <= 0 {
		config.MaxFileSizeMB = DefaultMaxFileSizeMB
	}
	if len(config.AllowedMimeTypes) == 0 {
		config.AllowedMimeTypes = DefaultAllowedMimeTypes()
	}
	return config
}
