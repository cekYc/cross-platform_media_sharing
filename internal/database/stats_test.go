package database

import (
	"testing"
	"time"
)

func TestGetChatStats(t *testing.T) {
	setupQueueTestDB(t)

	now := time.Now()
	_ = InsertEventHistory("tg:1001:1:file:dc-1", "delivered", "ok")
	_ = InsertEventHistory("tg:1001:2:file:dc-1", "filtered", "blocked")
	_ = InsertEventHistory("dc:2002:1:file:tg-1", "delivered", "ok")

	stats, err := GetChatStats("telegram", "1001", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("GetChatStats() error = %v", err)
	}
	if stats.Counts["delivered"] != 1 {
		t.Fatalf("expected delivered=1, got %d", stats.Counts["delivered"])
	}
	if stats.Counts["filtered"] != 1 {
		t.Fatalf("expected filtered=1, got %d", stats.Counts["filtered"])
	}
	if stats.LastEventAt.IsZero() {
		t.Fatal("expected last event timestamp to be set")
	}
}
