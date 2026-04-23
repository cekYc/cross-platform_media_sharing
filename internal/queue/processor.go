package queue

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/transport"
)

const (
	albumCollectWindow = 3 * time.Second
	albumIdleTimeout   = 700 * time.Millisecond
)

type albumBatch struct {
	items     []database.QueuedEvent
	firstSeen time.Time
	lastSeen  time.Time
}

var (
	albumCache = make(map[string]*albumBatch)
	albumMutex sync.Mutex

	queuePollInterval  = 250 * time.Millisecond
	processingLease    = 30 * time.Second
	maxDeliveryRetries = 5
	retryBaseDelay     = 2 * time.Second
)

func SetConfig(pollInterval, lease time.Duration, maxRetries int, retryBase time.Duration) {
	if pollInterval > 0 {
		queuePollInterval = pollInterval
	}
	if lease > 0 {
		processingLease = lease
	}
	if maxRetries > 0 {
		maxDeliveryRetries = maxRetries
	}
	if retryBase > 0 {
		retryBaseDelay = retryBase
	}
}

func StartProcessor() {
	log.Println("starting generic queue processor...")
	for {
		observability.MarkConsumerHeartbeat()
		now := time.Now()
		// Note: Currently Album processing is disabled or we can implement it generically.
		// For now, we process single events directly.

		queuedEvent, found, err := database.ClaimNextPendingEvent(now, processingLease)
		if err != nil {
			observability.Log("warn", "failed to claim pending event", map[string]interface{}{"error": err.Error()})
			time.Sleep(queuePollInterval)
			continue
		}

		if !found {
			time.Sleep(queuePollInterval)
			continue
		}

		processQueuedEvent(queuedEvent)
	}
}

func processQueuedEvent(queuedEvent database.QueuedEvent) {
	event := queuedEvent.Event

	if strings.TrimSpace(event.TargetID) == "" {
		handleDeliveryFailure(queuedEvent, "missing target id")
		return
	}

	blockedWords, err := database.GetBlockedWords(event.SourcePlatform, event.SourceID, event.TargetPlatform, event.TargetID)
	if err != nil {
		handleDeliveryFailure(queuedEvent, fmt.Sprintf("failed to read blocked words: %v", err))
		return
	}

	if containsBlockedWord(event.Caption, blockedWords) {
		observability.RegisterEventFiltered()
		observability.LogEvent("info", "event filtered by keyword rules", event.EventID, map[string]interface{}{
			"source_id": event.SourceID,
			"target_id": event.TargetID,
			"file_name": event.FileName,
		})
		ackEvent(event.EventID)
		return
	}

	pairing, err := database.GetPairing(event.SourcePlatform, event.SourceID, event.TargetPlatform, event.TargetID)
	if err == nil {
		event = applyFormatting(event, pairing.RuleConfig)
	}

	consumer, err := transport.GetConsumer(event.TargetPlatform)
	if err != nil {
		handleDeliveryFailure(queuedEvent, fmt.Sprintf("consumer not found for platform %s", event.TargetPlatform))
		return
	}

	// Dispatch to the transport consumer
	ctx, cancel := context.WithTimeout(context.Background(), processingLease-2*time.Second)
	defer cancel()

	if err := consumer.Send(ctx, event); err != nil {
		handleDeliveryFailure(queuedEvent, fmt.Sprintf("%s delivery failed: %v", event.TargetPlatform, err))
		return
	}

	ackEvent(event.EventID)
	observability.RegisterEventsForwarded(1)
	observability.LogEvent("info", "event forwarded", event.EventID, map[string]interface{}{
		"source_id": event.SourceID,
		"target_id": event.TargetID,
		"platform":  event.TargetPlatform,
	})
}

func containsBlockedWord(text string, words []string) bool {
	lowerText := strings.ToLower(text)
	for _, word := range words {
		if strings.Contains(lowerText, word) {
			return true
		}
	}
	return false
}

func applyFormatting(event models.MediaEvent, ruleConfig models.RuleConfig) models.MediaEvent {
	caption := event.Caption

	if ruleConfig.CaptionTemplate != "" {
		tpl := ruleConfig.CaptionTemplate
		tpl = strings.ReplaceAll(tpl, "{caption}", event.Caption)
		tpl = strings.ReplaceAll(tpl, "{sender}", event.SenderName)
		tpl = strings.ReplaceAll(tpl, "{source_chat}", event.SourceID)
		tpl = strings.ReplaceAll(tpl, "{time}", time.Now().Format("15:04"))
		caption = tpl
	}

	if event.ReplyToSender != "" {
		replyPreview := event.ReplyToCaption
		if len(replyPreview) > 80 {
			replyPreview = replyPreview[:77] + "..."
		}
		
		blockquote := fmt.Sprintf("> **In reply to %s:**\n> %s\n\n", event.ReplyToSender, replyPreview)
		caption = blockquote + caption
	}

	event.Caption = strings.TrimSpace(caption)
	return event
}

func handleDeliveryFailure(queuedEvent database.QueuedEvent, reason string) {
	observability.RegisterDeliveryFailure(reason)
	observability.LogEvent("warn", "delivery failure recorded", queuedEvent.Event.EventID, map[string]interface{}{
		"source_id": queuedEvent.Event.SourceID,
		"target_id": queuedEvent.Event.TargetID,
		"reason":    reason,
	})

	nextRetry := queuedEvent.RetryCount + 1
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown delivery failure"
	}

	if nextRetry > maxDeliveryRetries {
		deadLetterID, err := database.MovePendingEventToDeadLetter(queuedEvent.Event.EventID, reason)
		if err != nil {
			observability.LogEvent("error", "failed to move event to dead-letter queue", queuedEvent.Event.EventID, map[string]interface{}{
				"error": err.Error(),
			})
			return
		}
		observability.LogEvent("warn", "event moved to dead-letter queue", queuedEvent.Event.EventID, map[string]interface{}{
			"dead_letter_id": deadLetterID,
		})
		return
	}

	backoff := retryBaseDelay * time.Duration(1<<(nextRetry-1))
	if backoff > 300*time.Second {
		backoff = 300 * time.Second
	}
	nextAvailableAt := time.Now().Add(backoff)

	if err := database.ReschedulePendingEvent(queuedEvent.Event.EventID, nextRetry, nextAvailableAt, reason); err != nil {
		observability.LogEvent("error", "failed to reschedule event", queuedEvent.Event.EventID, map[string]interface{}{
			"error": err.Error(),
		})
	}
}

func ackEvent(eventID string) {
	if err := database.AckPendingEvent(eventID); err != nil {
		observability.LogEvent("warn", "failed to ack event", eventID, map[string]interface{}{"error": err.Error()})
	}
}
