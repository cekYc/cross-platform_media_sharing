package discord

import (
	"fmt"
	"strings"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/rules"
	"tg-discord-bot/internal/security"
	"time"

	"github.com/bwmarrin/discordgo"
)

func handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	if strings.HasPrefix(m.Content, "!") {
		// likely a command
		return
	}

	sourceID := m.ChannelID

	// Source rate limit check
	sourceKey := "discord:" + sourceID
	if !security.CheckSourceRateLimit(sourceKey) {
		observability.Log("warn", "source rate limit exceeded", map[string]interface{}{
			"source_key": sourceKey,
		})
		return
	}

	pairings, err := database.GetPairingsBySource("discord", sourceID)
	if err != nil || len(pairings) == 0 {
		return
	}

	senderName := m.Author.Username
	if m.Member != nil && m.Member.Nick != "" {
		senderName = m.Member.Nick
	}

	// We only process the first attachment for simplicity, or we can iterate
	// Discord sends files as attachments with URLs.
	for _, attachment := range m.Attachments {
		var validPairings []database.Pairing
		for _, pairing := range pairings {
			if !rules.EvaluateFilterRule(pairing.RuleConfig, m.Content, m.Author.ID) {
				continue
			}
			if !rules.EvaluateSpamRule(pairing.RuleConfig, sourceID, pairing.TargetID) {
				continue
			}
			if !rules.EvaluateFileRule(pairing.RuleConfig, int64(attachment.Size), attachment.ContentType) {
				continue
			}
			validPairings = append(validPairings, pairing)
		}

		if len(validPairings) == 0 {
			continue
		}

		mediaType := getMediaType(attachment.ContentType)

		for _, pairing := range validPairings {
			availableAt, _ := rules.EvaluateTimeRule(pairing.RuleConfig, time.Now())

			event := models.MediaEvent{
				EventID:        fmt.Sprintf("dc:%s:%s:%s:%s", m.ChannelID, m.ID, attachment.ID, pairing.TargetID),
				FileURL:        attachment.URL, // Direct URL to the attachment!
				FileName:       attachment.Filename,
				Caption:        m.Content,
				SourcePlatform: "discord",
				SourceID:       sourceID,
				TargetPlatform: pairing.TargetPlatform,
				TargetID:       pairing.TargetID,
				MediaType:      mediaType,
				ContentType:    attachment.ContentType,
				AvailableAt:    availableAt.Unix(),
				SenderName:     senderName,
			}

			enqueued, _ := database.EnqueuePendingEvent(event)
			if enqueued {
				observability.RegisterEventEnqueued()
				database.InsertEventHistory(event.EventID, "enqueued", "event added to processing queue")
			} else {
				database.InsertEventHistory(event.EventID, "skipped", "duplicate event detected")
			}
		}
	}

	// What if there are no attachments but just text? We should forward text!
	if len(m.Attachments) == 0 && strings.TrimSpace(m.Content) != "" {
		for _, pairing := range pairings {
			if !rules.EvaluateFilterRule(pairing.RuleConfig, m.Content, m.Author.ID) {
				continue
			}

			availableAt, _ := rules.EvaluateTimeRule(pairing.RuleConfig, time.Now())

			event := models.MediaEvent{
				EventID:        fmt.Sprintf("dc:%s:%s:text:%s", m.ChannelID, m.ID, pairing.TargetID),
				Caption:        m.Content,
				SourcePlatform: "discord",
				SourceID:       sourceID,
				TargetPlatform: pairing.TargetPlatform,
				TargetID:       pairing.TargetID,
				MediaType:      models.MediaTypeDocument, // Text
				AvailableAt:    availableAt.Unix(),
				SenderName:     senderName,
			}

			enqueued, _ := database.EnqueuePendingEvent(event)
			if enqueued {
				observability.RegisterEventEnqueued()
				database.InsertEventHistory(event.EventID, "enqueued", "text event added to processing queue")
			} else {
				database.InsertEventHistory(event.EventID, "skipped", "duplicate text event detected")
			}
		}
	}
}

func getMediaType(contentType string) string {
	if strings.HasPrefix(contentType, "image/") {
		if contentType == "image/gif" {
			return models.MediaTypeAnimation
		}
		return models.MediaTypePhoto
	}
	if strings.HasPrefix(contentType, "video/") {
		return models.MediaTypeVideo
	}
	return models.MediaTypeDocument
}
