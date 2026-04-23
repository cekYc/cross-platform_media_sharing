package queue

import (
	"testing"
	"tg-discord-bot/internal/models"
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

func TestApplyFormatting(t *testing.T) {
	event := models.MediaEvent{
		Caption:    "original caption",
		SenderName: "Alice",
		SourceID:   "123",
	}

	ruleConfig := models.RuleConfig{
		CaptionTemplate: "Message from {sender}: {caption}",
	}

	result := applyFormatting(event, ruleConfig)
	expected := "Message from Alice: original caption"
	if result.Caption != expected {
		t.Fatalf("expected caption %q, got %q", expected, result.Caption)
	}
}

func TestApplyFormattingWithReply(t *testing.T) {
	event := models.MediaEvent{
		Caption:        "this is my reply",
		ReplyToSender:  "Bob",
		ReplyToCaption: "This is the original long message from Bob that I am replying to.",
	}

	result := applyFormatting(event, models.RuleConfig{})
	expectedPrefix := "> **In reply to Bob:**\n> This is the original long message from Bob that I am replying to.\n\nthis is my reply"
	if result.Caption != expectedPrefix {
		t.Fatalf("expected caption %q, got %q", expectedPrefix, result.Caption)
	}
}

func TestConfigUpdate(t *testing.T) {
	SetConfig(100*time.Millisecond, 10*time.Second, 10, 5*time.Second)
	if queuePollInterval != 100*time.Millisecond {
		t.Fatalf("expected queuePollInterval 100ms")
	}
	if processingLease != 10*time.Second {
		t.Fatalf("expected processingLease 10s")
	}
	if maxDeliveryRetries != 10 {
		t.Fatalf("expected maxDeliveryRetries 10")
	}
	if retryBaseDelay != 5*time.Second {
		t.Fatalf("expected retryBaseDelay 5s")
	}
}
