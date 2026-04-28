package database

import (
	"errors"
	"strings"
	"time"
)

// ChatStats aggregates event history counts for a single source chat/channel.
type ChatStats struct {
	SourcePlatform string
	SourceID       string
	Since          time.Time
	Counts         map[string]int
	LastEventAt    time.Time
}

// GetChatStats returns per-source counts for recent event history entries.
func GetChatStats(sourcePlatform, sourceID string, since time.Time) (ChatStats, error) {
	sourcePlatform = strings.TrimSpace(sourcePlatform)
	sourceID = strings.TrimSpace(sourceID)
	if sourcePlatform == "" || sourceID == "" {
		return ChatStats{}, errors.New("source platform and id are required")
	}

	rows, err := DB.Query(
		"SELECT event_id, status, created_at FROM event_history WHERE created_at >= ?",
		since.Unix(),
	)
	if err != nil {
		return ChatStats{}, err
	}
	defer rows.Close()

	stats := ChatStats{
		SourcePlatform: sourcePlatform,
		SourceID:       sourceID,
		Since:          since,
		Counts:         map[string]int{},
	}

	for rows.Next() {
		var eventID string
		var status string
		var createdAt int64
		if err := rows.Scan(&eventID, &status, &createdAt); err != nil {
			return ChatStats{}, err
		}

		platform, id, ok := parseSourceFromEventID(eventID)
		if !ok {
			continue
		}
		if platform != sourcePlatform || id != sourceID {
			continue
		}

		cleanStatus := strings.TrimSpace(status)
		if cleanStatus == "" {
			cleanStatus = "unknown"
		}
		stats.Counts[cleanStatus]++

		createdTime := time.Unix(createdAt, 0)
		if stats.LastEventAt.IsZero() || createdTime.After(stats.LastEventAt) {
			stats.LastEventAt = createdTime
		}
	}
	if err := rows.Err(); err != nil {
		return ChatStats{}, err
	}

	return stats, nil
}

func parseSourceFromEventID(eventID string) (string, string, bool) {
	parts := strings.Split(eventID, ":")
	if len(parts) < 2 {
		return "", "", false
	}

	switch parts[0] {
	case "tg":
		return "telegram", parts[1], true
	case "dc":
		return "discord", parts[1], true
	case "digest":
		if len(parts) >= 3 {
			return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), true
		}
	}

	return "", "", false
}
