package database

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestNormalizeBlockedWord(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trims and lowercases", input: "  SPAM  ", want: "spam"},
		{name: "empty", input: "", want: ""},
		{name: "whitespace", input: "   ", want: ""},
		{name: "phrase", input: "MiXeD CaSe", want: "mixed case"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeBlockedWord(tc.input)
			if got != tc.want {
				t.Fatalf("normalizeBlockedWord(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSplitBlockedWords(t *testing.T) {
	input := sql.NullString{Valid: true, String: "spam,SPAM,  ads , ,News"}
	want := []string{"spam", "ads", "news"}

	got := splitBlockedWords(input)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitBlockedWords() = %#v, want %#v", got, want)
	}
}

func TestMergeBlockedWords(t *testing.T) {
	existing := sql.NullString{Valid: true, String: "spam,ads"}

	if got := mergeBlockedWords(existing, " SPAM "); got != "spam,ads" {
		t.Fatalf("mergeBlockedWords duplicate handling failed, got %q", got)
	}

	if got := mergeBlockedWords(existing, "news"); got != "spam,ads,news" {
		t.Fatalf("mergeBlockedWords append handling failed, got %q", got)
	}
}

func TestRemoveBlockedWord(t *testing.T) {
	existing := sql.NullString{Valid: true, String: "spam,ads,news"}

	updated, removed := removeBlockedWord(existing, "ads")
	if !removed {
		t.Fatal("expected removeBlockedWord to remove existing value")
	}
	if updated != "spam,news" {
		t.Fatalf("removeBlockedWord returned %q, want %q", updated, "spam,news")
	}

	updated, removed = removeBlockedWord(existing, "missing")
	if removed {
		t.Fatal("expected removeBlockedWord to return removed=false for missing value")
	}
	if updated != "spam,ads,news" {
		t.Fatalf("removeBlockedWord returned %q, want %q", updated, "spam,ads,news")
	}
}
