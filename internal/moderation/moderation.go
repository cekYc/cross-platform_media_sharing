package moderation

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tg-discord-bot/internal/models"
)

var (
	urlPattern          = regexp.MustCompile(`https?://|www\.`)
	repeatedPunct       = regexp.MustCompile(`[!?]{3,}`)
	riskyExtensions     = map[string]float64{".exe": 0.60, ".bat": 0.50, ".cmd": 0.50, ".scr": 0.50, ".js": 0.40}
	defaultKeywordScore = map[string]float64{
		"nsfw":            0.60,
		"porn":            0.75,
		"xxx":             0.80,
		"onlyfans":        0.60,
		"casino":          0.55,
		"crypto giveaway": 0.60,
		"free money":      0.60,
		"airdrop":         0.40,
	}
)

// Decision contains the moderation score and decision for one event.
type Decision struct {
	Enabled   bool
	Blocked   bool
	Score     float64
	Threshold float64
	Reasons   []string
}

// Evaluate computes a lightweight AI-style moderation score and returns a block decision.
// This layer is optional and disabled by default.
func Evaluate(event models.MediaEvent, ruleConfig models.RuleConfig) Decision {
	enabled := ruleConfig.AIModerationEnabled || parseBoolEnv("AI_MODERATION_ENABLED", false)
	if !enabled {
		return Decision{}
	}

	threshold := ruleConfig.AIModerationThreshold
	if threshold <= 0 {
		threshold = parseFloatEnv("AI_MODERATION_MIN_SCORE", 0.70)
	}
	if threshold > 1 {
		threshold = 1
	}
	if threshold < 0.05 {
		threshold = 0.05
	}

	text := strings.ToLower(strings.TrimSpace(event.Caption + " " + event.FileName))
	score := 0.0
	reasons := make([]string, 0, 4)

	for keyword, weight := range defaultKeywordScore {
		if strings.Contains(text, keyword) {
			score += weight
			reasons = append(reasons, "keyword:"+keyword)
		}
	}

	for _, keyword := range splitCSV(os.Getenv("AI_MODERATION_KEYWORDS")) {
		if strings.Contains(text, keyword) {
			score += 0.25
			reasons = append(reasons, "custom_keyword:"+keyword)
		}
	}

	urlHits := len(urlPattern.FindAllStringIndex(text, -1))
	if urlHits >= 3 {
		score += 0.35
		reasons = append(reasons, "many_urls")
	}

	if repeatedPunct.MatchString(text) {
		score += 0.10
		reasons = append(reasons, "aggressive_punctuation")
	}

	if hasRepeatedWords(text) {
		score += 0.20
		reasons = append(reasons, "repeated_words")
	}

	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(event.FileName)))
	if weight, ok := riskyExtensions[ext]; ok {
		score += weight
		reasons = append(reasons, "risky_extension:"+ext)
	}

	if score > 1 {
		score = 1
	}

	return Decision{
		Enabled:   true,
		Blocked:   score >= threshold,
		Score:     score,
		Threshold: threshold,
		Reasons:   reasons,
	}
}

func hasRepeatedWords(text string) bool {
	parts := strings.Fields(text)
	if len(parts) < 4 {
		return false
	}

	run := 1
	for i := 1; i < len(parts); i++ {
		if parts[i] == parts[i-1] {
			run++
			if run >= 3 {
				return true
			}
			continue
		}
		run = 1
	}

	return false
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(part))
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func parseBoolEnv(name string, defaultValue bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return defaultValue
	}

	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func parseFloatEnv(name string, defaultValue float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return defaultValue
	}
	return value
}
