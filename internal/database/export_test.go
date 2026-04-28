package database

import (
	"testing"

	"tg-discord-bot/internal/models"
)

func TestExportImportPairings(t *testing.T) {
	setupQueueTestDB(t)

	config := models.RuleConfig{Language: "en", RequiredTags: []string{"news"}}
	if err := LinkChannel("telegram", "tg-1", "discord", "dc-1", ""); err != nil {
		t.Fatalf("LinkChannel() error = %v", err)
	}
	if err := UpdateRuleConfig("telegram", "tg-1", "discord", "dc-1", config); err != nil {
		t.Fatalf("UpdateRuleConfig() error = %v", err)
	}
	if err := AddBlockedWord("telegram", "tg-1", "discord", "dc-1", "spam"); err != nil {
		t.Fatalf("AddBlockedWord() error = %v", err)
	}

	exports, err := ExportPairings(10, false)
	if err != nil {
		t.Fatalf("ExportPairings() error = %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("expected 1 export, got %d", len(exports))
	}

	if _, err := UnlinkChannel("telegram", "tg-1", "discord", "dc-1"); err != nil {
		t.Fatalf("UnlinkChannel() error = %v", err)
	}

	count, err := ImportPairings(exports, false, true)
	if err != nil {
		t.Fatalf("ImportPairings() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 import, got %d", count)
	}

	pairing, err := GetPairing("telegram", "tg-1", "discord", "dc-1")
	if err != nil {
		t.Fatalf("GetPairing() error = %v", err)
	}
	if pairing.RuleConfig.Language != "en" {
		t.Fatalf("expected language=en, got %q", pairing.RuleConfig.Language)
	}
	if len(pairing.RuleConfig.RequiredTags) != 1 {
		t.Fatalf("expected required tags to persist")
	}

	words, err := GetBlockedWords("telegram", "tg-1", "discord", "dc-1")
	if err != nil {
		t.Fatalf("GetBlockedWords() error = %v", err)
	}
	if len(words) != 1 || words[0] != "spam" {
		t.Fatalf("expected blocked words to persist, got %v", words)
	}
}
