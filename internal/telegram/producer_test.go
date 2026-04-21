package telegram

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestDownloadFileRetriesAndSucceeds(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	data, err := downloadFile(srv.URL, 1024)
	if err != nil {
		t.Fatalf("downloadFile() returned error: %v", err)
	}

	if string(data) != "ok" {
		t.Fatalf("downloadFile() returned %q, want %q", string(data), "ok")
	}

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("downloadFile() attempts = %d, want 3", got)
	}
}

func TestDownloadFileFailsAfterRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := downloadFile(srv.URL, 1024); err == nil {
		t.Fatal("downloadFile() expected an error, got nil")
	}
}

func TestIsAllowedContentType(t *testing.T) {
	policy := mediaPolicy{
		allowedPrefixes: []string{"image/"},
		allowedExact:    []string{"application/pdf"},
	}

	if !isAllowedContentType(policy, "image/jpeg") {
		t.Fatal("expected image/jpeg to be allowed")
	}

	if !isAllowedContentType(policy, "application/pdf") {
		t.Fatal("expected application/pdf to be allowed")
	}

	if isAllowedContentType(policy, "video/mp4") {
		t.Fatal("expected video/mp4 to be blocked")
	}
}

func TestBuildEventIDDeterministic(t *testing.T) {
	message := &tgbotapi.Message{}
	message.Chat = &tgbotapi.Chat{ID: 99}
	message.MessageID = 321

	a := buildEventID(message, "file-1", "dc-1")
	b := buildEventID(message, "file-1", "dc-1")

	if a != b {
		t.Fatalf("buildEventID should be deterministic, got %q and %q", a, b)
	}

	if a == buildEventID(message, "file-1", "dc-2") {
		t.Fatal("buildEventID should vary by Discord channel id")
	}
}
