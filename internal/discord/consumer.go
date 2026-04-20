package discord

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"sync"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"time"

	"github.com/bwmarrin/discordgo"
)

var Session *discordgo.Session

var (
	albumCache = make(map[string][]models.MediaEvent)
	albumMutex sync.Mutex
)

func InitBot(token string) {
	var err error
	Session, err = discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("failed to create Discord bot:", err)
	}

	Session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil || m.Author.ID == s.State.User.ID {
			return
		}

		parts := strings.Fields(strings.TrimSpace(m.Content))
		if len(parts) == 0 {
			return
		}

		switch strings.ToLower(parts[0]) {
		case "!join":
			if len(parts) != 2 {
				s.ChannelMessageSend(m.ChannelID, "Usage: `!join <telegram_chat_id>`")
				return
			}

			tgID := parts[1]
			_, err := database.DB.Exec(
				"INSERT INTO pairings (tg_chat_id, dc_channel_id) VALUES (?, ?) ON CONFLICT(tg_chat_id) DO UPDATE SET dc_channel_id = excluded.dc_channel_id",
				tgID,
				m.ChannelID,
			)
			if err != nil {
				s.ChannelMessageSend(m.ChannelID, "❌ Database error while linking this channel.")
				log.Println("database error while linking channel:", err)
				return
			}

			s.ChannelMessageSend(m.ChannelID, "✅ Bridge linked successfully. Telegram chat ID: "+tgID)
		case "!help":
			s.ChannelMessageSend(m.ChannelID, "Commands:\n`!join <telegram_chat_id>` - link this Discord channel to a Telegram chat")
		}
	})

	err = Session.Open()
	if err != nil {
		log.Fatal("failed to open Discord connection:", err)
	}
	log.Println("[+] Discord bot is connected and listening for commands...")
}

func StartConsumer(queue <-chan models.MediaEvent) {
	for event := range queue {
		dcChannelID, err := database.GetDiscordChannel(event.SourceTGID)
		if err != nil || dcChannelID == "" {
			continue
		}

		blockedWords, err := database.GetBlockedWords(event.SourceTGID)
		if err != nil {
			log.Printf("[WARN] failed to read blocked words: %v", err)
		}

		if containsBlockedWord(event.Caption, blockedWords) {
			log.Printf("[FILTERED] media blocked by keyword rules: %s", event.FileName)
			continue
		}

		if event.MediaGroupID != "" {
			handleAlbum(event, dcChannelID)
		} else {
			sendSingle(event, dcChannelID)
		}
	}
}

func handleAlbum(event models.MediaEvent, dcChannelID string) {
	albumMutex.Lock()
	defer albumMutex.Unlock()

	if _, exists := albumCache[event.MediaGroupID]; !exists {
		albumCache[event.MediaGroupID] = []models.MediaEvent{event}

		go func(groupID string, channelID string) {
			time.Sleep(2 * time.Second)

			albumMutex.Lock()
			items := albumCache[groupID]
			delete(albumCache, groupID)
			albumMutex.Unlock()

			sendGroupToDiscord(items, channelID)
		}(event.MediaGroupID, dcChannelID)

	} else {
		albumCache[event.MediaGroupID] = append(albumCache[event.MediaGroupID], event)
	}
}

func sendGroupToDiscord(items []models.MediaEvent, dcChannelID string) {
	if len(items) == 0 {
		return
	}

	var files []*discordgo.File
	var combinedCaption string

	for _, item := range items {
		contentType := http.DetectContentType(item.Data)
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
	contentType := http.DetectContentType(event.Data)
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
