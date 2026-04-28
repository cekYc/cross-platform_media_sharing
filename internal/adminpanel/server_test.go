package adminpanel

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tg-discord-bot/internal/database"

	_ "modernc.org/sqlite"
)

func setupAdminPanelTestDB(t *testing.T) {
	t.Helper()

	oldDB := database.DB
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	database.DB = db

	if err := database.RunMigrations(); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	if err := database.LinkChannel("telegram", "tg-1", "discord", "dc-1", "secret-should-not-leak"); err != nil {
		t.Fatalf("LinkChannel() error = %v", err)
	}
	if err := database.AddBlockedWord("telegram", "tg-1", "discord", "dc-1", "spam"); err != nil {
		t.Fatalf("AddBlockedWord() error = %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
		database.DB = oldDB
	})
}

func TestSummaryRequiresToken(t *testing.T) {
	setupAdminPanelTestDB(t)

	mux := newMux(panelConfig{token: "panel-token", defaultLimit: 100})
	request := httptest.NewRequest(http.MethodGet, "/admin/api/summary", nil)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestSummaryAndPairingsWithToken(t *testing.T) {
	setupAdminPanelTestDB(t)

	mux := newMux(panelConfig{token: "panel-token", defaultLimit: 100})

	summaryReq := httptest.NewRequest(http.MethodGet, "/admin/api/summary", nil)
	summaryReq.Header.Set("Authorization", "Bearer panel-token")
	summaryRes := httptest.NewRecorder()
	mux.ServeHTTP(summaryRes, summaryReq)

	if summaryRes.Code != http.StatusOK {
		t.Fatalf("expected summary 200, got %d", summaryRes.Code)
	}

	var summaryPayload map[string]interface{}
	if err := json.Unmarshal(summaryRes.Body.Bytes(), &summaryPayload); err != nil {
		t.Fatalf("failed to parse summary payload: %v", err)
	}

	if summaryPayload["pairings"].(float64) < 1 {
		t.Fatalf("expected pairings >= 1, got %v", summaryPayload["pairings"])
	}

	pairingsReq := httptest.NewRequest(http.MethodGet, "/admin/api/pairings?limit=10", nil)
	pairingsReq.Header.Set("X-Admin-Token", "panel-token")
	pairingsRes := httptest.NewRecorder()
	mux.ServeHTTP(pairingsRes, pairingsReq)

	if pairingsRes.Code != http.StatusOK {
		t.Fatalf("expected pairings 200, got %d", pairingsRes.Code)
	}

	var pairingsPayload struct {
		Count int `json:"count"`
		Items []struct {
			SourceID         string `json:"source_id"`
			TargetID         string `json:"target_id"`
			BlockedWordCount int    `json:"blocked_word_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pairingsRes.Body.Bytes(), &pairingsPayload); err != nil {
		t.Fatalf("failed to parse pairings payload: %v", err)
	}

	if pairingsPayload.Count != 1 {
		t.Fatalf("expected count=1, got %d", pairingsPayload.Count)
	}
	if pairingsPayload.Items[0].SourceID != "tg-1" || pairingsPayload.Items[0].TargetID != "dc-1" {
		t.Fatalf("unexpected pairing payload: %+v", pairingsPayload.Items[0])
	}
	if pairingsPayload.Items[0].BlockedWordCount != 1 {
		t.Fatalf("expected blocked_word_count=1, got %d", pairingsPayload.Items[0].BlockedWordCount)
	}
}

func TestChatStatsEndpoint(t *testing.T) {
	setupAdminPanelTestDB(t)

	if err := database.InsertEventHistory("tg:tg-1:1:file:dc-1", "delivered", "ok"); err != nil {
		t.Fatalf("InsertEventHistory() error = %v", err)
	}

	mux := newMux(panelConfig{token: "panel-token", defaultLimit: 100})
	statsReq := httptest.NewRequest(http.MethodGet, "/admin/api/chat-stats?source_platform=telegram&source_id=tg-1", nil)
	statsReq.Header.Set("Authorization", "Bearer panel-token")
	statsRes := httptest.NewRecorder()
	mux.ServeHTTP(statsRes, statsReq)

	if statsRes.Code != http.StatusOK {
		t.Fatalf("expected chat stats 200, got %d", statsRes.Code)
	}

	var statsPayload struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(statsRes.Body.Bytes(), &statsPayload); err != nil {
		t.Fatalf("failed to parse stats payload: %v", err)
	}

	if statsPayload.Counts["delivered"] != 1 {
		t.Fatalf("expected delivered=1, got %d", statsPayload.Counts["delivered"])
	}
}
