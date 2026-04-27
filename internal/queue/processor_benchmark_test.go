package queue

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/security"
	"tg-discord-bot/internal/transport"

	_ "modernc.org/sqlite"
)

func setupProcessorBenchmarkDB(b *testing.B) {
	b.Helper()

	oldDB := database.DB

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("sql.Open() error = %v", err)
	}
	database.DB = db

	if err := database.RunMigrations(); err != nil {
		b.Fatalf("RunMigrations() error = %v", err)
	}

	SetConfig(250*time.Millisecond, 30*time.Second, 5, 2*time.Second)
	security.ResetRateLimits()

	oldDestRateLimitMax := security.DestRateLimitMax
	security.DestRateLimitMax = 0

	b.Cleanup(func() {
		security.DestRateLimitMax = oldDestRateLimitMax
		security.ResetRateLimits()
		SetConfig(250*time.Millisecond, 30*time.Second, 5, 2*time.Second)
		_ = db.Close()
		database.DB = oldDB
	})
}

func BenchmarkQueueProcessBurst(b *testing.B) {
	setupProcessorBenchmarkDB(b)

	consumer := &mockConsumer{platform: "bench-burst"}
	transport.RegisterConsumer(consumer)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := models.MediaEvent{
			EventID:        fmt.Sprintf("bench-burst-%d", i),
			SourcePlatform: "telegram",
			SourceID:       "bench-source",
			TargetPlatform: "bench-burst",
			TargetID:       fmt.Sprintf("target-%d", i),
			Caption:        "burst event",
		}

		enqueued, err := database.EnqueuePendingEvent(event)
		if err != nil {
			b.Fatalf("EnqueuePendingEvent() error: %v", err)
		}
		if !enqueued {
			b.Fatalf("expected event %s to be enqueued", event.EventID)
		}

		claimed, found, err := database.ClaimNextPendingEvent(time.Now().Add(2*time.Second), 5*time.Second)
		if err != nil {
			b.Fatalf("ClaimNextPendingEvent() error: %v", err)
		}
		if !found {
			b.Fatal("expected queued event to be claimable")
		}

		processQueuedEvent(claimed)
	}
}

func BenchmarkQueueAlbumBurstProcessing(b *testing.B) {
	setupProcessorBenchmarkDB(b)

	consumer := &mockConsumer{platform: "bench-album"}
	transport.RegisterConsumer(consumer)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		albumID := fmt.Sprintf("album-%d", i)

		for j := 0; j < 8; j++ {
			event := models.MediaEvent{
				EventID:        fmt.Sprintf("bench-album-%d-%d", i, j),
				SourcePlatform: "telegram",
				SourceID:       "bench-album-source",
				TargetPlatform: "bench-album",
				TargetID:       "bench-album-target",
				MediaGroupID:   albumID,
				FileName:       fmt.Sprintf("frame-%d.jpg", j),
				Caption:        "album burst item",
			}

			enqueued, err := database.EnqueuePendingEvent(event)
			if err != nil {
				b.Fatalf("EnqueuePendingEvent() error: %v", err)
			}
			if !enqueued {
				b.Fatalf("expected event %s to be enqueued", event.EventID)
			}
		}

		for j := 0; j < 8; j++ {
			claimed, found, err := database.ClaimNextPendingEvent(time.Now().Add(2*time.Second), 5*time.Second)
			if err != nil {
				b.Fatalf("ClaimNextPendingEvent() error: %v", err)
			}
			if !found {
				b.Fatal("expected queued event to be claimable")
			}
			processQueuedEvent(claimed)
		}
	}
}

func BenchmarkApplyFormattingLargePayload(b *testing.B) {
	event := models.MediaEvent{
		Caption:        strings.Repeat("large-caption-segment ", 200),
		SenderName:     "BridgeUser",
		SourceID:       "source-large",
		ReplyToSender:  "OriginalAuthor",
		ReplyToCaption: strings.Repeat("context ", 80),
	}

	ruleConfig := models.RuleConfig{
		CaptionTemplate: "[{time}] {sender} from {source_chat}: {caption}",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = applyFormatting(event, ruleConfig)
	}
}
