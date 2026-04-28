package database

import (
	"database/sql"
	"testing"
	"time"

	"tg-discord-bot/internal/models"

	_ "modernc.org/sqlite"
)

func TestBlockedWords(t *testing.T) {
	setupQueueTestDB(t)

	err := LinkChannel("telegram", "source-1", "discord", "target-1", "")
	if err != nil {
		t.Fatalf("LinkChannel() error = %v", err)
	}

	err = AddBlockedWord("telegram", "source-1", "discord", "target-1", "  SPAM  ")
	if err != nil {
		t.Fatalf("AddBlockedWord() error = %v", err)
	}
	err = AddBlockedWord("telegram", "source-1", "discord", "target-1", "ADS")
	if err != nil {
		t.Fatalf("AddBlockedWord() error = %v", err)
	}

	words, err := GetBlockedWords("telegram", "source-1", "discord", "target-1")
	if err != nil {
		t.Fatalf("GetBlockedWords() error = %v", err)
	}
	if len(words) != 2 || words[0] != "ads" || words[1] != "spam" {
		t.Fatalf("expected [ads spam], got %v", words)
	}

	removed, err := RemoveBlockedWord("telegram", "source-1", "discord", "target-1", "spam")
	if err != nil || !removed {
		t.Fatalf("RemoveBlockedWord() failed: %v, %v", removed, err)
	}

	words, _ = GetBlockedWords("telegram", "source-1", "discord", "target-1")
	if len(words) != 1 || words[0] != "ads" {
		t.Fatalf("expected [ads], got %v", words)
	}
}

func TestPersistentQueueEnqueueClaimAckIdempotent(t *testing.T) {
	setupQueueTestDB(t)

	event := models.MediaEvent{
		EventID:        "evt-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-1",
		TargetPlatform: "discord",
		TargetID:       "dc-1",
		FileName:       "image.jpg",
		FileURL:        "http://example.com/image.jpg",
	}

	enqueued, err := EnqueuePendingEvent(event)
	if err != nil {
		t.Fatalf("EnqueuePendingEvent() error = %v", err)
	}
	if !enqueued {
		t.Fatal("expected first enqueue to be accepted")
	}

	enqueued, err = EnqueuePendingEvent(event)
	if err != nil {
		t.Fatalf("EnqueuePendingEvent() duplicate error = %v", err)
	}
	if enqueued {
		t.Fatal("expected duplicate enqueue to be ignored")
	}

	claimed, found, err := ClaimNextPendingEvent(time.Now(), 10*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextPendingEvent() error = %v", err)
	}
	if !found {
		t.Fatal("expected queued event to be claimable")
	}
	if claimed.Event.EventID != event.EventID {
		t.Fatalf("claimed event id = %q, want %q", claimed.Event.EventID, event.EventID)
	}

	if err := AckPendingEvent(event.EventID); err != nil {
		t.Fatalf("AckPendingEvent() error = %v", err)
	}

	enqueued, err = EnqueuePendingEvent(event)
	if err != nil {
		t.Fatalf("EnqueuePendingEvent() post-ack duplicate check error = %v", err)
	}
	if enqueued {
		t.Fatal("expected processed event id to remain idempotent and be ignored")
	}
}

func TestMediaHashDuplicatePrevention(t *testing.T) {
	setupQueueTestDB(t)

	first := models.MediaEvent{
		EventID:        "evt-media-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-1",
		TargetPlatform: "discord",
		TargetID:       "dc-1",
		FileName:       "photo.jpg",
		FileURL:        "http://example.com/photo.jpg",
		FileHash:       "hash-shared",
	}

	enqueued, err := EnqueuePendingEvent(first)
	if err != nil {
		t.Fatalf("EnqueuePendingEvent() first error = %v", err)
	}
	if !enqueued {
		t.Fatal("expected first media event to enqueue")
	}

	second := first
	second.EventID = "evt-media-2"
	second.FileURL = "http://example.com/photo-copy.jpg"

	enqueued, err = EnqueuePendingEvent(second)
	if err != nil {
		t.Fatalf("EnqueuePendingEvent() duplicate media error = %v", err)
	}
	if enqueued {
		t.Fatal("expected duplicate media hash to be ignored")
	}

	third := first
	third.EventID = "evt-media-3"
	third.FileHash = "hash-unique"

	enqueued, err = EnqueuePendingEvent(third)
	if err != nil {
		t.Fatalf("EnqueuePendingEvent() unique media error = %v", err)
	}
	if !enqueued {
		t.Fatal("expected unique media hash to enqueue")
	}
}

func TestDeadLetterReplayFlow(t *testing.T) {
	setupQueueTestDB(t)

	event := models.MediaEvent{
		EventID:        "evt-dead-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-2",
		TargetPlatform: "discord",
		TargetID:       "dc-2",
		FileName:       "clip.mp4",
		FileURL:        "http://example.com/video.mp4",
	}

	enqueued, err := EnqueuePendingEvent(event)
	if err != nil || !enqueued {
		t.Fatalf("EnqueuePendingEvent() = (%v, %v), want (true, nil)", enqueued, err)
	}

	_, found, err := ClaimNextPendingEvent(time.Now(), 10*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextPendingEvent() error = %v", err)
	}
	if !found {
		t.Fatal("expected queued event to be claimable")
	}

	if err := ReschedulePendingEvent(event.EventID, 3, time.Now(), "discord timeout"); err != nil {
		t.Fatalf("ReschedulePendingEvent() error = %v", err)
	}

	deadLetterID, err := MovePendingEventToDeadLetter(event.EventID, "max retries exceeded")
	if err != nil {
		t.Fatalf("MovePendingEventToDeadLetter() error = %v", err)
	}
	if deadLetterID <= 0 {
		t.Fatalf("expected valid dead letter id, got %d", deadLetterID)
	}

	items, err := ListDeadLettersByTarget("discord", event.TargetID, 10)
	if err != nil {
		t.Fatalf("ListDeadLettersByTarget() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("dead letter list length = %d, want 1", len(items))
	}

	replayed, err := ReplayDeadLetter(deadLetterID, "discord", event.TargetID)
	if err != nil {
		t.Fatalf("ReplayDeadLetter() error = %v", err)
	}
	if !replayed {
		t.Fatal("expected dead letter replay to succeed")
	}

	items, err = ListDeadLettersByTarget("discord", event.TargetID, 10)
	if err != nil {
		t.Fatalf("ListDeadLettersByTarget() after replay error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("dead letter list length after replay = %d, want 0", len(items))
	}

	claimed, found, err := ClaimNextPendingEvent(time.Now(), 10*time.Second)
	if err != nil {
		t.Fatalf("ClaimNextPendingEvent() after replay error = %v", err)
	}
	if !found {
		t.Fatal("expected replayed event to be claimable")
	}
	if claimed.Event.EventID != event.EventID {
		t.Fatalf("replayed claimed event id = %q, want %q", claimed.Event.EventID, event.EventID)
	}
}

func setupQueueTestDB(t *testing.T) {
	t.Helper()

	oldDB := DB
	t.Cleanup(func() {
		if DB != nil {
			_ = DB.Close()
		}
		DB = oldDB
	})

	var err error
	DB, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	if err := RunMigrations(); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
}

func TestAuditLogInsertAndList(t *testing.T) {
	setupQueueTestDB(t)

	if err := InsertAuditLog("link", "discord", "user-1", "linked tg:-100 to dc:999"); err != nil {
		t.Fatalf("InsertAuditLog() error = %v", err)
	}

	if err := InsertAuditLog("unlink", "telegram", "user-2", "unlinked tg:-100 from dc:999"); err != nil {
		t.Fatalf("InsertAuditLog() error = %v", err)
	}

	entries, err := ListAuditLogs(10)
	if err != nil {
		t.Fatalf("ListAuditLogs() error = %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 audit entries, got %d", len(entries))
	}

	// Most recent first
	if entries[0].Action != "unlink" {
		t.Fatalf("expected first entry to be 'unlink', got %q", entries[0].Action)
	}

	if entries[1].ActorPlatform != "discord" {
		t.Fatalf("expected second entry actor_platform 'discord', got %q", entries[1].ActorPlatform)
	}
}
