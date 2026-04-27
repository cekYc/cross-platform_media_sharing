package queue

import (
	"testing"
	"time"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/transport"
)

func TestProcessQueuedEvent_AIModerationFiltersEvent(t *testing.T) {
	setupProcessorTestDB(t)
	t.Setenv("AI_MODERATION_ENABLED", "true")
	t.Setenv("AI_MODERATION_MIN_SCORE", "0.40")

	consumer := &mockConsumer{platform: "discord"}
	transport.RegisterConsumer(consumer)

	if err := database.LinkChannel("telegram", "tg-ai", "discord", "dc-ai", ""); err != nil {
		t.Fatalf("LinkChannel() error = %v", err)
	}

	event := models.MediaEvent{
		EventID:        "evt-ai-filter-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-ai",
		TargetPlatform: "discord",
		TargetID:       "dc-ai",
		Caption:        "free money free money free money!!! https://a https://b https://c",
	}

	enqueued, err := database.EnqueuePendingEvent(event)
	if err != nil || !enqueued {
		t.Fatalf("EnqueuePendingEvent() = (%v, %v), want (true, nil)", enqueued, err)
	}

	claimed, found, err := database.ClaimNextPendingEvent(time.Now(), 5*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextPendingEvent() error = %v", err)
	}
	if !found {
		t.Fatal("expected queued event to be claimable")
	}

	processQueuedEvent(claimed)

	if consumer.Calls() != 0 {
		t.Fatalf("expected moderated event not to be forwarded, got %d sends", consumer.Calls())
	}

	var processedCount int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM processed_events WHERE event_id = ?", event.EventID).Scan(&processedCount); err != nil {
		t.Fatalf("query processed count failed: %v", err)
	}
	if processedCount != 1 {
		t.Fatalf("expected processed count to be 1, got %d", processedCount)
	}
}
