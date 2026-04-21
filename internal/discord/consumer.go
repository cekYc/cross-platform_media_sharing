package discord

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	defaultDuplicateWindowSeconds = 600
	defaultQueuePollMilliseconds  = 250
	defaultProcessingLeaseSeconds = 30
	defaultMaxDeliveryRetries     = 5
	defaultRetryBaseSeconds       = 2
	maxRetryBackoffSeconds        = 300

	albumCollectWindow = 3 * time.Second
	albumIdleTimeout   = 700 * time.Millisecond
)

var Session *discordgo.Session

type albumBatch struct {
	items     []database.QueuedEvent
	firstSeen time.Time
	lastSeen  time.Time
}

var (
	albumCache = make(map[string]*albumBatch)
	albumMutex sync.Mutex

	seenMediaHashes = make(map[string]time.Time)
	seenMediaMutex  sync.Mutex

	adminRoleIDs    = map[string]struct{}{}
	duplicateWindow = time.Duration(defaultDuplicateWindowSeconds) * time.Second

	queuePollInterval  = time.Duration(defaultQueuePollMilliseconds) * time.Millisecond
	processingLease    = time.Duration(defaultProcessingLeaseSeconds) * time.Second
	maxDeliveryRetries = defaultMaxDeliveryRetries
	retryBaseDelay     = time.Duration(defaultRetryBaseSeconds) * time.Second
)

func InitBot(token string) {
	var err error
	Session, err = discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("failed to create Discord bot:", err)
	}

	adminRoleIDs = parseRoleIDs(os.Getenv("DISCORD_ADMIN_ROLE_IDS"))
	duplicateWindow = resolveDuplicateWindow()
	resolveQueueSettings()

	Session.AddHandler(handleCommand)

	err = Session.Open()
	if err != nil {
		log.Fatal("failed to open Discord connection:", err)
	}
	observability.Log("info", "discord bot connected and command listener active", map[string]interface{}{})
}

func handleCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	parts := strings.Fields(strings.TrimSpace(m.Content))
	if len(parts) == 0 {
		return
	}

	command := strings.ToLower(parts[0])
	if !isManagedCommand(command) {
		return
	}

	if !isAuthorizedAdminCommand(s, m) {
		sendChannelMessage(s, m.ChannelID, "❌ You do not have permission to use bridge admin commands in this channel.")
		return
	}

	switch command {
	case "!join":
		handleJoinCommand(s, m, parts)
	case "!unlink":
		handleUnlinkCommand(s, m, parts)
	case "!status":
		handleStatusCommand(s, m, parts)
	case "!blocklist":
		handleBlocklistCommand(s, m, parts)
	case "!unblock":
		handleUnblockCommand(s, m, parts)
	case "!clearblocks":
		handleClearBlocksCommand(s, m, parts)
	case "!deadletters":
		handleDeadLettersCommand(s, m, parts)
	case "!replaydead":
		handleReplayDeadCommand(s, m, parts)
	case "!help":
		handleHelpCommand(s, m.ChannelID)
	}
}

func isManagedCommand(command string) bool {
	switch command {
	case "!join", "!unlink", "!status", "!help", "!blocklist", "!unblock", "!clearblocks", "!deadletters", "!replaydead":
		return true
	default:
		return false
	}
}

func isAuthorizedAdminCommand(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	if m.GuildID == "" {
		return false
	}

	permissions, err := s.State.UserChannelPermissions(m.Author.ID, m.ChannelID)
	if err != nil {
		permissions, err = s.UserChannelPermissions(m.Author.ID, m.ChannelID)
	}

	if err == nil && permissions&discordgo.PermissionManageChannels != 0 {
		return true
	}

	if len(adminRoleIDs) == 0 {
		return false
	}

	member := m.Member
	if member == nil {
		member, err = s.GuildMember(m.GuildID, m.Author.ID)
		if err != nil {
			return false
		}
	}

	for _, roleID := range member.Roles {
		if _, allowed := adminRoleIDs[roleID]; allowed {
			return true
		}
	}

	return false
}

func parseRoleIDs(raw string) map[string]struct{} {
	roleIDs := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		roleID := strings.TrimSpace(part)
		if roleID == "" {
			continue
		}
		roleIDs[roleID] = struct{}{}
	}

	return roleIDs
}

func resolveDuplicateWindow() time.Duration {
	value := strings.TrimSpace(os.Getenv("DUPLICATE_WINDOW_SECONDS"))
	if value == "" {
		return time.Duration(defaultDuplicateWindowSeconds) * time.Second
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		log.Printf("[WARN] invalid DUPLICATE_WINDOW_SECONDS value (%q), using default: %d", value, defaultDuplicateWindowSeconds)
		return time.Duration(defaultDuplicateWindowSeconds) * time.Second
	}

	return time.Duration(seconds) * time.Second
}

func resolveQueueSettings() {
	queuePollInterval = time.Duration(resolvePositiveIntEnv("QUEUE_POLL_MILLISECONDS", defaultQueuePollMilliseconds)) * time.Millisecond
	processingLease = time.Duration(resolvePositiveIntEnv("QUEUE_PROCESSING_LEASE_SECONDS", defaultProcessingLeaseSeconds)) * time.Second
	maxDeliveryRetries = resolvePositiveIntEnv("DELIVERY_MAX_RETRIES", defaultMaxDeliveryRetries)
	retryBaseDelay = time.Duration(resolvePositiveIntEnv("DELIVERY_RETRY_BASE_SECONDS", defaultRetryBaseSeconds)) * time.Second
}

func resolvePositiveIntEnv(name string, defaultValue int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("[WARN] invalid %s value (%q), using default: %d", name, value, defaultValue)
		return defaultValue
	}

	return parsed
}

func handleHelpCommand(s *discordgo.Session, channelID string) {
	message := "Commands:\n" +
		"`!join <telegram_chat_id>` - link this Discord channel to a Telegram chat\n" +
		"`!unlink <telegram_chat_id>` - remove one Telegram link from this channel\n" +
		"`!status [telegram_chat_id]` - show link and filter status\n" +
		"`!blocklist [telegram_chat_id]` - show blocked words\n" +
		"`!unblock <word_or_phrase>` - remove a blocked word (single-link channels)\n" +
		"`!unblock <telegram_chat_id> <word_or_phrase>` - remove from a specific link\n" +
		"`!clearblocks [telegram_chat_id]` - clear blocked words for one link\n" +
		"`!deadletters [limit]` - show failed deliveries for this channel\n" +
		"`!replaydead <dead_letter_id>` - replay one dead-letter item\n" +
		"`!help` - show this help"
	sendChannelMessage(s, channelID, message)
}

func handleJoinCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) != 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!join <telegram_chat_id>`")
		return
	}

	tgID := strings.TrimSpace(parts[1])
	if err := database.LinkChannel(tgID, m.ChannelID); err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Database error while linking this channel.")
		log.Println("database error while linking channel:", err)
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Linked Telegram chat `%s` to this Discord channel.", tgID))
}

func handleUnlinkCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) != 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!unlink <telegram_chat_id>`")
		return
	}

	tgID := strings.TrimSpace(parts[1])
	removed, err := database.UnlinkChannel(tgID, m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Database error while unlinking this channel.")
		log.Println("database error while unlinking channel:", err)
		return
	}

	if !removed {
		sendChannelMessage(s, m.ChannelID, fmt.Sprintf("ℹ️ No link found for Telegram chat `%s` in this channel.", tgID))
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Unlinked Telegram chat `%s` from this Discord channel.", tgID))
}

func handleStatusCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) > 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!status [telegram_chat_id]`")
		return
	}

	pairings, err := database.GetPairingsByDiscordChannel(m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load status from database.")
		log.Println("database error while loading status:", err)
		return
	}

	if len(pairings) == 0 {
		sendChannelMessage(s, m.ChannelID, "ℹ️ This Discord channel is not linked to any Telegram chat.")
		return
	}

	if len(parts) == 2 {
		tgID := strings.TrimSpace(parts[1])
		pairing, userMessage := resolvePairingForChannel(pairings, tgID)
		if userMessage != "" {
			sendChannelMessage(s, m.ChannelID, userMessage)
			return
		}

		message := fmt.Sprintf("Status for Telegram chat `%s` in this channel:\nBlocked words: %d", pairing.TGChatID, len(pairing.BlockedWords))
		if len(pairing.BlockedWords) > 0 {
			message += "\nList: " + strings.Join(pairing.BlockedWords, ", ")
		}
		sendChannelMessage(s, m.ChannelID, message)
		return
	}

	lines := make([]string, 0, len(pairings))
	for _, pairing := range pairings {
		lines = append(lines, fmt.Sprintf("- `%s` (blocked words: %d)", pairing.TGChatID, len(pairing.BlockedWords)))
	}
	sort.Strings(lines)

	sendChannelMessage(s, m.ChannelID, "Linked Telegram chats for this channel:\n"+strings.Join(lines, "\n"))
}

func handleBlocklistCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) > 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!blocklist [telegram_chat_id]`")
		return
	}

	pairings, err := database.GetPairingsByDiscordChannel(m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load block list from database.")
		log.Println("database error while loading block list:", err)
		return
	}

	tgID := ""
	if len(parts) == 2 {
		tgID = strings.TrimSpace(parts[1])
	}

	pairing, userMessage := resolvePairingForChannel(pairings, tgID)
	if userMessage != "" {
		sendChannelMessage(s, m.ChannelID, userMessage)
		return
	}

	if len(pairing.BlockedWords) == 0 {
		sendChannelMessage(s, m.ChannelID, fmt.Sprintf("ℹ️ Block list is empty for Telegram chat `%s`.", pairing.TGChatID))
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("Blocked words for `%s`: %s", pairing.TGChatID, strings.Join(pairing.BlockedWords, ", ")))
}

func handleUnblockCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) < 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!unblock <word_or_phrase>` or `!unblock <telegram_chat_id> <word_or_phrase>`")
		return
	}

	pairings, err := database.GetPairingsByDiscordChannel(m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load links from database.")
		log.Println("database error while preparing unblock:", err)
		return
	}

	if len(pairings) == 0 {
		sendChannelMessage(s, m.ChannelID, "ℹ️ This channel is not linked to any Telegram chat.")
		return
	}

	targetTGID, word := parseUnblockCommand(pairings, parts[1:])
	if targetTGID == "" {
		sendChannelMessage(s, m.ChannelID, "❌ Multiple Telegram chats are linked. Use `!unblock <telegram_chat_id> <word_or_phrase>`.")
		return
	}

	if strings.TrimSpace(word) == "" {
		sendChannelMessage(s, m.ChannelID, "Usage: `!unblock <word_or_phrase>` or `!unblock <telegram_chat_id> <word_or_phrase>`")
		return
	}

	removed, err := database.RemoveBlockedWord(targetTGID, m.ChannelID, word)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to update block list.")
		log.Println("database error while unblocking word:", err)
		return
	}

	if !removed {
		sendChannelMessage(s, m.ChannelID, fmt.Sprintf("ℹ️ `%s` is not in the block list for `%s`.", word, targetTGID))
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Removed `%s` from block list for `%s`.", word, targetTGID))
}

func handleClearBlocksCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) > 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!clearblocks [telegram_chat_id]`")
		return
	}

	pairings, err := database.GetPairingsByDiscordChannel(m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load links from database.")
		log.Println("database error while loading links:", err)
		return
	}

	tgID := ""
	if len(parts) == 2 {
		tgID = strings.TrimSpace(parts[1])
	}

	pairing, userMessage := resolvePairingForChannel(pairings, tgID)
	if userMessage != "" {
		sendChannelMessage(s, m.ChannelID, userMessage)
		return
	}

	if err := database.ClearBlockedWords(pairing.TGChatID, m.ChannelID); err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to clear block list.")
		log.Println("database error while clearing block list:", err)
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Cleared block list for `%s`.", pairing.TGChatID))
}

func handleDeadLettersCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	limit := 5
	if len(parts) > 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!deadletters [limit]`")
		return
	}
	if len(parts) == 2 {
		parsed, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || parsed <= 0 {
			sendChannelMessage(s, m.ChannelID, "Usage: `!deadletters [limit]` (limit must be a positive number)")
			return
		}
		if parsed > 20 {
			parsed = 20
		}
		limit = parsed
	}

	items, err := database.ListDeadLettersByChannel(m.ChannelID, limit)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load dead-letter items.")
		log.Printf("[WARN] failed to load dead letters: %v", err)
		return
	}

	if len(items) == 0 {
		sendChannelMessage(s, m.ChannelID, "ℹ️ No dead-letter items for this channel.")
		return
	}

	lines := make([]string, 0, len(items))
	for _, item := range items {
		reason := truncateReason(item.FailureReason, 80)
		lines = append(lines, fmt.Sprintf("- id `%d` | file `%s` | retries `%d` | failed `%s` | reason `%s`", item.ID, item.FileName, item.RetryCount, item.FailedAt.Format(time.RFC3339), reason))
	}

	sendChannelMessage(s, m.ChannelID, "Dead-letter items for this channel:\n"+strings.Join(lines, "\n"))
}

func handleReplayDeadCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) != 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!replaydead <dead_letter_id>`")
		return
	}

	deadLetterID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || deadLetterID <= 0 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!replaydead <dead_letter_id>`")
		return
	}

	replayed, err := database.ReplayDeadLetter(deadLetterID, m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to replay dead-letter item.")
		log.Printf("[WARN] failed to replay dead letter %d: %v", deadLetterID, err)
		return
	}

	if !replayed {
		sendChannelMessage(s, m.ChannelID, "ℹ️ Dead-letter item not found for this channel.")
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Requeued dead-letter item `%d`.", deadLetterID))
}

func parseUnblockCommand(pairings []database.Pairing, args []string) (string, string) {
	if len(args) == 0 {
		return "", ""
	}

	if len(args) >= 2 && pairingExists(pairings, args[0]) {
		return args[0], strings.TrimSpace(strings.Join(args[1:], " "))
	}

	if len(pairings) == 1 {
		return pairings[0].TGChatID, strings.TrimSpace(strings.Join(args, " "))
	}

	return "", ""
}

func resolvePairingForChannel(pairings []database.Pairing, tgID string) (database.Pairing, string) {
	if len(pairings) == 0 {
		return database.Pairing{}, "ℹ️ This Discord channel is not linked to any Telegram chat."
	}

	if tgID != "" {
		for _, pairing := range pairings {
			if pairing.TGChatID == tgID {
				return pairing, ""
			}
		}
		return database.Pairing{}, fmt.Sprintf("ℹ️ This channel is not linked to Telegram chat `%s`.", tgID)
	}

	if len(pairings) > 1 {
		ids := make([]string, 0, len(pairings))
		for _, pairing := range pairings {
			ids = append(ids, pairing.TGChatID)
		}
		sort.Strings(ids)
		return database.Pairing{}, "ℹ️ Multiple Telegram chats are linked here. Specify one: `" + strings.Join(ids, "`, `") + "`"
	}

	return pairings[0], ""
}

func pairingExists(pairings []database.Pairing, tgID string) bool {
	for _, pairing := range pairings {
		if pairing.TGChatID == tgID {
			return true
		}
	}

	return false
}

func sendChannelMessage(s *discordgo.Session, channelID, message string) {
	if _, err := s.ChannelMessageSend(channelID, message); err != nil {
		log.Printf("[WARN] failed to send Discord command response: %v", err)
	}
}

func StartConsumer() {
	for {
		observability.MarkConsumerHeartbeat()
		now := time.Now()
		flushReadyAlbums(now)

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
	if strings.TrimSpace(event.EventID) == "" {
		observability.Log("warn", "dropping malformed queued event without event id", map[string]interface{}{})
		return
	}

	if strings.TrimSpace(event.TargetDCID) == "" {
		handleDeliveryFailure(queuedEvent, "missing target Discord channel id")
		return
	}

	blockedWords, err := database.GetBlockedWords(event.SourceTGID, event.TargetDCID)
	if errors.Is(err, sql.ErrNoRows) {
		observability.LogEvent("info", "skipping event because link no longer exists", event.EventID, map[string]interface{}{
			"source_tg_id": event.SourceTGID,
			"target_dc_id": event.TargetDCID,
		})
		ackEvent(event.EventID)
		return
	}
	if err != nil {
		handleDeliveryFailure(queuedEvent, fmt.Sprintf("failed to read blocked words: %v", err))
		return
	}

	if containsBlockedWord(event.Caption, blockedWords) {
		observability.RegisterEventFiltered()
		observability.LogEvent("info", "event filtered by keyword rules", event.EventID, map[string]interface{}{
			"source_tg_id": event.SourceTGID,
			"target_dc_id": event.TargetDCID,
			"file_name":    event.FileName,
		})
		ackEvent(event.EventID)
		return
	}

	if isDuplicateMedia(event.TargetDCID, event.Data) {
		observability.LogEvent("info", "duplicate media skipped", event.EventID, map[string]interface{}{
			"source_tg_id": event.SourceTGID,
			"target_dc_id": event.TargetDCID,
			"file_name":    event.FileName,
		})
		ackEvent(event.EventID)
		return
	}

	if event.MediaGroupID != "" {
		bufferAlbumEvent(queuedEvent)
		return
	}

	if err := sendSingle(event, event.TargetDCID); err != nil {
		handleDeliveryFailure(queuedEvent, fmt.Sprintf("discord single send failed: %v", err))
		return
	}

	ackEvent(event.EventID)
	observability.RegisterEventsForwarded(1)
	observability.LogEvent("info", "event forwarded to discord", event.EventID, map[string]interface{}{
		"source_tg_id": event.SourceTGID,
		"target_dc_id": event.TargetDCID,
		"file_name":    event.FileName,
	})
}

func bufferAlbumEvent(queuedEvent database.QueuedEvent) {
	now := time.Now()
	key := albumCacheKey(queuedEvent.Event.TargetDCID, queuedEvent.Event.MediaGroupID)

	albumMutex.Lock()
	defer albumMutex.Unlock()

	batch, exists := albumCache[key]
	if !exists {
		albumCache[key] = &albumBatch{
			items:     []database.QueuedEvent{queuedEvent},
			firstSeen: now,
			lastSeen:  now,
		}
		return
	}

	batch.items = append(batch.items, queuedEvent)
	batch.lastSeen = now
}

func flushReadyAlbums(now time.Time) {
	readyItems := make([][]database.QueuedEvent, 0)

	albumMutex.Lock()
	for key, batch := range albumCache {
		if now.Sub(batch.firstSeen) >= albumCollectWindow || now.Sub(batch.lastSeen) >= albumIdleTimeout {
			readyItems = append(readyItems, batch.items)
			delete(albumCache, key)
		}
	}
	albumMutex.Unlock()

	for _, items := range readyItems {
		processAlbumBatch(items)
	}
}

func processAlbumBatch(items []database.QueuedEvent) {
	if len(items) == 0 {
		return
	}

	events := make([]models.MediaEvent, 0, len(items))
	channelID := items[0].Event.TargetDCID
	for _, item := range items {
		events = append(events, item.Event)
	}

	if err := sendGroupToDiscord(events, channelID); err != nil {
		for _, item := range items {
			handleDeliveryFailure(item, fmt.Sprintf("discord album send failed: %v", err))
		}
		return
	}

	for _, item := range items {
		ackEvent(item.Event.EventID)
		observability.LogEvent("info", "album event forwarded to discord", item.Event.EventID, map[string]interface{}{
			"source_tg_id": item.Event.SourceTGID,
			"target_dc_id": item.Event.TargetDCID,
			"file_name":    item.Event.FileName,
		})
	}

	observability.RegisterEventsForwarded(int64(len(items)))
}

func ackEvent(eventID string) {
	if err := database.AckPendingEvent(eventID); err != nil {
		observability.LogEvent("warn", "failed to ack event", eventID, map[string]interface{}{"error": err.Error()})
	}
}

func handleDeliveryFailure(queuedEvent database.QueuedEvent, reason string) {
	observability.RegisterDeliveryFailure(reason)
	observability.LogEvent("warn", "delivery failure recorded", queuedEvent.Event.EventID, map[string]interface{}{
		"source_tg_id": queuedEvent.Event.SourceTGID,
		"target_dc_id": queuedEvent.Event.TargetDCID,
		"file_name":    queuedEvent.Event.FileName,
		"reason":       reason,
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

		observability.RegisterDeadLetterMoved()
		observability.LogEvent("warn", "event moved to dead-letter queue", queuedEvent.Event.EventID, map[string]interface{}{
			"dead_letter_id": deadLetterID,
			"retry_count":    queuedEvent.RetryCount,
			"reason":         reason,
		})
		return
	}

	delay := computeRetryDelay(nextRetry)
	nextAttemptAt := time.Now().Add(delay)
	if err := database.ReschedulePendingEvent(queuedEvent.Event.EventID, nextRetry, nextAttemptAt, reason); err != nil {
		observability.LogEvent("error", "failed to reschedule event", queuedEvent.Event.EventID, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	observability.RegisterRetryScheduled()
	observability.LogEvent("info", "retry scheduled for event", queuedEvent.Event.EventID, map[string]interface{}{
		"next_retry":  nextRetry,
		"max_retries": maxDeliveryRetries,
		"delay":       delay.String(),
		"reason":      reason,
	})
}

func computeRetryDelay(retryNumber int) time.Duration {
	if retryNumber <= 1 {
		return retryBaseDelay
	}

	maxDelay := time.Duration(maxRetryBackoffSeconds) * time.Second
	delay := retryBaseDelay
	for i := 1; i < retryNumber; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}

	if delay > maxDelay {
		return maxDelay
	}

	return delay
}

func truncateReason(reason string, limit int) string {
	clean := strings.TrimSpace(reason)
	if clean == "" {
		return "-"
	}
	if limit <= 0 || len(clean) <= limit {
		return clean
	}
	if limit <= 3 {
		return clean[:limit]
	}
	return clean[:limit-3] + "..."
}

func isDuplicateMedia(channelID string, data []byte) bool {
	if len(data) == 0 || duplicateWindow <= 0 {
		return false
	}

	hash := sha256.Sum256(data)
	key := channelID + ":" + hex.EncodeToString(hash[:])
	now := time.Now()
	cutoff := now.Add(-duplicateWindow)

	seenMediaMutex.Lock()
	defer seenMediaMutex.Unlock()

	for existingKey, seenAt := range seenMediaHashes {
		if seenAt.Before(cutoff) {
			delete(seenMediaHashes, existingKey)
		}
	}

	if seenAt, exists := seenMediaHashes[key]; exists && now.Sub(seenAt) <= duplicateWindow {
		return true
	}

	seenMediaHashes[key] = now
	return false
}

func albumCacheKey(dcChannelID, mediaGroupID string) string {
	return dcChannelID + ":" + mediaGroupID
}

func sendGroupToDiscord(items []models.MediaEvent, dcChannelID string) error {
	if len(items) == 0 {
		return nil
	}

	var files []*discordgo.File
	var combinedCaption string

	for _, item := range items {
		contentType := item.ContentType
		if strings.TrimSpace(contentType) == "" {
			contentType = http.DetectContentType(item.Data)
		}

		files = append(files, &discordgo.File{
			Name:        item.FileName,
			ContentType: contentType,
			Reader:      bytes.NewReader(item.Data),
		})

		if item.Caption != "" {
			combinedCaption = item.Caption
		}
	}

	_, err := Session.ChannelMessageSendComplex(dcChannelID, &discordgo.MessageSend{
		Content: combinedCaption,
		Files:   files,
	})
	if err != nil {
		return err
	}
	return nil
}

func sendSingle(event models.MediaEvent, dcChannelID string) error {
	contentType := event.ContentType
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(event.Data)
	}

	file := &discordgo.File{
		Name:        event.FileName,
		ContentType: contentType,
		Reader:      bytes.NewReader(event.Data),
	}

	_, err := Session.ChannelMessageSendComplex(dcChannelID, &discordgo.MessageSend{
		Content: event.Caption,
		Files:   []*discordgo.File{file},
	})
	if err != nil {
		return err
	}
	return nil
}

func containsBlockedWord(caption string, blockedWords []string) bool {
	captionLower := strings.ToLower(caption)
	for _, word := range blockedWords {
		normalized := strings.ToLower(strings.TrimSpace(word))
		if normalized == "" {
			continue
		}
		if strings.Contains(captionLower, normalized) {
			return true
		}
	}

	return false
}
