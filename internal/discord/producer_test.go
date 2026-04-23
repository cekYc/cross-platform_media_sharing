package discord

import (
	"testing"
	"tg-discord-bot/internal/models"
)

func TestGetMediaType(t *testing.T) {
	tests := []struct {
		contentType string
		expected    string
	}{
		{"image/jpeg", models.MediaTypePhoto},
		{"image/png", models.MediaTypePhoto},
		{"image/gif", models.MediaTypeAnimation},
		{"video/mp4", models.MediaTypeVideo},
		{"application/pdf", models.MediaTypeDocument},
		{"text/plain", models.MediaTypeDocument},
	}

	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			result := getMediaType(tc.contentType)
			if result != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}
