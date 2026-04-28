package database

import (
	"errors"
	"strings"

	"tg-discord-bot/internal/models"
)

// PairingExport describes a pairing and its configuration for JSON import/export.
type PairingExport struct {
	SourcePlatform string            `json:"source_platform"`
	SourceID       string            `json:"source_id"`
	TargetPlatform string            `json:"target_platform"`
	TargetID       string            `json:"target_id"`
	RuleConfig     models.RuleConfig `json:"rule_config"`
	BlockedWords   []string          `json:"blocked_words,omitempty"`
	WebhookSecret  string            `json:"webhook_secret,omitempty"`
}

// ExportPairings returns pairings ready for JSON serialization.
func ExportPairings(limit int, includeSecrets bool) ([]PairingExport, error) {
	pairings, err := ListAllPairings(limit)
	if err != nil {
		return nil, err
	}

	items := make([]PairingExport, 0, len(pairings))
	for _, pairing := range pairings {
		item := PairingExport{
			SourcePlatform: pairing.SourcePlatform,
			SourceID:       pairing.SourceID,
			TargetPlatform: pairing.TargetPlatform,
			TargetID:       pairing.TargetID,
			RuleConfig:     pairing.RuleConfig,
			BlockedWords:   pairing.BlockedWords,
		}
		if includeSecrets {
			item.WebhookSecret = pairing.WebhookSecret
		}
		items = append(items, item)
	}

	return items, nil
}

// ImportPairings applies a JSON export to the database.
func ImportPairings(items []PairingExport, includeSecrets, replaceBlocked bool) (int, error) {
	if len(items) == 0 {
		return 0, errors.New("no pairings provided")
	}

	applied := 0
	for _, item := range items {
		item.SourcePlatform = strings.TrimSpace(item.SourcePlatform)
		item.SourceID = strings.TrimSpace(item.SourceID)
		item.TargetPlatform = strings.TrimSpace(item.TargetPlatform)
		item.TargetID = strings.TrimSpace(item.TargetID)

		if item.SourcePlatform == "" || item.SourceID == "" || item.TargetPlatform == "" || item.TargetID == "" {
			return applied, errors.New("pairing fields cannot be empty")
		}

		secret := ""
		if includeSecrets {
			secret = strings.TrimSpace(item.WebhookSecret)
		} else {
			if existing, err := GetPairing(item.SourcePlatform, item.SourceID, item.TargetPlatform, item.TargetID); err == nil {
				secret = existing.WebhookSecret
			}
		}

		if err := LinkChannel(item.SourcePlatform, item.SourceID, item.TargetPlatform, item.TargetID, secret); err != nil {
			return applied, err
		}

		if err := UpdateRuleConfig(item.SourcePlatform, item.SourceID, item.TargetPlatform, item.TargetID, item.RuleConfig); err != nil {
			return applied, err
		}

		if replaceBlocked {
			_ = ClearBlockedWords(item.SourcePlatform, item.SourceID, item.TargetPlatform, item.TargetID)
			for _, word := range item.BlockedWords {
				if err := AddBlockedWord(item.SourcePlatform, item.SourceID, item.TargetPlatform, item.TargetID, word); err != nil {
					return applied, err
				}
			}
		}

		applied++
	}

	return applied, nil
}
