package discord

import "testing"

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
