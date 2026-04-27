package transport

import (
	"context"
	"testing"

	"tg-discord-bot/internal/models"
)

type stubConsumer struct {
	platform string
}

func (s *stubConsumer) PlatformID() string {
	return s.platform
}

func (s *stubConsumer) Send(ctx context.Context, event models.MediaEvent) error {
	return nil
}

func TestRegisterAndGetConsumer(t *testing.T) {
	original := consumers
	consumers = make(map[string]Consumer)
	t.Cleanup(func() {
		consumers = original
	})

	consumer := &stubConsumer{platform: "stub-platform"}
	RegisterConsumer(consumer)

	resolved, err := GetConsumer("stub-platform")
	if err != nil {
		t.Fatalf("expected consumer to be resolved, got error: %v", err)
	}
	if resolved.PlatformID() != "stub-platform" {
		t.Fatalf("expected platform id stub-platform, got %s", resolved.PlatformID())
	}
}

func TestGetConsumerNotFound(t *testing.T) {
	original := consumers
	consumers = make(map[string]Consumer)
	t.Cleanup(func() {
		consumers = original
	})

	if _, err := GetConsumer("missing-platform"); err == nil {
		t.Fatal("expected missing consumer lookup to fail")
	}
}
