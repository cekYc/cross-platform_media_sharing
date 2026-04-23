package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	"tg-discord-bot/internal/transport"
)

type Consumer struct{}

func init() {
	transport.RegisterConsumer(&Consumer{})
}

func (c *Consumer) PlatformID() string {
	return "webhook"
}

func (c *Consumer) Send(ctx context.Context, event models.MediaEvent) error {
	// The target ID is the Webhook URL
	url := event.TargetID

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Get pairing to fetch the webhook secret for HMAC
	pairing, err := database.GetPairing(event.SourcePlatform, event.SourceID, event.TargetPlatform, event.TargetID)
	if err == nil && pairing.WebhookSecret != "" {
		h := hmac.New(sha256.New, []byte(pairing.WebhookSecret))
		h.Write(payload)
		signature := hex.EncodeToString(h.Sum(nil))
		req.Header.Set("X-Signature", signature)
	}

	client := &http.Client{
		Timeout: 10 * time.Second, // Fallback if context has no deadline
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned non-success status code: %d", resp.StatusCode)
	}

	return nil
}
