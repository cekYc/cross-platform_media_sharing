package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"tg-discord-bot/internal/database"
	"tg-discord-bot/internal/models"
	
	_ "modernc.org/sqlite"
)

func TestWebhookConsumer_Send(t *testing.T) {
	var err error
	database.DB, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	defer database.DB.Close()
	
	_, _ = database.DB.Exec("CREATE TABLE pairings (source_platform TEXT, source_id TEXT, target_platform TEXT, target_id TEXT, blocked_words TEXT, rule_config TEXT, webhook_secret TEXT, PRIMARY KEY(source_platform, source_id, target_platform, target_id))")

	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Header.Get("X-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &Consumer{}
	event := models.MediaEvent{
		TargetID: srv.URL,
		Caption:  "test payload",
	}

	err = c.Send(context.Background(), event)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var receivedEvent models.MediaEvent
	if err := json.Unmarshal(receivedBody, &receivedEvent); err != nil {
		t.Fatalf("failed to parse webhook body: %v", err)
	}

	if receivedEvent.Caption != "test payload" {
		t.Fatalf("expected caption 'test payload', got %s", receivedEvent.Caption)
	}
}

func TestWebhookConsumer_PlatformID(t *testing.T) {
	c := &Consumer{}
	if c.PlatformID() != "webhook" {
		t.Fatalf("expected platform id 'webhook', got %s", c.PlatformID())
	}
}
