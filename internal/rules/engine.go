package rules

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/security"
)

var (
	mu          sync.Mutex
	burstCounts = make(map[string]int)
	burstLast   = make(map[string]time.Time)
	hashtagRe   = regexp.MustCompile(`#([A-Za-z0-9_]+)`)
)

// EvaluateFilterRule returns true if the message passes the filter rules.
func EvaluateFilterRule(config models.RuleConfig, caption string, senderID string) bool {
	// 1. Check sender
	for _, blocked := range config.BlockedSenders {
		if senderID == strings.TrimSpace(blocked) {
			return false
		}
	}

	// 2. Check Include precedence
	if len(config.IncludeWords) > 0 {
		hasInclude := false
		lowerCaption := strings.ToLower(caption)
		for _, word := range config.IncludeWords {
			if strings.Contains(lowerCaption, strings.ToLower(strings.TrimSpace(word))) {
				hasInclude = true
				break
			}
		}
		if !hasInclude {
			return false // Does not match include list
		}
	}

	// 3. Check Regex
	for _, pattern := range config.RegexFilters {
		matched, err := regexp.MatchString(pattern, caption)
		if err == nil && matched {
			return false
		}
	}

	return true
}

// EvaluateTagRule returns true if the caption satisfies required tag routing.
func EvaluateTagRule(config models.RuleConfig, caption string) bool {
	if len(config.RequiredTags) == 0 {
		return true
	}

	tags := extractHashtags(caption)
	if len(tags) == 0 {
		return false
	}

	for _, tag := range config.RequiredTags {
		normalized := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(tag), "#"))
		if normalized == "" {
			continue
		}
		if _, ok := tags[normalized]; ok {
			return true
		}
	}

	return false
}

func extractHashtags(caption string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, match := range hashtagRe.FindAllStringSubmatch(caption, -1) {
		if len(match) < 2 {
			continue
		}
		normalized := strings.TrimSpace(strings.ToLower(match[1]))
		if normalized == "" {
			continue
		}
		result[normalized] = struct{}{}
	}
	return result
}

// EvaluateFileRule returns true if the file satisfies per-chat limitations.
func EvaluateFileRule(config models.RuleConfig, fileSize int64, contentType string) bool {
	config = security.ApplyFileRuleDefaults(config)

	fileSizeMB := int(fileSize / (1024 * 1024))
	if config.MaxFileSizeMB > 0 && fileSizeMB > config.MaxFileSizeMB {
		return false
	}

	if len(config.AllowedMimeTypes) > 0 {
		allowed := false
		for _, mime := range config.AllowedMimeTypes {
			if strings.HasPrefix(strings.ToLower(contentType), strings.ToLower(strings.TrimSpace(mime))) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	return true
}

// EvaluateTimeRule returns the available time and whether it was delayed by quiet hours.
func EvaluateTimeRule(config models.RuleConfig, now time.Time) (time.Time, bool) {
	if config.QuietHoursStart == "" || config.QuietHoursEnd == "" {
		return now, false
	}

	startParsed, err1 := time.Parse("15:04", config.QuietHoursStart)
	endParsed, err2 := time.Parse("15:04", config.QuietHoursEnd)
	if err1 != nil || err2 != nil {
		return now, false
	}

	utcNow := now.UTC()
	currentMin := utcNow.Hour()*60 + utcNow.Minute()
	startMin := startParsed.Hour()*60 + startParsed.Minute()
	endMin := endParsed.Hour()*60 + endParsed.Minute()

	inQuietHours := false
	if startMin < endMin {
		inQuietHours = currentMin >= startMin && currentMin < endMin
	} else {
		// Wraps around midnight
		inQuietHours = currentMin >= startMin || currentMin < endMin
	}

	if !inQuietHours {
		return now, false
	}

	var nextEnd time.Time
	if currentMin < endMin {
		nextEnd = time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), endParsed.Hour(), endParsed.Minute(), 0, 0, time.UTC)
	} else {
		nextEnd = time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, endParsed.Hour(), endParsed.Minute(), 0, 0, time.UTC)
	}

	return nextEnd, true
}

// EvaluateSpamRule returns true if the message is NOT spam (i.e. passes burst limits).
func EvaluateSpamRule(config models.RuleConfig, tgID, dcChannelID string) bool {
	if config.BurstLimit <= 0 || config.BurstWindow <= 0 {
		return true // No limits
	}

	key := tgID + ":" + dcChannelID

	mu.Lock()
	defer mu.Unlock()

	last := burstLast[key]
	if time.Since(last) > time.Duration(config.BurstWindow)*time.Second {
		burstCounts[key] = 0
	}

	if burstCounts[key] >= config.BurstLimit {
		return false // Limit reached
	}

	burstCounts[key]++
	burstLast[key] = time.Now()

	return true
}
