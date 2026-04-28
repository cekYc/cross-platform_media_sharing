package telegram

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/i18n"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/rules"
	"tg-discord-bot/internal/security"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var botInstance *tgbotapi.BotAPI
var trustedTelegramUserIDs = map[string]struct{}{}

type Producer struct {
	token string
}

func NewProducer(token string) *Producer {
	return &Producer{token: token}
}

func (p *Producer) Start() {
	bot, err := tgbotapi.NewBotAPI(p.token)
	if err != nil {
		log.Fatalf("failed to initialize Telegram bot: %v", err)
	}
	botInstance = bot

	// Parse trusted user IDs from env
	for _, id := range strings.Split(os.Getenv("TELEGRAM_TRUSTED_USER_IDS"), ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			trustedTelegramUserIDs[id] = struct{}{}
		}
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

		// Source rate limit check
		sourceKey := "telegram:" + chatStringID
		if !security.CheckSourceRateLimit(sourceKey) {
			observability.Log("warn", "source rate limit exceeded", map[string]interface{}{
				"source_key": sourceKey,
			})
			continue
		}

		pairings, err := database.GetPairingsBySource("telegram", chatStringID)
		if err != nil {
			observability.Log("warn", "failed to load pairings for telegram chat", map[string]interface{}{
				"source_id": chatStringID,
				"error":     err.Error(),
			})
			continue
		}

		if len(pairings) == 0 {
			continue
		}

		senderID := ""
		senderName := ""
		if update.Message.From != nil {
			senderID = fmt.Sprintf("%d", update.Message.From.ID)
			senderName = strings.TrimSpace(update.Message.From.FirstName + " " + update.Message.From.LastName)
			if senderName == "" {
				senderName = update.Message.From.UserName
			}
		}

		replyToSender := ""
		replyToCaption := ""
		if update.Message.ReplyToMessage != nil {
			if update.Message.ReplyToMessage.From != nil {
				replyToSender = strings.TrimSpace(update.Message.ReplyToMessage.From.FirstName + " " + update.Message.ReplyToMessage.From.LastName)
				if replyToSender == "" {
					replyToSender = update.Message.ReplyToMessage.From.UserName
				}
			}
			replyToCaption = update.Message.ReplyToMessage.Text
			if replyToCaption == "" {
				replyToCaption = update.Message.ReplyToMessage.Caption
			}
		}

		var validPairings []database.Pairing
		for _, pairing := range pairings {
			if !rules.EvaluateFilterRule(pairing.RuleConfig, update.Message.Caption, senderID) {
				if pairing.RuleConfig.SimulationMode {
					recordSimulation(buildEventID(update.Message, fileID, pairing.TargetID), "filter rule blocked", chatStringID, pairing.TargetID)
					validPairings = append(validPairings, pairing)
				}
				continue
			}
			if !rules.EvaluateTagRule(pairing.RuleConfig, update.Message.Caption) {
				if pairing.RuleConfig.SimulationMode {
					recordSimulation(buildEventID(update.Message, fileID, pairing.TargetID), "required tags missing", chatStringID, pairing.TargetID)
					validPairings = append(validPairings, pairing)
				}
				continue
			}
			if !rules.EvaluateSpamRule(pairing.RuleConfig, chatStringID, pairing.TargetID) {
				if pairing.RuleConfig.SimulationMode {
					recordSimulation(buildEventID(update.Message, fileID, pairing.TargetID), "spam rule blocked", chatStringID, pairing.TargetID)
					validPairings = append(validPairings, pairing)
					continue
				}
				observability.Log("info", "message dropped by spam rule", map[string]interface{}{
					"source_id": chatStringID,
					"target_id": pairing.TargetID,
				})
				continue
			}
			if !rules.EvaluateFileRule(pairing.RuleConfig, fileSize, declaredContentType) {
				if pairing.RuleConfig.SimulationMode {
					recordSimulation(buildEventID(update.Message, fileID, pairing.TargetID), "file rule blocked", chatStringID, pairing.TargetID)
					validPairings = append(validPairings, pairing)
				}
				continue
			}
			validPairings = append(validPairings, pairing)
		}

		if len(validPairings) == 0 {
			continue
		}

		fileURL, err := bot.GetFileDirectURL(fileID)
		if err != nil {
			observability.Log("warn", "failed to resolve Telegram file URL", map[string]interface{}{"error": err.Error()})
			continue
		}

		fileHash, err := computeRemoteFileHash(fileURL)
		if err != nil {
			observability.Log("warn", "failed to compute telegram media hash", map[string]interface{}{"error": err.Error()})
		}

		for _, pairing := range validPairings {
			availableAt, _ := rules.EvaluateTimeRule(pairing.RuleConfig, time.Now())

			event := models.MediaEvent{
				EventID:        buildEventID(update.Message, fileID, pairing.TargetID),
				FileURL:        fileURL,
				FileName:       fileName,
				Caption:        update.Message.Caption,
				SourcePlatform: "telegram",
				SourceID:       chatStringID,
				TargetPlatform: pairing.TargetPlatform,
				TargetID:       pairing.TargetID,
				MediaGroupID:   update.Message.MediaGroupID,
				MediaType:      mediaType,
				ContentType:    declaredContentType,
				AvailableAt:    availableAt.Unix(),
				SenderName:     senderName,
				ReplyToSender:  replyToSender,
				ReplyToCaption: replyToCaption,
				FileHash:       fileHash,
			}

			enqueued, err := database.EnqueuePendingEvent(event)
			if err != nil {
				observability.LogEvent("error", "failed to enqueue event", event.EventID, map[string]interface{}{
					"source_id": chatStringID,
					"target_id": pairing.TargetID,
					"file_name": fileName,
					"error":     err.Error(),
				})
				continue
			}

			if !enqueued {
				observability.LogEvent("info", "duplicate event skipped", event.EventID, map[string]interface{}{
					"source_id": chatStringID,
					"target_id": pairing.TargetID,
					"file_name": fileName,
				})
				database.InsertEventHistory(event.EventID, "skipped", "duplicate event detected")
				continue
			}

			observability.RegisterEventEnqueued()
			observability.LogEvent("info", "event enqueued", event.EventID, map[string]interface{}{
				"source_id": chatStringID,
				"target_id": pairing.TargetID,
				"file_name": fileName,
			})
			database.InsertEventHistory(event.EventID, "enqueued", "event added to processing queue")
		}
	}
}

func computeRemoteFileHash(fileURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fileURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code when hashing file: %d", resp.StatusCode)
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, resp.Body); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func recordSimulation(eventID, reason, sourceID, targetID string) {
	observability.LogEvent("info", "simulation: rule would block", eventID, map[string]interface{}{
		"source_id": sourceID,
		"target_id": targetID,
		"reason":    reason,
	})
	_ = database.InsertEventHistory(eventID, "simulated_filter", reason)
}

func buildEventID(message *tgbotapi.Message, fileID, targetID string) string {
	return fmt.Sprintf("tg:%d:%d:%s:%s", message.Chat.ID, message.MessageID, fileID, targetID)
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
		return message.Video.FileID, fileName, models.MediaTypeVideo, int64(message.Video.FileSize), message.Video.MimeType
	}

	if message.Animation != nil {
		fileName := message.Animation.FileName
		if fileName == "" {
			fileName = "animation.mp4"
		}
		return message.Animation.FileID, fileName, models.MediaTypeAnimation, int64(message.Animation.FileSize), message.Animation.MimeType
	}

	if message.Document != nil {
		fileName := message.Document.FileName
		if fileName == "" {
			fileName = "document.bin"
		}
		return message.Document.FileID, fileName, models.MediaTypeDocument, int64(message.Document.FileSize), message.Document.MimeType
	}

	return "", "", "", 0, ""
}

// isAuthorizedTelegramUser checks if a Telegram user is trusted for admin commands.
// Admin-only commands (block, unblock, clearblocks, setrule) require trusted user status or
// a chat admin role. Read-only commands (id, help, start, status) are available to everyone.
func isAuthorizedTelegramUser(userID string) bool {
	// If no trusted user list is configured, allow all (backward compatible)
	if len(trustedTelegramUserIDs) == 0 {
		return true
	}
	_, trusted := trustedTelegramUserIDs[userID]
	return trusted
}

func handleCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, chatStringID string) {
	command := strings.ToLower(message.Command())
	args := strings.TrimSpace(message.CommandArguments())

	senderID := ""
	if message.From != nil {
		senderID = fmt.Sprintf("%d", message.From.ID)
	}

	switch command {
	case "id", "chatid":
		reply := fmt.Sprintf("This chat ID is: `%s`\n\nIn Discord, run:\n`!join %s`", chatStringID, chatStringID)
		msg := tgbotapi.NewMessage(message.Chat.ID, reply)
		msg.ParseMode = "Markdown"
		sendTelegramMessage(bot, msg)
	case "status":
		handleStatusCommand(bot, message.Chat.ID, chatStringID)
	case "block":
		if !isAuthorizedTelegramUser(senderID) {
			sendTelegramText(bot, message.Chat.ID, "❌ You do not have permission to use this command.")
			return
		}
		handleBlockCommand(bot, message.Chat.ID, chatStringID, args, senderID)
	case "blocklist":
		handleBlocklistCommand(bot, message.Chat.ID, chatStringID, args)
	case "unblock":
		if !isAuthorizedTelegramUser(senderID) {
			sendTelegramText(bot, message.Chat.ID, "❌ You do not have permission to use this command.")
			return
		}
		handleUnblockCommand(bot, message.Chat.ID, chatStringID, args, senderID)
	case "clearblocks":
		if !isAuthorizedTelegramUser(senderID) {
			sendTelegramText(bot, message.Chat.ID, "❌ You do not have permission to use this command.")
			return
		}
		handleClearBlocksCommand(bot, message.Chat.ID, chatStringID, args, senderID)
	case "setrule":
		if !isAuthorizedTelegramUser(senderID) {
			sendTelegramText(bot, message.Chat.ID, "❌ You do not have permission to use this command.")
			return
		}
		handleSetRuleCommand(bot, message.Chat.ID, chatStringID, args, senderID)
	case "start":
		msg := tgbotapi.NewMessage(message.Chat.ID, i18n.Get("en", "welcome_step1")+"\n\n"+fmt.Sprintf(i18n.Get("en", "welcome_step2"), chatStringID))
		msg.ParseMode = "Markdown"
		sendTelegramMessage(bot, msg)
	case "help":
		helpText := i18n.Get("en", "help_title") + "\n" +
			"/id - " + i18n.Get("en", "help_id") + "\n" +
			"/status - " + i18n.Get("en", "help_status") + "\n" +
			"/block <word> - " + i18n.Get("en", "help_block") + "\n" +
			"/blocklist - " + i18n.Get("en", "help_blocklist") + "\n" +
			"/unblock <word> - " + i18n.Get("en", "help_unblock") + "\n" +
			"/clearblocks - " + i18n.Get("en", "help_clearblocks") + "\n" +
			"/setrule <json> - " + i18n.Get("en", "help_setrule")
		sendTelegramText(bot, message.Chat.ID, helpText)
	default:
		sendTelegramText(bot, message.Chat.ID, i18n.Get("en", "help_unknown"))
	}
}

func handleStatusCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID string) {
	pairings, err := database.GetPairingsBySource("telegram", tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load link status.")
		log.Printf("[WARN] failed to load Telegram status: %v", err)
		return
	}

	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ This Telegram chat is not linked to any target yet.")
		return
	}

	lines := make([]string, 0, len(pairings))
	for _, pairing := range pairings {
		lines = append(lines, fmt.Sprintf("- [%s] `%s` (blocked words: %d)", pairing.TargetPlatform, pairing.TargetID, len(pairing.BlockedWords)))
	}

	sendTelegramText(bot, chatID, "Linked targets:\n"+strings.Join(lines, "\n"))
}

func handleBlockCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args, actorID string) {
	pairings, err := database.GetPairingsBySource("telegram", tgChatID)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to load links for /block.")
		return
	}
	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "❌ This chat is not linked yet.")
		return
	}

	targetID, word := resolveOptionalTargetArg(args, pairings)
	if strings.TrimSpace(word) == "" {
		sendTelegramText(bot, chatID, "Usage: /block <word> or /block <target_id> <word>")
		return
	}

	if targetID != "" {
		// Find pairing to get platform
		var targetPlatform string
		for _, p := range pairings {
			if p.TargetID == targetID {
				targetPlatform = p.TargetPlatform
				break
			}
		}

		if err := database.AddBlockedWord("telegram", tgChatID, targetPlatform, targetID, word); err != nil {
			sendTelegramText(bot, chatID, "❌ Failed to update block list.")
			return
		}
		database.InsertAuditLog("block", "telegram", actorID, fmt.Sprintf("added '%s' to telegram:%s->%s:%s", word, tgChatID, targetPlatform, targetID))
		sendTelegramText(bot, chatID, fmt.Sprintf("✅ Added to block list for `%s`: `%s`", targetID, word))
		return
	}

	updated, err := database.AddBlockedWordForAllTargets("telegram", tgChatID, word)
	if err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to update block list.")
		return
	}
	database.InsertAuditLog("block", "telegram", actorID, fmt.Sprintf("added '%s' to all targets of telegram:%s (%d targets)", word, tgChatID, updated))
	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Added to block list for %d linked target(s): `%s`", updated, word))
}

func handleBlocklistCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args string) {
	pairings, err := database.GetPairingsBySource("telegram", tgChatID)
	if err != nil || len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ No linked targets found.")
		return
	}

	targetID := strings.TrimSpace(args)
	if targetID != "" {
		var targetPlatform string
		for _, p := range pairings {
			if p.TargetID == targetID {
				targetPlatform = p.TargetPlatform
				break
			}
		}
		if targetPlatform == "" {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ `%s` is not linked.", targetID))
			return
		}
		words, _ := database.GetBlockedWords("telegram", tgChatID, targetPlatform, targetID)
		if len(words) == 0 {
			sendTelegramText(bot, chatID, fmt.Sprintf("ℹ️ Block list is empty for `%s`.", targetID))
			return
		}
		sendTelegramText(bot, chatID, fmt.Sprintf("Block list for `%s`: %s", targetID, strings.Join(words, ", ")))
		return
	}

	lines := make([]string, 0, len(pairings))
	for _, p := range pairings {
		if len(p.BlockedWords) == 0 {
			lines = append(lines, fmt.Sprintf("- [%s] `%s`: (empty)", p.TargetPlatform, p.TargetID))
		} else {
			lines = append(lines, fmt.Sprintf("- [%s] `%s`: %s", p.TargetPlatform, p.TargetID, strings.Join(p.BlockedWords, ", ")))
		}
	}
	sendTelegramText(bot, chatID, "Block list by target:\n"+strings.Join(lines, "\n"))
}

func handleUnblockCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args, actorID string) {
	pairings, _ := database.GetPairingsBySource("telegram", tgChatID)
	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ No linked targets found.")
		return
	}

	targetID, word := resolveOptionalTargetArg(args, pairings)
	if strings.TrimSpace(word) == "" {
		sendTelegramText(bot, chatID, "Usage: /unblock <word> or /unblock <target_id> <word>")
		return
	}

	if targetID != "" {
		var targetPlatform string
		for _, p := range pairings {
			if p.TargetID == targetID {
				targetPlatform = p.TargetPlatform
				break
			}
		}
		database.RemoveBlockedWord("telegram", tgChatID, targetPlatform, targetID, word)
		database.InsertAuditLog("unblock", "telegram", actorID, fmt.Sprintf("removed '%s' from telegram:%s->%s:%s", word, tgChatID, targetPlatform, targetID))
		sendTelegramText(bot, chatID, fmt.Sprintf("✅ Removed `%s` from `%s`.", word, targetID))
		return
	}

	removed, _ := database.RemoveBlockedWordFromAllTargets("telegram", tgChatID, word)
	database.InsertAuditLog("unblock", "telegram", actorID, fmt.Sprintf("removed '%s' from all targets of telegram:%s (%d targets)", word, tgChatID, removed))
	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Removed `%s` from %d linked target(s).", word, removed))
}

func handleClearBlocksCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args, actorID string) {
	pairings, _ := database.GetPairingsBySource("telegram", tgChatID)
	if len(pairings) == 0 {
		sendTelegramText(bot, chatID, "ℹ️ No linked targets found.")
		return
	}

	targetID := strings.TrimSpace(args)
	if targetID != "" {
		var targetPlatform string
		for _, p := range pairings {
			if p.TargetID == targetID {
				targetPlatform = p.TargetPlatform
				break
			}
		}
		database.ClearBlockedWords("telegram", tgChatID, targetPlatform, targetID)
		database.InsertAuditLog("clearblocks", "telegram", actorID, fmt.Sprintf("cleared blocks for telegram:%s->%s:%s", tgChatID, targetPlatform, targetID))
		sendTelegramText(bot, chatID, fmt.Sprintf("✅ Cleared block list for `%s`.", targetID))
		return
	}

	cleared, _ := database.ClearBlockedWordsForAllTargets("telegram", tgChatID)
	database.InsertAuditLog("clearblocks", "telegram", actorID, fmt.Sprintf("cleared blocks for all targets of telegram:%s (%d targets)", tgChatID, cleared))
	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Cleared block list for %d linked target(s).", cleared))
}

func handleSetRuleCommand(bot *tgbotapi.BotAPI, chatID int64, tgChatID, args, actorID string) {
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		sendTelegramText(bot, chatID, "Usage: /setrule <target_id> <json_string>")
		return
	}

	targetID := parts[0]
	jsonStr := strings.TrimPrefix(parts[1], "```json")
	jsonStr = strings.TrimPrefix(jsonStr, "```")
	jsonStr = strings.TrimSuffix(jsonStr, "```")
	jsonStr = strings.TrimSpace(jsonStr)

	var config models.RuleConfig
	if err := json.Unmarshal([]byte(jsonStr), &config); err != nil {
		sendTelegramText(bot, chatID, "❌ Invalid JSON format: "+err.Error())
		return
	}

	pairings, _ := database.GetPairingsBySource("telegram", tgChatID)
	var targetPlatform string
	for _, p := range pairings {
		if p.TargetID == targetID {
			targetPlatform = p.TargetPlatform
			break
		}
	}

	if targetPlatform == "" {
		sendTelegramText(bot, chatID, "❌ Target not linked.")
		return
	}

	if err := database.UpdateRuleConfig("telegram", tgChatID, targetPlatform, targetID, config); err != nil {
		sendTelegramText(bot, chatID, "❌ Failed to update rules.")
		return
	}

	database.InsertAuditLog("setrule", "telegram", actorID, fmt.Sprintf("updated rules for telegram:%s->%s:%s", tgChatID, targetPlatform, targetID))

	sendTelegramText(bot, chatID, fmt.Sprintf("✅ Successfully updated rules for `%s`.", targetID))
}

func resolveOptionalTargetArg(args string, pairings []database.Pairing) (string, string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "", ""
	}

	parts := strings.Fields(trimmed)
	if len(parts) >= 2 {
		for _, p := range pairings {
			if p.TargetID == parts[0] {
				remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, parts[0]))
				return parts[0], remainder
			}
		}
	}

	return "", trimmed
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
