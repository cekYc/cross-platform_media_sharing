package database

import (
	"testing"
	"time"

	"tg-discord-bot/internal/models"
)

func TestDigestEventFlow(t *testing.T) {
	setupQueueTestDB(t)

	event := models.MediaEvent{
		EventID:        "evt-digest-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-1",
		TargetPlatform: "discord",
		TargetID:       "dc-1",
		Caption:        "hello #news",
		FileName:       "photo.jpg",
		MediaType:      models.MediaTypePhoto,
	}

	deliverAfter := time.Now().Add(-1 * time.Minute)
	if err := AddDigestEvent(event, deliverAfter); err != nil {
		t.Fatalf("AddDigestEvent() error = %v", err)
	}

	groups, err := ListDigestGroupsDue(time.Now(), 10)
	if err != nil {
		t.Fatalf("ListDigestGroupsDue() error = %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 digest group, got %d", len(groups))
	}

	events, total, err := ListDigestEventsForGroup("telegram", "tg-1", "discord", "dc-1", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListDigestEventsForGroup() error = %v", err)
	}
	if total != 1 || len(events) != 1 {
		t.Fatalf("expected 1 digest event, got total=%d items=%d", total, len(events))
	}

	if err := DeleteDigestEventsForGroup("telegram", "tg-1", "discord", "dc-1", time.Now()); err != nil {
		t.Fatalf("DeleteDigestEventsForGroup() error = %v", err)
	}

	_, total, err = ListDigestEventsForGroup("telegram", "tg-1", "discord", "dc-1", time.Now(), 10)
	if err != nil {
		t.Fatalf("ListDigestEventsForGroup() after delete error = %v", err)
	}
	if total != 0 {
		t.Fatalf("expected digest events cleared, got total=%d", total)
	}
}
