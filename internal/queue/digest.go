package queue

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/security"
	"tg-discord-bot/internal/transport"
)

const (
	digestPollInterval = 60 * time.Second
	digestBatchLimit   = 25
	digestSendTimeout  = 20 * time.Second
)

// StartDigestScheduler starts a background loop to deliver digest summaries.
func StartDigestScheduler() {
	go func() {
		ticker := time.NewTicker(digestPollInterval)
		defer ticker.Stop()
		for range ticker.C {
			processDigests()
		}
	}()
}

func processDigests() {
	now := time.Now()
	groups, err := database.ListDigestGroupsDue(now, digestBatchLimit)
	if err != nil {
		observability.Log("warn", "failed to load digest groups", map[string]interface{}{"error": err.Error()})
		return
	}
	if len(groups) == 0 {
		return
	}

	for _, group := range groups {
		pairing, err := database.GetPairing(group.SourcePlatform, group.SourceID, group.TargetPlatform, group.TargetID)
		if err != nil {
			observability.Log("warn", "digest pairing not found", map[string]interface{}{
				"source_platform": group.SourcePlatform,
				"source_id":       group.SourceID,
				"target_platform": group.TargetPlatform,
				"target_id":       group.TargetID,
			})
			_ = database.DeleteDigestEventsForGroup(group.SourcePlatform, group.SourceID, group.TargetPlatform, group.TargetID, now)
			continue
		}

		maxItems := pairing.RuleConfig.DigestMaxItems
		if maxItems <= 0 {
			maxItems = 20
		}

		events, total, err := database.ListDigestEventsForGroup(group.SourcePlatform, group.SourceID, group.TargetPlatform, group.TargetID, now, maxItems)
		if err != nil {
			observability.Log("warn", "failed to load digest events", map[string]interface{}{"error": err.Error()})
			continue
		}
		if total == 0 {
			continue
		}

		message := buildDigestMessage(group, events, total)
		digestEventID := buildDigestEventID(group)
		destKey := group.TargetPlatform + ":" + group.TargetID
		if !security.CheckDestinationRateLimit(destKey) {
			observability.Log("info", "digest delivery delayed by destination rate limit", map[string]interface{}{
				"dest_key": destKey,
			})
			continue
		}
		consumer, err := transport.GetConsumer(group.TargetPlatform)
		if err != nil {
			observability.Log("warn", "digest consumer not found", map[string]interface{}{"error": err.Error()})
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), digestSendTimeout)
		err = consumer.Send(ctx, models.MediaEvent{
			EventID:        digestEventID,
			Caption:        message,
			SourcePlatform: group.SourcePlatform,
			SourceID:       group.SourceID,
			TargetPlatform: group.TargetPlatform,
			TargetID:       group.TargetID,
			MediaType:      models.MediaTypeDocument,
		})
		cancel()

		if err != nil {
			observability.Log("warn", "digest delivery failed", map[string]interface{}{"error": err.Error()})
			_ = database.InsertEventHistory(digestEventID, "digest_failed", err.Error())
			continue
		}

		if err := database.DeleteDigestEventsForGroup(group.SourcePlatform, group.SourceID, group.TargetPlatform, group.TargetID, now); err != nil {
			observability.Log("warn", "failed to delete digest events", map[string]interface{}{"error": err.Error()})
		}
		_ = database.InsertEventHistory(digestEventID, "digest_sent", fmt.Sprintf("digest sent with %d items", total))
	}
}

func buildDigestEventID(group database.DigestGroup) string {
	return fmt.Sprintf("digest:%s:%s:%s:%s:%d", group.SourcePlatform, group.SourceID, group.TargetPlatform, group.TargetID, time.Now().Unix())
}

func buildDigestMessage(group database.DigestGroup, events []database.DigestEvent, total int) string {
	lines := []string{
		fmt.Sprintf("Digest summary for %s:%s -> %s:%s", group.SourcePlatform, group.SourceID, group.TargetPlatform, group.TargetID),
		fmt.Sprintf("Items due: %d (showing %d)", total, len(events)),
	}

	for i, item := range events {
		label := strings.TrimSpace(item.FileName)
		if label == "" {
			label = strings.TrimSpace(item.MediaType)
		}
		if label == "" {
			label = "item"
		}
		caption := strings.TrimSpace(item.Caption)
		if caption != "" {
			caption = truncateDigestLine(caption, 100)
			lines = append(lines, fmt.Sprintf("%d) %s - %s", i+1, label, caption))
		} else {
			lines = append(lines, fmt.Sprintf("%d) %s", i+1, label))
		}
	}

	if total > len(events) {
		lines = append(lines, fmt.Sprintf("...and %d more items", total-len(events)))
	}

	return strings.Join(lines, "\n")
}

func truncateDigestLine(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= max {
		return trimmed
	}
	if max <= 3 {
		return trimmed[:max]
	}
	return trimmed[:max-3] + "..."
}
