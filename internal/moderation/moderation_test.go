package moderation

import (
	"testing"

	"tg-discord-bot/internal/models"
)

func TestEvaluate_DisabledByDefault(t *testing.T) {
	event := models.MediaEvent{Caption: "free money now"}
	decision := Evaluate(event, models.RuleConfig{})

	if decision.Enabled {
		t.Fatal("expected moderation to be disabled by default")
	}
	if decision.Blocked {
		t.Fatal("expected disabled moderation not to block")
	}
}

func TestEvaluate_BlocksHighRiskContent(t *testing.T) {
	t.Setenv("AI_MODERATION_ENABLED", "true")
	t.Setenv("AI_MODERATION_MIN_SCORE", "0.7")

	event := models.MediaEvent{Caption: "NSFW porn xxx promo"}
	decision := Evaluate(event, models.RuleConfig{})

	if !decision.Enabled {
		t.Fatal("expected moderation to be enabled")
	}
	if !decision.Blocked {
		t.Fatalf("expected high risk text to be blocked, got score=%.2f threshold=%.2f", decision.Score, decision.Threshold)
	}
}

func TestEvaluate_RespectsRuleThreshold(t *testing.T) {
	event := models.MediaEvent{Caption: "airdrop alert"}
	rule := models.RuleConfig{
		AIModerationEnabled:   true,
		AIModerationThreshold: 0.95,
	}

	decision := Evaluate(event, rule)
	if decision.Blocked {
		t.Fatalf("expected threshold to prevent block, got score=%.2f threshold=%.2f", decision.Score, decision.Threshold)
	}
}

func TestEvaluate_UsesCustomKeywords(t *testing.T) {
	t.Setenv("AI_MODERATION_ENABLED", "true")
	t.Setenv("AI_MODERATION_MIN_SCORE", "0.20")
	t.Setenv("AI_MODERATION_KEYWORDS", "mycustomrisk")

	event := models.MediaEvent{Caption: "this includes mycustomrisk indicator"}
	decision := Evaluate(event, models.RuleConfig{})

	if !decision.Blocked {
		t.Fatalf("expected custom keyword to trigger block, got score=%.2f threshold=%.2f", decision.Score, decision.Threshold)
	}
}
