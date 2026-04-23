package discord

import (
	"testing"
	"tg-discord-bot/internal/database"
)

func TestParseUnblockCommand(t *testing.T) {
	pairings := []database.Pairing{
		{SourceID: "1001"},
		{SourceID: "1002"},
	}

	tgID, word := parseUnblockCommand(pairings, []string{"1002", "ad"})
	if tgID != "1002" || word != "ad" {
		t.Fatalf("parseUnblockCommand explicit mode = (%q, %q)", tgID, word)
	}

	tgID, word = parseUnblockCommand(pairings, []string{"ad"})
	if tgID != "" || word != "" {
		t.Fatalf("parseUnblockCommand ambiguous mode = (%q, %q), want empty values", tgID, word)
	}

	singlePairing := []database.Pairing{{SourceID: "1009"}}
	tgID, word = parseUnblockCommand(singlePairing, []string{"promo", "spam"})
	if tgID != "1009" || word != "promo spam" {
		t.Fatalf("parseUnblockCommand single pairing mode = (%q, %q)", tgID, word)
	}
}
