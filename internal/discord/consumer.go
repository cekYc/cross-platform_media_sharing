package discord

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	defaultDuplicateWindowSeconds = 600
	albumCollectDelay             = 2 * time.Second
)

var Session *discordgo.Session

var (
	albumCache = make(map[string][]models.MediaEvent)
	albumMutex sync.Mutex

	seenMediaHashes = make(map[string]time.Time)
	seenMediaMutex  sync.Mutex

	adminRoleIDs    = map[string]struct{}{}
	duplicateWindow = time.Duration(defaultDuplicateWindowSeconds) * time.Second
)

func InitBot(token string) {
	var err error
	Session, err = discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("failed to create Discord bot:", err)
	}

	adminRoleIDs = parseRoleIDs(os.Getenv("DISCORD_ADMIN_ROLE_IDS"))
	duplicateWindow = resolveDuplicateWindow()

	Session.AddHandler(handleCommand)

	err = Session.Open()
	if err != nil {
		log.Fatal("failed to open Discord connection:", err)
	}
	log.Println("[+] Discord bot is connected and listening for commands...")
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
	case "!help":
		handleHelpCommand(s, m.ChannelID)
	}
}

func isManagedCommand(command string) bool {
	switch command {
	case "!join", "!unlink", "!status", "!help", "!blocklist", "!unblock", "!clearblocks":
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

func handleHelpCommand(s *discordgo.Session, channelID string) {
	message := "Commands:\n" +
		"`!join <telegram_chat_id>` - link this Discord channel to a Telegram chat\n" +
		"`!unlink <telegram_chat_id>` - remove one Telegram link from this channel\n" +
		"`!status [telegram_chat_id]` - show link and filter status\n" +
		"`!blocklist [telegram_chat_id]` - show blocked words\n" +
		"`!unblock <word_or_phrase>` - remove a blocked word (single-link channels)\n" +
		"`!unblock <telegram_chat_id> <word_or_phrase>` - remove from a specific link\n" +
		"`!clearblocks [telegram_chat_id]` - clear blocked words for one link\n" +
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

func StartConsumer(queue <-chan models.MediaEvent) {
	for event := range queue {
		pairings, err := database.GetPairingsByTelegramChat(event.SourceTGID)
		if err != nil {
			log.Printf("[WARN] failed to read pairings for Telegram chat %s: %v", event.SourceTGID, err)
			continue
		}

		if len(pairings) == 0 {
			continue
		}

		for _, pairing := range pairings {
			if containsBlockedWord(event.Caption, pairing.BlockedWords) {
				log.Printf("[FILTERED] media blocked by keyword rules for channel %s: %s", pairing.DCChannelID, event.FileName)
				continue
			}

			if isDuplicateMedia(pairing.DCChannelID, event.Data) {
				log.Printf("[DUPLICATE] skipped duplicate media for channel %s: %s", pairing.DCChannelID, event.FileName)
				continue
			}

			if event.MediaGroupID != "" {
				handleAlbum(event, pairing.DCChannelID)
			} else {
				sendSingle(event, pairing.DCChannelID)
			}
		}
	}
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

func handleAlbum(event models.MediaEvent, dcChannelID string) {
	albumMutex.Lock()
	defer albumMutex.Unlock()

	cacheKey := albumCacheKey(dcChannelID, event.MediaGroupID)

	if _, exists := albumCache[cacheKey]; !exists {
		albumCache[cacheKey] = []models.MediaEvent{event}

		go func(key string, channelID string) {
			time.Sleep(albumCollectDelay)

			albumMutex.Lock()
			items := albumCache[key]
			delete(albumCache, key)
			albumMutex.Unlock()

			sendGroupToDiscord(items, channelID)
		}(cacheKey, dcChannelID)

	} else {
		albumCache[cacheKey] = append(albumCache[cacheKey], event)
	}
}

func albumCacheKey(dcChannelID, mediaGroupID string) string {
	return dcChannelID + ":" + mediaGroupID
}

func sendGroupToDiscord(items []models.MediaEvent, dcChannelID string) {
	if len(items) == 0 {
		return
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
		log.Printf("[ERROR] failed to send media group: %v\n", err)
	} else {
		log.Printf("[OK] media group with %d files sent to channel %s\n", len(files), dcChannelID)
	}
}

func sendSingle(event models.MediaEvent, dcChannelID string) {
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
		log.Printf("[ERROR] failed to send media to Discord: %v\n", err)
	} else {
		log.Printf("[OK] %s sent to channel %s\n", event.FileName, dcChannelID)
	}
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
