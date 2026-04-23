package database

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"tg-discord-bot/internal/models"

	_ "modernc.org/sqlite"
)

func TestNormalizeBlockedWord(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trims and lowercases", input: "  SPAM  ", want: "spam"},
		{name: "empty", input: "", want: ""},
		{name: "whitespace", input: "   ", want: ""},
		{name: "phrase", input: "MiXeD CaSe", want: "mixed case"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeBlockedWord(tc.input)
			if got != tc.want {
				t.Fatalf("normalizeBlockedWord(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSplitBlockedWords(t *testing.T) {
	input := sql.NullString{Valid: true, String: "spam,SPAM,  ads , ,News"}
	want := []string{"spam", "ads", "news"}

	got := splitBlockedWords(input)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitBlockedWords() = %#v, want %#v", got, want)
	}
}

func TestMergeBlockedWords(t *testing.T) {
	existing := sql.NullString{Valid: true, String: "spam,ads"}

	if got := mergeBlockedWords(existing, " SPAM "); got != "spam,ads" {
		t.Fatalf("mergeBlockedWords duplicate handling failed, got %q", got)
	}

	if got := mergeBlockedWords(existing, "news"); got != "spam,ads,news" {
		t.Fatalf("mergeBlockedWords append handling failed, got %q", got)
	}
}

func TestRemoveBlockedWord(t *testing.T) {
	existing := sql.NullString{Valid: true, String: "spam,ads,news"}

	updated, removed := removeBlockedWord(existing, "ads")
	if !removed {
		t.Fatal("expected removeBlockedWord to remove existing value")
	}
	if updated != "spam,news" {
		t.Fatalf("removeBlockedWord returned %q, want %q", updated, "spam,news")
	}

	updated, removed = removeBlockedWord(existing, "missing")
	if removed {
		t.Fatal("expected removeBlockedWord to return removed=false for missing value")
	}
	if updated != "spam,ads,news" {
		t.Fatalf("removeBlockedWord returned %q, want %q", updated, "spam,ads,news")
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

	if err := createPairingsTable(); err != nil {
		t.Fatalf("createPairingsTable() error = %v", err)
	}

	if err := ensureQueueSchema(); err != nil {
		t.Fatalf("ensureQueueSchema() error = %v", err)
	}
}
