package discord

import (
	"testing"
	"tg-discord-bot/internal/database"
	"time"
)

func TestContainsBlockedWord(t *testing.T) {
	tests := []struct {
		name        string
		caption     string
		blockedList []string
		want        bool
	}{
		{
			name:        "matches case-insensitive",
			caption:     "This contains SPAM content",
			blockedList: []string{"spam"},
			want:        true,
		},
		{
			name:        "ignores empty blocked entries",
			caption:     "regular message",
			blockedList: []string{"", "   "},
			want:        false,
		},
		{
			name:        "matches trimmed word",
			caption:     "contains advertising",
			blockedList: []string{"  advertising   "},
			want:        true,
		},
		{
			name:        "no match",
			caption:     "all clear",
			blockedList: []string{"forbidden"},
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := containsBlockedWord(tc.caption, tc.blockedList)
			if got != tc.want {
				t.Fatalf("containsBlockedWord(%q, %#v) = %v, want %v", tc.caption, tc.blockedList, got, tc.want)
			}
		})
	}
}

func TestParseUnblockCommand(t *testing.T) {
	pairings := []database.Pairing{
		{TGChatID: "1001"},
		{TGChatID: "1002"},
	}

	tgID, word := parseUnblockCommand(pairings, []string{"1002", "ad"})
	if tgID != "1002" || word != "ad" {
		t.Fatalf("parseUnblockCommand explicit mode = (%q, %q)", tgID, word)
	}

	tgID, word = parseUnblockCommand(pairings, []string{"ad"})
	if tgID != "" || word != "" {
		t.Fatalf("parseUnblockCommand ambiguous mode = (%q, %q), want empty values", tgID, word)
	}

	singlePairing := []database.Pairing{{TGChatID: "1009"}}
	tgID, word = parseUnblockCommand(singlePairing, []string{"promo", "spam"})
	if tgID != "1009" || word != "promo spam" {
		t.Fatalf("parseUnblockCommand single pairing mode = (%q, %q)", tgID, word)
	}
}

func TestComputeRetryDelay(t *testing.T) {
	originalBase := retryBaseDelay
	t.Cleanup(func() {
		retryBaseDelay = originalBase
	})

	retryBaseDelay = time.Second

	if delay := computeRetryDelay(1); delay != time.Second {
		t.Fatalf("computeRetryDelay(1) = %s, want %s", delay, time.Second)
	}

	if delay := computeRetryDelay(3); delay != 4*time.Second {
		t.Fatalf("computeRetryDelay(3) = %s, want %s", delay, 4*time.Second)
	}

	if delay := computeRetryDelay(20); delay != time.Duration(maxRetryBackoffSeconds)*time.Second {
		t.Fatalf("computeRetryDelay(20) = %s, want %s", delay, time.Duration(maxRetryBackoffSeconds)*time.Second)
	}
}
