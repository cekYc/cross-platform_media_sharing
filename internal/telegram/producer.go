package telegram

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	downloadRetries  = 3
	downloadTimeout  = 20 * time.Second
	fallbackMaxBytes = 20 * 1024 * 1024
)

type mediaPolicy struct {
	maxBytes        int64
	allowedPrefixes []string
	allowedExact    []string
}

var (
	downloadClient = &http.Client{Timeout: downloadTimeout}
	mediaPolicies  = map[string]mediaPolicy{
		models.MediaTypePhoto: {
			maxBytes:        12 * 1024 * 1024,
			allowedPrefixes: []string{"image/"},
		},
		models.MediaTypeVideo: {
			maxBytes:        30 * 1024 * 1024,
			allowedPrefixes: []string{"video/"},
		},
		models.MediaTypeAnimation: {
			maxBytes:        20 * 1024 * 1024,
			allowedPrefixes: []string{"video/", "image/"},
		},
		models.MediaTypeDocument: {
			maxBytes:        25 * 1024 * 1024,
			allowedPrefixes: []string{"image/", "video/", "audio/", "text/"},
			allowedExact: []string{
				"application/pdf",
				"application/json",
				"application/zip",
				"application/x-zip-compressed",
				"application/octet-stream",
			},
		},
	}
)

func StartProducer(token string) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("failed to initialize Telegram bot: %v", err)
	}

	observability.Log("info", "telegram producer initialized", map[string]interface{}{})

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatStringID := fmt.Sprintf("%d", update.Message.Chat.ID)

		if update.Message.IsCommand() {
			handleCommand(bot, update.Message, chatStringID)
			continue
		}

		fileID, fileName, mediaType, fileSize, declaredContentType := extractMediaMetadata(update.Message)
		if fileID == "" {
			continue
		}

		policy := policyForMediaType(mediaType)
		if policy.maxBytes > 0 && fileSize > policy.maxBytes {
			observability.Log("warn", "media exceeds configured size limit", map[string]interface{}{
				"media_type": mediaType,
				"file_name":  fileName,
				"file_size":  fileSize,
				"max_size":   policy.maxBytes,
			})
			continue
		}

		fileURL, err := bot.GetFileDirectURL(fileID)
		if err != nil {
			observability.Log("warn", "failed to resolve Telegram file URL", map[string]interface{}{"error": err.Error()})
			continue
		}

		data, err := downloadFile(fileURL, policy.maxBytes)
		if err != nil {
			observability.Log("warn", "telegram media download failed", map[string]interface{}{"error": err.Error()})
			continue
		}

		detectedContentType := normalizeContentType(http.DetectContentType(data))
		if !isAllowedContentType(policy, detectedContentType) {
			if !(detectedContentType == "application/octet-stream" && isAllowedContentType(policy, declaredContentType)) {
				observability.Log("warn", "media blocked by MIME policy", map[string]interface{}{
					"detected_content_type": detectedContentType,
					"file_name":             fileName,
				})
				continue
			}
		}

		if declaredContentType != "" && !isAllowedContentType(policy, declaredContentType) {
			observability.Log("warn", "media blocked by declared MIME policy", map[string]interface{}{
				"declared_content_type": declaredContentType,
				"file_name":             fileName,
			})
			continue
		}

		pairings, err := database.GetPairingsByTelegramChat(chatStringID)
		if err != nil {
			observability.Log("warn", "failed to load pairings for telegram chat", map[string]interface{}{
				"source_tg_id": chatStringID,
				"error":        err.Error(),
			})
			continue
		}

		if len(pairings) == 0 {
			continue
		}

		for _, pairing := range pairings {
			event := models.MediaEvent{
				EventID:      buildEventID(update.Message, fileID, pairing.DCChannelID),
				Data:         data,
				FileName:     fileName,
				Caption:      update.Message.Caption,
				SourceTGID:   chatStringID,
				TargetDCID:   pairing.DCChannelID,
				MediaGroupID: update.Message.MediaGroupID,
				MediaType:    mediaType,
				ContentType:  detectedContentType,
			}

			enqueued, err := database.EnqueuePendingEvent(event)
			if err != nil {
				observability.LogEvent("error", "failed to enqueue event", event.EventID, map[string]interface{}{
					"source_tg_id": chatStringID,
					"target_dc_id": pairing.DCChannelID,
					"file_name":    fileName,
					"error":        err.Error(),
				})
				continue
			}

			if !enqueued {
				observability.LogEvent("info", "duplicate event skipped", event.EventID, map[string]interface{}{
					"source_tg_id": chatStringID,
					"target_dc_id": pairing.DCChannelID,
					"file_name":    fileName,
				})
				continue
			}

			observability.RegisterEventEnqueued()
			observability.LogEvent("info", "event enqueued", event.EventID, map[string]interface{}{
				"source_tg_id": chatStringID,
				"target_dc_id": pairing.DCChannelID,
				"file_name":    fileName,
			})
		}
	}
}

func buildEventID(message *tgbotapi.Message, fileID, dcChannelID string) string {
	return fmt.Sprintf("tg:%d:%d:%s:%s", message.Chat.ID, message.MessageID, fileID, dcChannelID)
}

func handleCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, chatStringID string) {
	command := strings.ToLower(message.Command())
	args := strings.TrimSpace(message.CommandArguments())

	switch command {
	case "id", "chatid":
		reply := fmt.Sprintf("This chat ID is: `%s`\n\nIn Discord, run:\n`!join %s`", chatStringID, chatStringID)
		msg := tgbotapi.NewMessage(message.Chat.ID, reply)
		msg.ParseMode = "Markdown"
		sendTelegramMessage(bot, msg)
	case "status":
		handleStatusCommand(bot, message.Chat.ID, chatStringID)
	case "block":
		handleBlockCommand(bot, message.Chat.ID, chatStringID, args)
	case "blocklist":
		handleBlocklistCommand(bot, message.Chat.ID, chatStringID, args)
	case "unblock":
		handleUnblockCommand(bot, message.Chat.ID, chatStringID, args)
	case "clearblocks":
		handleClearBlocksCommand(bot, message.Chat.ID, chatStringID, args)
	case "help", "start":
		helpText := "Commands:\n" +
			"/id - show this Telegram chat ID\n" +
			"/status - show linked Discord channels\n" +
			"/block <word_or_phrase> - add blocked text for all linked channels\n" +
			"/block <discord_channel_id> <word_or_phrase> - add blocked text for one channel\n" +
			"/blocklist [discord_channel_id] - show blocked words\n" +
			"/unblock <word_or_phrase> - remove blocked text from all linked channels\n" +
			"/unblock <discord_channel_id> <word_or_phrase> - remove from one channel\n" +
			"/clearblocks [discord_channel_id] - clear blocked words"
		sendTelegramText(bot, message.Chat.ID, helpText)
	default:
		sendTelegramText(bot, message.Chat.ID, "Unknown command. Use /help")
	}
}

func handleStatusCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID string) {
	pairings, err := database.GetPairingsByTelegramChat(tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load link status.")
		log.Printf("[WARN] failed to load Telegram status: %v", err)
		return
	}

	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ This Telegram chat is not linked to any Discord channel yet.")
		return
	}

	lines := make([]string, 0, len(pairings))
	for _, pairing := range pairings {
		lines = append(lines, fmt.Sprintf("- `%s` (blocked words: %d)", pairing.DCChannelID, len(pairing.BlockedWords)))
	}

	sendTelegramText(bot, chatID, "Linked Discord channels:\n"+strings.Join(lines, "\n"))
}

func handleBlockCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args string) {
	pairings, err := database.GetPairingsByTelegramChat(tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load links for /block.")
		log.Printf("[WARN] failed to load links for /block: %v", err)
		return
	}
	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "❌ This chat is not linked yet. In Discord, run: !join <telegram_chat_id>")
		return
	}

	targetChannelID, word := resolveOptionalChannelArg(args, pairings)
	if strings.TrimSpace(word) == "" {
		sendTelegramText(bot, chatID, "Usage: /block <word_or_phrase> or /block <discord_channel_id> <word_or_phrase>")
		return
	}

	if targetChannelID != "" {
		if err := database.AddBlockedWord(tgChatID, targetChannelID, word); err != nil {
			sendTelegramText(bot, chatID, "❌ Failed to update block list for this channel.")
			log.Printf("[WARN] failed to add blocked word for channel %s: %v", targetChannelID, err)
			return
		}

		sendTelegramText(bot, chatID, fmt.Sprintf("✅ Added to block list for `%s`: `%s`", targetChannelID, word))
		return
	}

	updatedChannels, err := database.AddBlockedWordForAllChannels(tgChatID, word)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to update block list.")
		log.Printf("[WARN] failed to add blocked word across channels: %v", err)
		return
	}

	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Added to block list for %d linked channel(s): `%s`", updatedChannels, word))
}

func handleBlocklistCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args string) {
	pairings, err := database.GetPairingsByTelegramChat(tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load block list.")
		log.Printf("[WARN] failed to load block list: %v", err)
		return
	}

	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ No linked Discord channels found.")
		return
	}

	targetChannelID := strings.TrimSpace(args)
	if targetChannelID != "" {
		if strings.Contains(targetChannelID, " ") {
			sendTelegramText(bot, chatID, "Usage: /blocklist [discord_channel_id]")
			return
		}

		words, err := database.GetBlockedWords(tgChatID, targetChannelID)
		if errors.Is(err, sql.ErrNoRows) {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ `%s` is not linked to this Telegram chat.", targetChannelID))
			return
		}
		if err != nil {
			sendTelegramText(bot, chatID, "❌ Failed to load block list for this channel.")
			log.Printf("[WARN] failed to load block list for %s: %v", targetChannelID, err)
			return
		}

		if len(words) == 0 {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ Block list is empty for `%s`.", targetChannelID))
			return
		}

		sendTelegramText(bot, chatID, fmt.Sprintf("Block list for `%s`: %s", targetChannelID, strings.Join(words, ", ")))
		return
	}

	lines := make([]string, 0, len(pairings))
	for _, pairing := range pairings {
		if len(pairing.BlockedWords) == 0 {
			lines = append(lines, fmt.Sprintf("- `%s`: (empty)", pairing.DCChannelID))
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s`: %s", pairing.DCChannelID, strings.Join(pairing.BlockedWords, ", ")))
	}

	sendTelegramText(bot, chatID, "Block list by channel:\n"+strings.Join(lines, "\n"))
}

func handleUnblockCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args string) {
	pairings, err := database.GetPairingsByTelegramChat(tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load links for /unblock.")
		log.Printf("[WARN] failed to load links for /unblock: %v", err)
		return
	}
	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ No linked Discord channels found.")
		return
	}

	targetChannelID, word := resolveOptionalChannelArg(args, pairings)
	if strings.TrimSpace(word) == "" {
		sendTelegramText(bot, chatID, "Usage: /unblock <word_or_phrase> or /unblock <discord_channel_id> <word_or_phrase>")
		return
	}

	if targetChannelID != "" {
		removed, err := database.RemoveBlockedWord(tgChatID, targetChannelID, word)
		if errors.Is(err, sql.ErrNoRows) {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ `%s` is not linked to this Telegram chat.", targetChannelID))
			return
		}
		if err != nil {
			sendTelegramText(bot, chatID, "❌ Failed to update block list for this channel.")
			log.Printf("[WARN] failed to remove blocked word for %s: %v", targetChannelID, err)
			return
		}

		if !removed {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ `%s` is not blocked for `%s`.", word, targetChannelID))
			return
		}

		sendTelegramText(bot, chatID, fmt.Sprintf("✅ Removed `%s` from `%s`.", word, targetChannelID))
		return
	}

	removedChannels, err := database.RemoveBlockedWordFromAllChannels(tgChatID, word)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to update block list.")
		log.Printf("[WARN] failed to remove blocked word across channels: %v", err)
		return
	}

	if removedChannels == 0 {
		sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ `%s` was not found in any linked channel block list.", word))
		return
	}

	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Removed `%s` from %d linked channel(s).", word, removedChannels))
}

func handleClearBlocksCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args string) {
	pairings, err := database.GetPairingsByTelegramChat(tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load links for /clearblocks.")
		log.Printf("[WARN] failed to load links for /clearblocks: %v", err)
		return
	}
	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ No linked Discord channels found.")
		return
	}

	targetChannelID := strings.TrimSpace(args)
	if targetChannelID != "" {
		if strings.Contains(targetChannelID, " ") {
			sendTelegramText(bot, chatID, "Usage: /clearblocks [discord_channel_id]")
			return
		}

		if !isLinkedChannel(pairings, targetChannelID) {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ `%s` is not linked to this Telegram chat.", targetChannelID))
			return
		}

		if err := database.ClearBlockedWords(tgChatID, targetChannelID); err != nil {
			sendTelegramText(bot, chatID, "❌ Failed to clear block list for this channel.")
			log.Printf("[WARN] failed to clear blocked words for %s: %v", targetChannelID, err)
			return
		}

		sendTelegramText(bot, chatID, fmt.Sprintf("✅ Cleared block list for `%s`.", targetChannelID))
		return
	}

	clearedChannels, err := database.ClearBlockedWordsForAllChannels(tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to clear block list.")
		log.Printf("[WARN] failed to clear blocked words across channels: %v", err)
		return
	}

	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Cleared block list for %d linked channel(s).", clearedChannels))
}

func resolveOptionalChannelArg(args string, pairings []database.Pairing) (string, string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}

	parts := strings.Fields(trimmed)
	if len(parts) >= 2 && isLinkedChannel(pairings, parts[0]) {
		remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, parts[0]))
		return parts[0], remainder
	}

	return "", trimmed
}

func isLinkedChannel(pairings []database.Pairing, dcChannelID string) bool {
	for _, pairing := range pairings {
		if pairing.DCChannelID == dcChannelID {
			return true
		}
	}

	return false
}

func extractMediaMetadata(message *tgbotapi.Message) (string, string, string, int64, string) {
	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		return photo.FileID, "image.jpg", models.MediaTypePhoto, int64(photo.FileSize), "image/jpeg"
	}

	if message.Video != nil {
		fileName := message.Video.FileName
		if fileName == "" {
			fileName = "video.mp4"
		}

		return message.Video.FileID, fileName, models.MediaTypeVideo, int64(message.Video.FileSize), normalizeContentType(message.Video.MimeType)
	}

	if message.Animation != nil {
		fileName := message.Animation.FileName
		if fileName == "" {
			fileName = "animation.mp4"
		}

		return message.Animation.FileID, fileName, models.MediaTypeAnimation, int64(message.Animation.FileSize), normalizeContentType(message.Animation.MimeType)
	}

	if message.Document != nil {
		fileName := message.Document.FileName
		if fileName == "" {
			fileName = "document.bin"
		}

		return message.Document.FileID, fileName, models.MediaTypeDocument, int64(message.Document.FileSize), normalizeContentType(message.Document.MimeType)
	}

	return "", "", "", 0, ""
}

func policyForMediaType(mediaType string) mediaPolicy {
	policy, exists := mediaPolicies[mediaType]
	if !exists {
		return mediaPolicy{maxBytes: fallbackMaxBytes}
	}

	return policy
}

func normalizeContentType(contentType string) string {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType == "" {
		return ""
	}

	if separator := strings.Index(contentType, ";"); separator >= 0 {
		return strings.TrimSpace(contentType[:separator])
	}

	return contentType
}

func isAllowedContentType(policy mediaPolicy, contentType string) bool {
	normalized := normalizeContentType(contentType)
	if normalized == "" {
		return false
	}

	for _, exact := range policy.allowedExact {
		if normalized == normalizeContentType(exact) {
			return true
		}
	}

	for _, prefix := range policy.allowedPrefixes {
		if strings.HasPrefix(normalized, strings.ToLower(strings.TrimSpace(prefix))) {
			return true
		}
	}

	return false
}

func sendTelegramText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	sendTelegramMessage(bot, msg)
}

func sendTelegramMessage(bot *tgbotapi.BotAPI, msg tgbotapi.MessageConfig) {
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[WARN] failed to send Telegram response: %v", err)
	}
}

func downloadFile(url string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = fallbackMaxBytes
	}

	var lastErr error

	for attempt := 1; attempt <= downloadRetries; attempt++ {
		data, err := downloadFileOnce(url, maxBytes)
		if err == nil {
			return data, nil
		}

		lastErr = err
		if attempt < downloadRetries {
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
	}

	return nil, fmt.Errorf("download failed after %d attempts: %w", downloadRetries, lastErr)
}

func downloadFileOnce(url string, maxBytes int64) ([]byte, error) {
	resp, err := downloadClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("file too large: %d bytes (limit: %d bytes)", resp.ContentLength, maxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("file exceeds maximum allowed size")
	}

	return data, nil
}
