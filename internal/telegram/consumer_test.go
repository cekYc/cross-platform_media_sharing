package telegram

import (
	"testing"
)

func TestTelegramConsumer_PlatformID(t *testing.T) {
	c := &Consumer{}
	if c.PlatformID() != "telegram" {
		t.Fatalf("expected platform id 'telegram', got %s", c.PlatformID())
	}
}
