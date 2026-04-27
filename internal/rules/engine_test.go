package rules

import (
	"fmt"
	"testing"
	"time"

	"tg-discord-bot/internal/models"
)

func TestEvaluateFilterRule(t *testing.T) {
	config := models.RuleConfig{
		BlockedSenders: []string{"bad-user"},
		IncludeWords:   []string{"release", "urgent"},
		RegexFilters:   []string{`(?i)forbidden`},
	}

	if EvaluateFilterRule(config, "release notes", "bad-user") {
		t.Fatal("expected blocked sender to fail")
	}

	if EvaluateFilterRule(config, "plain hello", "good-user") {
		t.Fatal("expected message without include words to fail")
	}

	if EvaluateFilterRule(config, "urgent but forbidden", "good-user") {
		t.Fatal("expected regex filtered message to fail")
	}

	if !EvaluateFilterRule(config, "urgent release shipped", "good-user") {
		t.Fatal("expected message satisfying all rules to pass")
	}
}

func TestEvaluateFileRule(t *testing.T) {
	defaultConfig := models.RuleConfig{}

	if !EvaluateFileRule(defaultConfig, 40*1024*1024, "image/jpeg") {
		t.Fatal("expected safe default file to pass")
	}

	if EvaluateFileRule(defaultConfig, 60*1024*1024, "image/jpeg") {
		t.Fatal("expected file larger than default max size to fail")
	}

	custom := models.RuleConfig{
		AllowedMimeTypes: []string{"video/"},
		MaxFileSizeMB:    10,
	}

	if EvaluateFileRule(custom, 3*1024*1024, "image/png") {
		t.Fatal("expected disallowed mime type to fail")
	}

	if !EvaluateFileRule(custom, 3*1024*1024, "video/mp4") {
		t.Fatal("expected allowed custom mime type to pass")
	}
}

func TestEvaluateTimeRule(t *testing.T) {
	dayRule := models.RuleConfig{
		QuietHoursStart: "09:00",
		QuietHoursEnd:   "17:00",
	}

	now := time.Date(2026, time.January, 5, 12, 15, 0, 0, time.UTC)
	next, delayed := EvaluateTimeRule(dayRule, now)
	if !delayed {
		t.Fatal("expected daytime quiet hours to delay event")
	}
	if next.Hour() != 17 || next.Minute() != 0 {
		t.Fatalf("expected delivery at 17:00, got %s", next.Format(time.RFC3339))
	}

	nightRule := models.RuleConfig{
		QuietHoursStart: "22:00",
		QuietHoursEnd:   "06:00",
	}

	now = time.Date(2026, time.January, 5, 23, 10, 0, 0, time.UTC)
	next, delayed = EvaluateTimeRule(nightRule, now)
	if !delayed {
		t.Fatal("expected overnight quiet hours to delay event")
	}
	if next.Day() != 6 || next.Hour() != 6 || next.Minute() != 0 {
		t.Fatalf("expected delivery next day at 06:00, got %s", next.Format(time.RFC3339))
	}

	now = time.Date(2026, time.January, 5, 18, 0, 0, 0, time.UTC)
	next, delayed = EvaluateTimeRule(dayRule, now)
	if delayed {
		t.Fatal("expected outside quiet hours not to delay event")
	}
	if !next.Equal(now) {
		t.Fatal("expected returned time to equal input when not delayed")
	}
}

func TestEvaluateSpamRule(t *testing.T) {
	config := models.RuleConfig{BurstLimit: 2, BurstWindow: 60}

	tgID := fmt.Sprintf("tg-%d", time.Now().UnixNano())
	dcID := fmt.Sprintf("dc-%d", time.Now().UnixNano())

	if !EvaluateSpamRule(config, tgID, dcID) {
		t.Fatal("expected first message to pass")
	}
	if !EvaluateSpamRule(config, tgID, dcID) {
		t.Fatal("expected second message to pass")
	}
	if EvaluateSpamRule(config, tgID, dcID) {
		t.Fatal("expected third message to be blocked by burst limit")
	}

	// Different source-target key should have independent counter.
	if !EvaluateSpamRule(config, tgID+"-other", dcID+"-other") {
		t.Fatal("expected independent key to pass")
	}
}
