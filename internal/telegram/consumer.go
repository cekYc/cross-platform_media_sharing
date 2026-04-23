package telegram

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/transport"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Consumer struct{}

func init() {
	transport.RegisterConsumer(&Consumer{})
}

func (c *Consumer) PlatformID() string {
	return "telegram"
}

func (c *Consumer) Send(ctx context.Context, event models.MediaEvent) error {
	if botInstance == nil {
		return fmt.Errorf("telegram bot instance not initialized")
	}

	chatID, err := strconv.ParseInt(event.TargetID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram target id: %v", err)
	}

	if event.FileURL == "" {
		// Just text
		msg := tgbotapi.NewMessage(chatID, event.Caption)
		msg.ParseMode = "Markdown" // Optional: can be disabled
		_, err := botInstance.Send(msg)
		return err
	}

	// It's a media event with a FileURL. We need to stream it.
	req, err := http.NewRequestWithContext(ctx, "GET", event.FileURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for file stream: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to stream file from url: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code when streaming file: %d", resp.StatusCode)
	}

	fileReader := tgbotapi.FileReader{
		Name:   event.FileName,
		Reader: resp.Body,
	}

	switch event.MediaType {
	case models.MediaTypePhoto:
		msg := tgbotapi.NewPhoto(chatID, fileReader)
		msg.Caption = event.Caption
		_, err = botInstance.Send(msg)
	case models.MediaTypeVideo:
		msg := tgbotapi.NewVideo(chatID, fileReader)
		msg.Caption = event.Caption
		_, err = botInstance.Send(msg)
	case models.MediaTypeAnimation:
		msg := tgbotapi.NewAnimation(chatID, fileReader)
		msg.Caption = event.Caption
		_, err = botInstance.Send(msg)
	default: // Document
		msg := tgbotapi.NewDocument(chatID, fileReader)
		msg.Caption = event.Caption
		_, err = botInstance.Send(msg)
	}

	return err
}
