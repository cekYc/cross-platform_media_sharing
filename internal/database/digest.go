package database

import (
	"errors"
	"strings"
	"time"

	"tg-discord-bot/internal/models"
)

// DigestEvent stores a buffered item that will be summarized in a digest message.
type DigestEvent struct {
	ID             int64
	SourcePlatform string
	SourceID       string
	TargetPlatform string
	TargetID       string
	EventID        string
	Caption        string
	FileName       string
	MediaType      string
	CreatedAt      time.Time
	DeliverAfter   time.Time
}

// DigestGroup represents a target group with digest entries due to send.
type DigestGroup struct {
	SourcePlatform string
	SourceID       string
	TargetPlatform string
	TargetID       string
	DeliverAfter   time.Time
	Count          int
}

// AddDigestEvent buffers an event for digest delivery.
func AddDigestEvent(event models.MediaEvent, deliverAfter time.Time) error {
	if strings.TrimSpace(event.EventID) == "" {
		return errors.New("event id is required")
	}
	if strings.TrimSpace(event.SourcePlatform) == "" || strings.TrimSpace(event.SourceID) == "" {
		return errors.New("source platform/id is required")
	}
	if strings.TrimSpace(event.TargetPlatform) == "" || strings.TrimSpace(event.TargetID) == "" {
		return errors.New("target platform/id is required")
	}

	_, err := DB.Exec(
		`INSERT INTO digest_events (
			source_platform,
			source_id,
			target_platform,
			target_id,
			event_id,
			caption,
			file_name,
			media_type,
			created_at,
			deliver_after
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(event_id) DO NOTHING`,
		strings.TrimSpace(event.SourcePlatform),
		strings.TrimSpace(event.SourceID),
		strings.TrimSpace(event.TargetPlatform),
		strings.TrimSpace(event.TargetID),
		strings.TrimSpace(event.EventID),
		strings.TrimSpace(event.Caption),
		strings.TrimSpace(event.FileName),
		strings.TrimSpace(event.MediaType),
		time.Now().Unix(),
		deliverAfter.Unix(),
	)
	return err
}

// ListDigestGroupsDue returns target groups with digest events due to send.
func ListDigestGroupsDue(now time.Time, limit int) ([]DigestGroup, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := DB.Query(
		`SELECT source_platform, source_id, target_platform, target_id, MIN(deliver_after), COUNT(*)
		 FROM digest_events
		 WHERE deliver_after <= ?
		 GROUP BY source_platform, source_id, target_platform, target_id
		 ORDER BY MIN(deliver_after) ASC
		 LIMIT ?`,
		now.Unix(),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groups := make([]DigestGroup, 0)
	for rows.Next() {
		var group DigestGroup
		var deliverAfter int64
		if err := rows.Scan(
			&group.SourcePlatform,
			&group.SourceID,
			&group.TargetPlatform,
			&group.TargetID,
			&deliverAfter,
			&group.Count,
		); err != nil {
			return nil, err
		}
		group.DeliverAfter = time.Unix(deliverAfter, 0)
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return groups, nil
}

// ListDigestEventsForGroup returns digest items and the total due count.
func ListDigestEventsForGroup(sourcePlatform, sourceID, targetPlatform, targetID string, cutoff time.Time, limit int) ([]DigestEvent, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	var total int
	err := DB.QueryRow(
		`SELECT COUNT(*) FROM digest_events
		 WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ? AND deliver_after <= ?`,
		sourcePlatform, sourceID, targetPlatform, targetID, cutoff.Unix(),
	).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := DB.Query(
		`SELECT id, event_id, caption, file_name, media_type, created_at, deliver_after
		 FROM digest_events
		 WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ? AND deliver_after <= ?
		 ORDER BY created_at ASC
		 LIMIT ?`,
		sourcePlatform, sourceID, targetPlatform, targetID, cutoff.Unix(), limit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	events := make([]DigestEvent, 0)
	for rows.Next() {
		var item DigestEvent
		var createdAt int64
		var deliverAfter int64
		if err := rows.Scan(
			&item.ID,
			&item.EventID,
			&item.Caption,
			&item.FileName,
			&item.MediaType,
			&createdAt,
			&deliverAfter,
		); err != nil {
			return nil, 0, err
		}
		item.SourcePlatform = sourcePlatform
		item.SourceID = sourceID
		item.TargetPlatform = targetPlatform
		item.TargetID = targetID
		item.CreatedAt = time.Unix(createdAt, 0)
		item.DeliverAfter = time.Unix(deliverAfter, 0)
		events = append(events, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return events, total, nil
}

// DeleteDigestEventsForGroup removes due digest entries for a target group.
func DeleteDigestEventsForGroup(sourcePlatform, sourceID, targetPlatform, targetID string, cutoff time.Time) error {
	_, err := DB.Exec(
		`DELETE FROM digest_events
		 WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ? AND deliver_after <= ?`,
		sourcePlatform, sourceID, targetPlatform, targetID, cutoff.Unix(),
	)
	return err
}
