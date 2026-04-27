package queue

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/security"
	"tg-discord-bot/internal/transport"

	_ "modernc.org/sqlite"
)

type mockConsumer struct {
	platform string
	sendFn   func(ctx context.Context, event models.MediaEvent) error
	calls    atomic.Int32
}

func (m *mockConsumer) PlatformID() string {
	return m.platform
}

func (m *mockConsumer) Send(ctx context.Context, event models.MediaEvent) error {
	m.calls.Add(1)
	if m.sendFn != nil {
		return m.sendFn(ctx, event)
	}
	return nil
}

func (m *mockConsumer) Calls() int32 {
	return m.calls.Load()
}

func setupProcessorTestDB(t *testing.T) {
	t.Helper()

	oldDB := database.DB

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	database.DB = db

	if err := database.RunMigrations(); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	SetConfig(250*time.Millisecond, 30*time.Second, 5, 2*time.Second)
	security.ResetRateLimits()

	t.Cleanup(func() {
		_ = db.Close()
		database.DB = oldDB
		SetConfig(250*time.Millisecond, 30*time.Second, 5, 2*time.Second)
		security.ResetRateLimits()
	})
}

func TestProcessQueuedEvent_DBLifecycleWithMockTransports(t *testing.T) {
	setupProcessorTestDB(t)

	discordMock := &mockConsumer{platform: "discord"}
	telegramMock := &mockConsumer{platform: "telegram"}
	transport.RegisterConsumer(discordMock)
	transport.RegisterConsumer(telegramMock)

	events := []models.MediaEvent{
		{
			EventID:        "int-dc-1",
			SourcePlatform: "telegram",
			SourceID:       "tg-source",
			TargetPlatform: "discord",
			TargetID:       "dc-target",
			Caption:        "hello discord",
		},
		{
			EventID:        "int-tg-1",
			SourcePlatform: "discord",
			SourceID:       "dc-source",
			TargetPlatform: "telegram",
			TargetID:       "123456",
			Caption:        "hello telegram",
		},
	}

	for _, event := range events {
		enqueued, err := database.EnqueuePendingEvent(event)
		if err != nil {
			t.Fatalf("EnqueuePendingEvent(%s) error = %v", event.EventID, err)
		}
		if !enqueued {
			t.Fatalf("EnqueuePendingEvent(%s) expected true", event.EventID)
		}

		claimed, found, err := database.ClaimNextPendingEvent(time.Now(), 5*time.Second)
		if err != nil {
			t.Fatalf("ClaimNextPendingEvent() error = %v", err)
		}
		if !found {
			t.Fatal("expected queued event to be claimable")
		}

		processQueuedEvent(claimed)
	}

	if discordMock.Calls() != 1 {
		t.Fatalf("expected discord mock to receive 1 event, got %d", discordMock.Calls())
	}
	if telegramMock.Calls() != 1 {
		t.Fatalf("expected telegram mock to receive 1 event, got %d", telegramMock.Calls())
	}

	queueDepth, retryDepth, err := database.GetQueueStats()
	if err != nil {
		t.Fatalf("GetQueueStats() error = %v", err)
	}
	if queueDepth != 0 || retryDepth != 0 {
		t.Fatalf("expected empty queue, got queueDepth=%d retryDepth=%d", queueDepth, retryDepth)
	}

	var processed int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM processed_events").Scan(&processed); err != nil {
		t.Fatalf("query processed_events count failed: %v", err)
	}
	if processed != 2 {
		t.Fatalf("expected 2 processed events, got %d", processed)
	}
}

func TestProcessQueuedEvent_RetryAndDeadLetterOnRepeatedFailure(t *testing.T) {
	setupProcessorTestDB(t)

	SetConfig(250*time.Millisecond, 5*time.Second, 2, 1*time.Second)

	failing := &mockConsumer{
		platform: "failing-target",
		sendFn: func(ctx context.Context, event models.MediaEvent) error {
			return errors.New("simulated network failure")
		},
	}
	transport.RegisterConsumer(failing)

	event := models.MediaEvent{
		EventID:        "int-fail-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-fail",
		TargetPlatform: "failing-target",
		TargetID:       "target-1",
		Caption:        "retry me",
	}

	enqueued, err := database.EnqueuePendingEvent(event)
	if err != nil || !enqueued {
		t.Fatalf("EnqueuePendingEvent() = (%v, %v), want (true, nil)", enqueued, err)
	}

	for attempt := 0; attempt < 3; attempt++ {
		claimed, found, err := database.ClaimNextPendingEvent(time.Now().Add(20*time.Second), 5*time.Second)
		if err != nil {
			t.Fatalf("ClaimNextPendingEvent() error = %v", err)
		}
		if !found {
			t.Fatalf("expected event on attempt %d", attempt+1)
		}

		processQueuedEvent(claimed)
	}

	if failing.Calls() != 3 {
		t.Fatalf("expected 3 delivery attempts, got %d", failing.Calls())
	}

	var pendingCount int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM pending_events WHERE event_id = ?", event.EventID).Scan(&pendingCount); err != nil {
		t.Fatalf("query pending event count failed: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("expected pending event to be removed, got %d rows", pendingCount)
	}

	var deadLetterCount int
	if err := database.DB.QueryRow("SELECT COUNT(*) FROM dead_letters WHERE event_id = ?", event.EventID).Scan(&deadLetterCount); err != nil {
		t.Fatalf("query dead letter count failed: %v", err)
	}
	if deadLetterCount != 1 {
		t.Fatalf("expected exactly one dead-letter record, got %d", deadLetterCount)
	}
}

func TestProcessQueuedEvent_TimeoutReschedules(t *testing.T) {
	setupProcessorTestDB(t)

	SetConfig(250*time.Millisecond, 3*time.Second, 3, 1*time.Second)

	timeoutConsumer := &mockConsumer{
		platform: "timeout-target",
		sendFn: func(ctx context.Context, event models.MediaEvent) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	transport.RegisterConsumer(timeoutConsumer)

	event := models.MediaEvent{
		EventID:        "int-timeout-1",
		SourcePlatform: "telegram",
		SourceID:       "tg-timeout",
		TargetPlatform: "timeout-target",
		TargetID:       "target-timeout",
		Caption:        "this will timeout",
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

	if timeoutConsumer.Calls() != 1 {
		t.Fatalf("expected one timeout attempt, got %d", timeoutConsumer.Calls())
	}

	var retryCount int
	var lastError string
	var availableAt int64
	if err := database.DB.QueryRow("SELECT retry_count, last_error, available_at FROM pending_events WHERE event_id = ?", event.EventID).Scan(&retryCount, &lastError, &availableAt); err != nil {
		t.Fatalf("failed to query pending event after timeout: %v", err)
	}

	if retryCount != 1 {
		t.Fatalf("expected retry_count=1 after timeout, got %d", retryCount)
	}
	if !strings.Contains(strings.ToLower(lastError), "deadline") {
		t.Fatalf("expected timeout-related error, got %q", lastError)
	}
	if availableAt <= time.Now().Unix() {
		t.Fatalf("expected rescheduled availability in the future, got %d", availableAt)
	}
}
