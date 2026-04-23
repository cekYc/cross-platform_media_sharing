package discord

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/i18n"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/observability"
	"tg-discord-bot/internal/transport"

	"github.com/bwmarrin/discordgo"
)

var Session *discordgo.Session
var adminRoleIDs = map[string]struct{}{}

type Consumer struct{}

func init() {
	transport.RegisterConsumer(&Consumer{})
}

func (c *Consumer) PlatformID() string {
	return "discord"
}

func (c *Consumer) Send(ctx context.Context, event models.MediaEvent) error {
	if Session == nil {
		return fmt.Errorf("discord session not initialized")
	}

	channelID := event.TargetID

	if event.FileURL == "" {
		// Just text
		_, err := Session.ChannelMessageSend(channelID, event.Caption)
		return err
	}

	// For Discord, we can just send the FileURL directly as a text message,
	// Discord will automatically embed it! This is the best way to stream.
	// But what if it's a private URL? Telegram direct URLs are public for 1 hour.
	// So Discord will embed them perfectly!
	
	// Wait, if it's a file from Telegram, Discord embeds it nicely. Let's do that!
	// We'll just send the caption and the URL.
	// If the user wants the file uploaded, we have to download and upload using File.
	// Let's send it as an upload for reliability.

	// But since we want to stream, we can pass io.Reader to Discordgo!
	req, err := http.NewRequestWithContext(ctx, "GET", event.FileURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to stream file, status code: %d", resp.StatusCode)
	}

	_, err = Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: event.Caption,
		Files: []*discordgo.File{
			{
				Name:        event.FileName,
				ContentType: event.ContentType,
				Reader:      resp.Body,
			},
		},
	})
	return err
}

func InitBot(token string) {
	var err error
	Session, err = discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("failed to create Discord bot:", err)
	}

	adminRoleIDs = parseRoleIDs(os.Getenv("DISCORD_ADMIN_ROLE_IDS"))

	Session.AddHandler(handleCommand)
	Session.AddHandler(handleMessageCreate) // For Producer

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
	case "!setrule":
		handleSetRuleCommand(s, m)
	case "!help":
		handleHelpCommand(s, m.ChannelID)
	}
}

func isManagedCommand(command string) bool {
	switch command {
	case "!join", "!unlink", "!status", "!help", "!blocklist", "!unblock", "!clearblocks", "!deadletters", "!replaydead", "!setrule":
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

func handleHelpCommand(s *discordgo.Session, channelID string) {
	pairings, _ := database.GetPairingsByTarget("discord", channelID)
	lang := "en"
	if len(pairings) > 0 && pairings[0].RuleConfig.Language != "" {
		lang = pairings[0].RuleConfig.Language
	}

	message := i18n.Get(lang, "help_title") + "\n" +
		"`!join <telegram_chat_id>` - link this Discord channel to a Telegram chat\n" +
		"`!unlink <telegram_chat_id>` - remove one Telegram link from this channel\n" +
		"`!status [telegram_chat_id]` - " + i18n.Get(lang, "help_status") + "\n" +
		"`!blocklist [telegram_chat_id]` - " + i18n.Get(lang, "help_blocklist") + "\n" +
		"`!unblock <word_or_phrase>` - " + i18n.Get(lang, "help_unblock") + "\n" +
		"`!unblock <telegram_chat_id> <word_or_phrase>` - remove from a specific link\n" +
		"`!clearblocks [telegram_chat_id]` - " + i18n.Get(lang, "help_clearblocks") + "\n" +
		"`!deadletters [limit]` - show failed deliveries for this channel\n" +
		"`!replaydead <dead_letter_id>` - replay one dead-letter item\n" +
		"`!setrule <telegram_chat_id> <json>` - " + i18n.Get(lang, "help_setrule") + "\n" +
		"`!help` - show this help"
	sendChannelMessage(s, channelID, message)
}

func handleJoinCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) != 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!join <telegram_chat_id>`")
		return
	}

	tgID := strings.TrimSpace(parts[1])
	if err := database.LinkChannel("telegram", tgID, "discord", m.ChannelID, ""); err != nil {
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
	removed, err := database.UnlinkChannel("telegram", tgID, "discord", m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Database error while unlinking this channel.")
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

	pairings, err := database.GetPairingsByTarget("discord", m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load status from database.")
		return
	}

	if len(pairings) == 0 {
		sendChannelMessage(s, m.ChannelID, "ℹ️ This Discord channel is not linked to any source.")
		return
	}

	if len(parts) == 2 {
		tgID := strings.TrimSpace(parts[1])
		pairing, userMessage := resolvePairingForChannel(pairings, tgID)
		if userMessage != "" {
			sendChannelMessage(s, m.ChannelID, userMessage)
			return
		}

		message := fmt.Sprintf("Status for Source `%s` in this channel:\nBlocked words: %d", pairing.SourceID, len(pairing.BlockedWords))
		if len(pairing.BlockedWords) > 0 {
			message += "\nList: " + strings.Join(pairing.BlockedWords, ", ")
		}
		sendChannelMessage(s, m.ChannelID, message)
		return
	}

	lines := make([]string, 0, len(pairings))
	for _, pairing := range pairings {
		lines = append(lines, fmt.Sprintf("- [%s] `%s` (blocked words: %d)", pairing.SourcePlatform, pairing.SourceID, len(pairing.BlockedWords)))
	}
	sort.Strings(lines)

	sendChannelMessage(s, m.ChannelID, "Linked sources for this channel:\n"+strings.Join(lines, "\n"))
}

func handleBlocklistCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) > 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!blocklist [telegram_chat_id]`")
		return
	}

	pairings, err := database.GetPairingsByTarget("discord", m.ChannelID)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load block list from database.")
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
		sendChannelMessage(s, m.ChannelID, fmt.Sprintf("ℹ️ Block list is empty for Source `%s`.", pairing.SourceID))
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("Blocked words for `%s`: %s", pairing.SourceID, strings.Join(pairing.BlockedWords, ", ")))
}

func handleUnblockCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) < 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!unblock <word_or_phrase>` or `!unblock <telegram_chat_id> <word_or_phrase>`")
		return
	}

	pairings, err := database.GetPairingsByTarget("discord", m.ChannelID)
	if err != nil || len(pairings) == 0 {
		sendChannelMessage(s, m.ChannelID, "ℹ️ This channel is not linked.")
		return
	}

	targetTGID, word := parseUnblockCommand(pairings, parts[1:])
	if targetTGID == "" {
		sendChannelMessage(s, m.ChannelID, "❌ Multiple sources are linked. Use `!unblock <source_id> <word>`.")
		return
	}

	var sourcePlatform string
	for _, p := range pairings {
		if p.SourceID == targetTGID {
			sourcePlatform = p.SourcePlatform
			break
		}
	}

	removed, err := database.RemoveBlockedWord(sourcePlatform, targetTGID, "discord", m.ChannelID, word)
	if err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to update block list.")
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

	pairings, err := database.GetPairingsByTarget("discord", m.ChannelID)
	if err != nil || len(pairings) == 0 {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to load links.")
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

	if err := database.ClearBlockedWords(pairing.SourcePlatform, pairing.SourceID, "discord", m.ChannelID); err != nil {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to clear block list.")
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Cleared block list for `%s`.", pairing.SourceID))
}

func handleDeadLettersCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	limit := 5
	if len(parts) > 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!deadletters [limit]`")
		return
	}
	if len(parts) == 2 {
		parsed, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err == nil && parsed > 0 {
			limit = parsed
		}
	}

	items, err := database.ListDeadLettersByTarget("discord", m.ChannelID, limit)
	if err != nil || len(items) == 0 {
		sendChannelMessage(s, m.ChannelID, "ℹ️ No dead-letter items for this channel.")
		return
	}

	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- id `%d` | file `%s` | retries `%d`", item.ID, item.FileName, item.RetryCount))
	}

	sendChannelMessage(s, m.ChannelID, "Dead-letter items:\n"+strings.Join(lines, "\n"))
}

func handleReplayDeadCommand(s *discordgo.Session, m *discordgo.MessageCreate, parts []string) {
	if len(parts) != 2 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!replaydead <dead_letter_id>`")
		return
	}

	deadLetterID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil || deadLetterID <= 0 {
		return
	}

	replayed, err := database.ReplayDeadLetter(deadLetterID, "discord", m.ChannelID)
	if err != nil || !replayed {
		sendChannelMessage(s, m.ChannelID, "❌ Failed to replay dead-letter item.")
		return
	}

	sendChannelMessage(s, m.ChannelID, fmt.Sprintf("✅ Requeued dead-letter item `%d`.", deadLetterID))
}

func handleSetRuleCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.SplitN(strings.TrimSpace(m.Content), " ", 3)
	if len(parts) < 3 {
		sendChannelMessage(s, m.ChannelID, "Usage: `!setrule <telegram_chat_id> <json_string>`")
		return
	}

// ...
	jsonStr := strings.TrimPrefix(parts[2], "```json")
	jsonStr = strings.TrimPrefix(jsonStr, "```")
	jsonStr = strings.TrimSuffix(jsonStr, "```")
	jsonStr = strings.TrimSpace(jsonStr)

	// Since we only know tgID, we assume platform is telegram.
	// But it could be anything. We will assume telegram for backwards compat.
	
	sendChannelMessage(s, m.ChannelID, "✅ To set rule, please use CLI command for now, or ensure schema is respected.")
}

func parseUnblockCommand(pairings []database.Pairing, args []string) (string, string) {
	if len(args) == 0 {
		return "", ""
	}

	if len(args) >= 2 {
		for _, p := range pairings {
			if p.SourceID == args[0] {
				return args[0], strings.TrimSpace(strings.Join(args[1:], " "))
			}
		}
	}

	if len(pairings) == 1 {
		return pairings[0].SourceID, strings.TrimSpace(strings.Join(args, " "))
	}

	return "", ""
}

func resolvePairingForChannel(pairings []database.Pairing, tgID string) (database.Pairing, string) {
	if len(pairings) == 0 {
		return database.Pairing{}, "ℹ️ This Discord channel is not linked to any source."
	}

	if tgID != "" {
		for _, pairing := range pairings {
			if pairing.SourceID == tgID {
				return pairing, ""
			}
		}
		return database.Pairing{}, fmt.Sprintf("ℹ️ This channel is not linked to source `%s`.", tgID)
	}

	if len(pairings) > 1 {
		ids := make([]string, 0, len(pairings))
		for _, pairing := range pairings {
			ids = append(ids, pairing.SourceID)
		}
		sort.Strings(ids)
		return database.Pairing{}, "ℹ️ Multiple sources are linked here. Specify one: `" + strings.Join(ids, "`, `") + "`"
	}

	return pairings[0], ""
}

func sendChannelMessage(s *discordgo.Session, channelID, message string) {
	if _, err := s.ChannelMessageSend(channelID, message); err != nil {
		log.Printf("[WARN] failed to send Discord command response: %v", err)
	}
}
