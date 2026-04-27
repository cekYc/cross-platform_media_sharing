package observability

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"tg-discord-bot/internal/database"

	_ "modernc.org/sqlite"
)

func TestHealthHandler(t *testing.T) {
	originalStartedAt := startedAt
	startedAt = time.Now().Add(-3 * time.Second)
	t.Cleanup(func() {
		startedAt = originalStartedAt
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	healthHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response json: %v", err)
	}

	if payload["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", payload["status"])
	}
}

func TestReadyHandler_NotReadyWithoutDatabaseAndConsumer(t *testing.T) {
	originalCfg := cfg
	cfg = config{readyConsumerMaxStaleness: 30 * time.Second}
	t.Cleanup(func() {
		cfg = originalCfg
	})

	oldDB := database.DB
	database.DB = nil
	t.Cleanup(func() {
		database.DB = oldDB
	})

	oldHeartbeat := atomic.LoadInt64(&consumerHeartbeatUnix)
	atomic.StoreInt64(&consumerHeartbeatUnix, 0)
	t.Cleanup(func() {
		atomic.StoreInt64(&consumerHeartbeatUnix, oldHeartbeat)
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()

	readyHandler(recorder, req)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", recorder.Code)
	}
}

func TestReadyHandler_ReadyWithHealthyDatabaseAndConsumer(t *testing.T) {
	originalCfg := cfg
	cfg = config{readyConsumerMaxStaleness: 30 * time.Second}
	t.Cleanup(func() {
		cfg = originalCfg
	})

	oldDB := database.DB
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	database.DB = db
	t.Cleanup(func() {
		_ = db.Close()
		database.DB = oldDB
	})

	oldHeartbeat := atomic.LoadInt64(&consumerHeartbeatUnix)
	atomic.StoreInt64(&consumerHeartbeatUnix, time.Now().Unix())
	t.Cleanup(func() {
		atomic.StoreInt64(&consumerHeartbeatUnix, oldHeartbeat)
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recorder := httptest.NewRecorder()

	readyHandler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var payload struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response json: %v", err)
	}
	if !payload.Ready {
		t.Fatal("expected ready=true")
	}
}
