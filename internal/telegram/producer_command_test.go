package telegram

import (
	"testing"

	"tg-discord-bot/internal/database"
)

func TestResolveOptionalTargetArg(t *testing.T) {
	pairings := []database.Pairing{
		{TargetID: "dc-1"},
		{TargetID: "dc-2"},
	}

	targetID, word := resolveOptionalTargetArg("dc-2 urgent notice", pairings)
	if targetID != "dc-2" || word != "urgent notice" {
		t.Fatalf("expected explicit target parse, got targetID=%q word=%q", targetID, word)
	}

	targetID, word = resolveOptionalTargetArg("global phrase", pairings)
	if targetID != "" || word != "global phrase" {
		t.Fatalf("expected global phrase parse, got targetID=%q word=%q", targetID, word)
	}

	targetID, word = resolveOptionalTargetArg("   ", pairings)
	if targetID != "" || word != "" {
		t.Fatalf("expected empty parse for blank args, got targetID=%q word=%q", targetID, word)
	}
}
