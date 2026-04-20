package telegram

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	maxMediaBytes      = 20 * 1024 * 1024
	queueEnqueueTimout = 2 * time.Second
	downloadRetries    = 3
	downloadTimeout    = 20 * time.Second
)

var downloadClient = &http.Client{Timeout: downloadTimeout}

func StartProducer(token string, queue chan<- models.MediaEvent) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("failed to initialize Telegram bot: %v", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		chatStringID := fmt.Sprintf("%d", update.Message.Chat.ID)

		if update.Message.IsCommand() {
			cmd := strings.ToLower(update.Message.Command())
			args := strings.TrimSpace(update.Message.CommandArguments())

			switch cmd {
			case "id", "chatid":
				reply := fmt.Sprintf("This chat ID is: `%s`\n\nIn Discord, run:\n`!join %s`", chatStringID, chatStringID)
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, reply)
				msg.ParseMode = "Markdown"
				if _, err := bot.Send(msg); err != nil {
					log.Printf("[WARN] failed to send /id response: %v", err)
				}
			case "block":
				if args == "" {
					if _, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "Usage: /block <word_or_phrase>")); err != nil {
						log.Printf("[WARN] failed to send /block usage: %v", err)
					}
					continue
				}

				if err := database.AddBlockedWord(chatStringID, args); err != nil {
					if _, sendErr := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "❌ This chat is not linked yet. In Discord, run: !join <telegram_chat_id>")); sendErr != nil {
						log.Printf("[WARN] failed to send /block error: %v", sendErr)
					}
				} else {
					if _, sendErr := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "✅ Added to block list: '"+args+"'")); sendErr != nil {
						log.Printf("[WARN] failed to send /block success: %v", sendErr)
					}
				}
			case "help", "start":
				helpText := "Commands:\n" +
					"/id - show this Telegram chat ID\n" +
					"/block <word_or_phrase> - block media captions containing this text"
				if _, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, helpText)); err != nil {
					log.Printf("[WARN] failed to send help response: %v", err)
				}
			default:
				if _, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, "Unknown command. Use /help")); err != nil {
					log.Printf("[WARN] failed to send unknown command response: %v", err)
				}
			}
			continue
		}

		var fileID string
		var fileName string
		var caption = update.Message.Caption
		var mediaGroupID = update.Message.MediaGroupID

		if len(update.Message.Photo) > 0 {
			fileID = update.Message.Photo[len(update.Message.Photo)-1].FileID
			fileName = "image.jpg"
		} else if update.Message.Video != nil {
			fileID = update.Message.Video.FileID
			fileName = update.Message.Video.FileName
			if fileName == "" {
				fileName = "video.mp4"
			}
		} else if update.Message.Animation != nil {
			fileID = update.Message.Animation.FileID
			fileName = update.Message.Animation.FileName
			if fileName == "" {
				fileName = "animation.mp4"
			}
		} else if update.Message.Document != nil {
			fileID = update.Message.Document.FileID
			fileName = update.Message.Document.FileName
			if fileName == "" {
				fileName = "document.bin"
			}
		}

		if fileID != "" {
			fileURL, err := bot.GetFileDirectURL(fileID)
			if err != nil {
				log.Printf("[WARN] failed to resolve Telegram file URL: %v", err)
				continue
			}

			data, err := downloadFile(fileURL)
			if err != nil {
				log.Printf("[WARN] file download failed: %v", err)
				continue
			}

			event := models.MediaEvent{
				Data:         data,
				FileName:     fileName,
				Caption:      caption,
				SourceTGID:   chatStringID,
				MediaGroupID: mediaGroupID,
			}

			select {
			case queue <- event:
			case <-time.After(queueEnqueueTimout):
				log.Printf("[WARN] queue is full, dropping media: %s", fileName)
			}
		}
	}
}

func downloadFile(url string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= downloadRetries; attempt++ {
		data, err := downloadFileOnce(url)
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

func downloadFileOnce(url string) ([]byte, error) {
	resp, err := downloadClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if resp.ContentLength > maxMediaBytes {
		return nil, fmt.Errorf("file too large: %d bytes (limit: %d bytes)", resp.ContentLength, maxMediaBytes)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMediaBytes {
		return nil, errors.New("file exceeds maximum allowed size")
	}

	return data, nil
}
