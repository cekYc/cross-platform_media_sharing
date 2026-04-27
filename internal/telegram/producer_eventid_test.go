package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestBuildEventIDDeterministic(t *testing.T) {
	message := &tgbotapi.Message{
		MessageID: 77,
		Chat:      &tgbotapi.Chat{ID: -10012345},
	}

	eventA := buildEventID(message, "file-abc", "dc-target-1")
	eventB := buildEventID(message, "file-abc", "dc-target-1")
	if eventA != eventB {
		t.Fatalf("expected deterministic event id, got %q and %q", eventA, eventB)
	}

	eventOtherTarget := buildEventID(message, "file-abc", "dc-target-2")
	if eventOtherTarget == eventA {
		t.Fatalf("expected different target to produce different event id, got %q", eventOtherTarget)
	}

	eventOtherFile := buildEventID(message, "file-xyz", "dc-target-1")
	if eventOtherFile == eventA {
		t.Fatalf("expected different file to produce different event id, got %q", eventOtherFile)
	}
}
