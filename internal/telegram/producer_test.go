package telegram

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
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

	data, err := downloadFile(srv.URL)
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

	if _, err := downloadFile(srv.URL); err == nil {
		t.Fatal("downloadFile() expected an error, got nil")
	}
}
