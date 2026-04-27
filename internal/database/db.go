package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"tg-discord-bot/internal/models"

	_ "modernc.org/sqlite"
)

type Pairing struct {
	SourcePlatform string
	SourceID       string
	TargetPlatform string
	TargetID       string
	BlockedWords   []string
	RuleConfig     models.RuleConfig
	WebhookSecret  string
}

type QueuedEvent struct {
	Event      models.MediaEvent
	RetryCount int
}

type DeadLetter struct {
	ID             int64
	EventID        string
	SourcePlatform string
	SourceID       string
	TargetPlatform string
	TargetID       string
	FileName       string
	RetryCount     int
	FailureReason  string
	FailedAt       time.Time
}

// AuditEntry records an administrative change for the audit trail.
type AuditEntry struct {
	ID            int64
	Action        string
	ActorPlatform string
	ActorID       string
	Details       string
	CreatedAt     time.Time
}

var DB *sql.DB

func InitDB() {
	var err error
	// Enable WAL mode to allow concurrent reads and writes, preventing "database is locked" errors
	DB, err = sql.Open("sqlite", "bot.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatal(err)
	}

	if err := RunMigrations(); err != nil {
		log.Fatal("database migration failed:", err)
	}
}

// splitBlockedWordsLegacy is used strictly by the v2 migration to parse old data.
func splitBlockedWordsLegacy(words string) []string {
	if strings.TrimSpace(words) == "" {
		return []string{}
	}

	parts := strings.Split(words, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		normalized := normalizeBlockedWord(part)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}

		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	return result
}

func tableExists(tableName string) (bool, error) {
	var name string
	err := DB.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", tableName).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return true, nil
}

func LinkChannel(sourcePlatform, sourceID, targetPlatform, targetID, webhookSecret string) error {
	sourcePlatform = strings.TrimSpace(sourcePlatform)
	sourceID = strings.TrimSpace(sourceID)
	targetPlatform = strings.TrimSpace(targetPlatform)
	targetID = strings.TrimSpace(targetID)

	if sourcePlatform == "" || sourceID == "" || targetPlatform == "" || targetID == "" {
		return errors.New("all pairing ids and platforms are required")
	}

	_, err := DB.Exec(
		"INSERT INTO pairings (source_platform, source_id, target_platform, target_id, webhook_secret) VALUES (?, ?, ?, ?, ?) ON CONFLICT(source_platform, source_id, target_platform, target_id) DO UPDATE SET webhook_secret = excluded.webhook_secret",
		sourcePlatform,
		sourceID,
		targetPlatform,
		targetID,
		webhookSecret,
	)
	return err
}

func UnlinkChannel(sourcePlatform, sourceID, targetPlatform, targetID string) (bool, error) {
	result, err := DB.Exec("DELETE FROM pairings WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ?", sourcePlatform, sourceID, targetPlatform, targetID)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rowsAffected > 0, nil
}

func GetPairingsBySource(sourcePlatform, sourceID string) ([]Pairing, error) {
	rows, err := DB.Query(
		"SELECT source_platform, source_id, target_platform, target_id, rule_config, webhook_secret FROM pairings WHERE source_platform = ? AND source_id = ? ORDER BY target_id",
		sourcePlatform, sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPairings(rows)
}

func GetPairingsByTarget(targetPlatform, targetID string) ([]Pairing, error) {
	rows, err := DB.Query(
		"SELECT source_platform, source_id, target_platform, target_id, rule_config, webhook_secret FROM pairings WHERE target_platform = ? AND target_id = ? ORDER BY source_id",
		targetPlatform, targetID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPairings(rows)
}

func GetPairing(sourcePlatform, sourceID, targetPlatform, targetID string) (Pairing, error) {
	rows, err := DB.Query(
		"SELECT source_platform, source_id, target_platform, target_id, rule_config, webhook_secret FROM pairings WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ?",
		sourcePlatform, sourceID, targetPlatform, targetID,
	)
	if err != nil {
		return Pairing{}, err
	}
	defer rows.Close()

	pairings, err := scanPairings(rows)
	if err != nil {
		return Pairing{}, err
	}

	if len(pairings) == 0 {
		return Pairing{}, sql.ErrNoRows
	}

	return pairings[0], nil
}

func scanPairings(rows *sql.Rows) ([]Pairing, error) {
	pairings := make([]Pairing, 0)

	for rows.Next() {
		var sp, sid, tp, tid string
		var ruleConfigStr sql.NullString
		var webhookSecret sql.NullString

		if err := rows.Scan(&sp, &sid, &tp, &tid, &ruleConfigStr, &webhookSecret); err != nil {
			return nil, err
		}

		var ruleConfig models.RuleConfig
		if ruleConfigStr.Valid && ruleConfigStr.String != "" {
			_ = json.Unmarshal([]byte(ruleConfigStr.String), &ruleConfig)
		}

		pairings = append(pairings, Pairing{
			SourcePlatform: sp,
			SourceID:       sid,
			TargetPlatform: tp,
			TargetID:       tid,
			RuleConfig:     ruleConfig,
			WebhookSecret:  webhookSecret.String,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch blocked words for each pairing efficiently
	for i := range pairings {
		words, err := GetBlockedWords(pairings[i].SourcePlatform, pairings[i].SourceID, pairings[i].TargetPlatform, pairings[i].TargetID)
		if err != nil {
			return nil, err
		}
		pairings[i].BlockedWords = words
	}

	return pairings, nil
}

func UpdateRuleConfig(sourcePlatform, sourceID, targetPlatform, targetID string, config models.RuleConfig) error {
	configBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}

	_, err = DB.Exec(
		"UPDATE pairings SET rule_config = ? WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ?",
		string(configBytes),
		sourcePlatform, sourceID, targetPlatform, targetID,
	)
	return err
}

func GetBlockedWords(sourcePlatform, sourceID, targetPlatform, targetID string) ([]string, error) {
	rows, err := DB.Query(
		"SELECT word FROM blocked_words WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ? ORDER BY word ASC",
		sourcePlatform, sourceID, targetPlatform, targetID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return words, nil
}

func AddBlockedWord(sourcePlatform, sourceID, targetPlatform, targetID, word string) error {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return errors.New("blocked word cannot be empty")
	}

	_, err := DB.Exec(
		"INSERT OR IGNORE INTO blocked_words (source_platform, source_id, target_platform, target_id, word) VALUES (?, ?, ?, ?, ?)",
		sourcePlatform, sourceID, targetPlatform, targetID, normalizedWord,
	)
	return err
}

func AddBlockedWordForAllTargets(sourcePlatform, sourceID, word string) (int, error) {
	pairings, err := GetPairingsBySource(sourcePlatform, sourceID)
	if err != nil {
		return 0, err
	}

	if len(pairings) == 0 {
		return 0, sql.ErrNoRows
	}

	updated := 0
	for _, pairing := range pairings {
		if err := AddBlockedWord(sourcePlatform, sourceID, pairing.TargetPlatform, pairing.TargetID, word); err != nil {
			return updated, err
		}
		updated++
	}

	return updated, nil
}

func RemoveBlockedWord(sourcePlatform, sourceID, targetPlatform, targetID, word string) (bool, error) {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return false, errors.New("blocked word cannot be empty")
	}

	result, err := DB.Exec(
		"DELETE FROM blocked_words WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ? AND word = ?",
		sourcePlatform, sourceID, targetPlatform, targetID, normalizedWord,
	)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rowsAffected > 0, nil
}

func RemoveBlockedWordFromAllTargets(sourcePlatform, sourceID, word string) (int, error) {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return 0, errors.New("blocked word cannot be empty")
	}

	result, err := DB.Exec(
		"DELETE FROM blocked_words WHERE source_platform = ? AND source_id = ? AND word = ?",
		sourcePlatform, sourceID, normalizedWord,
	)
	if err != nil {
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(rowsAffected), nil
}

func ClearBlockedWords(sourcePlatform, sourceID, targetPlatform, targetID string) error {
	_, err := DB.Exec(
		"DELETE FROM blocked_words WHERE source_platform = ? AND source_id = ? AND target_platform = ? AND target_id = ?",
		sourcePlatform, sourceID, targetPlatform, targetID,
	)
	return err
}

func ClearBlockedWordsForAllTargets(sourcePlatform, sourceID string) (int, error) {
	result, err := DB.Exec("DELETE FROM blocked_words WHERE source_platform = ? AND source_id = ?", sourcePlatform, sourceID)
	if err != nil {
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if rowsAffected == 0 {
		return 0, sql.ErrNoRows
	}

	return int(rowsAffected), nil
}

func normalizeBlockedWord(word string) string {
	return strings.ToLower(strings.TrimSpace(word))
}

func EnqueuePendingEvent(event models.MediaEvent) (bool, error) {
	eventID := strings.TrimSpace(event.EventID)
	if eventID == "" {
		return false, errors.New("event id is required")
	}

	event.EventID = eventID
	event.SourcePlatform = strings.TrimSpace(event.SourcePlatform)
	event.SourceID = strings.TrimSpace(event.SourceID)
	event.TargetPlatform = strings.TrimSpace(event.TargetPlatform)
	event.TargetID = strings.TrimSpace(event.TargetID)

	if event.SourcePlatform == "" || event.SourceID == "" || event.TargetPlatform == "" || event.TargetID == "" {
		return false, errors.New("source and target platform/ids are required")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return false, err
	}

	now := time.Now().Unix()
	availableAt := now
	if event.AvailableAt > 0 {
		availableAt = event.AvailableAt
	}

	tx, err := DB.Begin()
	if err != nil {
		return false, err
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	var processedEventID string
	err = tx.QueryRow("SELECT event_id FROM processed_events WHERE event_id = ?", eventID).Scan(&processedEventID)
	if err == nil {
		rollback()
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		rollback()
		return false, err
	}

	result, err := tx.Exec(
		`INSERT INTO pending_events (
			event_id,
			source_platform,
			source_id,
			target_platform,
			target_id,
			payload,
			available_at,
			retry_count,
			last_error,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '', ?) ON CONFLICT(event_id) DO NOTHING`,
		eventID,
		event.SourcePlatform,
		event.SourceID,
		event.TargetPlatform,
		event.TargetID,
		string(payload),
		availableAt,
		now,
	)
	if err != nil {
		rollback()
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rowsAffected > 0, nil
}

func ClaimNextPendingEvent(now time.Time, lease time.Duration) (QueuedEvent, bool, error) {
	if lease <= 0 {
		lease = 30 * time.Second
	}

	tx, err := DB.Begin()
	if err != nil {
		return QueuedEvent{}, false, err
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	var (
		eventID    string
		sp, sid    string
		tp, tid    string
		payload    string
		retryCount int
	)

	err = tx.QueryRow(
		`SELECT event_id, source_platform, source_id, target_platform, target_id, payload, retry_count
		 FROM pending_events
		 WHERE available_at <= ?
		 ORDER BY available_at ASC, created_at ASC
		 LIMIT 1`,
		now.Unix(),
	).Scan(&eventID, &sp, &sid, &tp, &tid, &payload, &retryCount)
	if errors.Is(err, sql.ErrNoRows) {
		rollback()
		return QueuedEvent{}, false, nil
	}
	if err != nil {
		rollback()
		return QueuedEvent{}, false, err
	}

	leaseUntil := now.Add(lease).Unix()
	if _, err := tx.Exec("UPDATE pending_events SET available_at = ? WHERE event_id = ?", leaseUntil, eventID); err != nil {
		rollback()
		return QueuedEvent{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return QueuedEvent{}, false, err
	}

	var event models.MediaEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return QueuedEvent{}, false, err
	}

	if strings.TrimSpace(event.EventID) == "" {
		event.EventID = eventID
	}
	if strings.TrimSpace(event.SourcePlatform) == "" {
		event.SourcePlatform = sp
	}
	if strings.TrimSpace(event.SourceID) == "" {
		event.SourceID = sid
	}
	if strings.TrimSpace(event.TargetPlatform) == "" {
		event.TargetPlatform = tp
	}
	if strings.TrimSpace(event.TargetID) == "" {
		event.TargetID = tid
	}

	return QueuedEvent{Event: event, RetryCount: retryCount}, true, nil
}

func AckPendingEvent(eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return errors.New("event id is required")
	}

	tx, err := DB.Begin()
	if err != nil {
		return err
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	if _, err := tx.Exec(
		"INSERT INTO processed_events (event_id, processed_at) VALUES (?, ?) ON CONFLICT(event_id) DO UPDATE SET processed_at = excluded.processed_at",
		eventID,
		time.Now().Unix(),
	); err != nil {
		rollback()
		return err
	}

	if _, err := tx.Exec("DELETE FROM pending_events WHERE event_id = ?", eventID); err != nil {
		rollback()
		return err
	}

	return tx.Commit()
}

func ReschedulePendingEvent(eventID string, retryCount int, nextAvailableAt time.Time, reason string) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return errors.New("event id is required")
	}

	_, err := DB.Exec(
		"UPDATE pending_events SET retry_count = ?, available_at = ?, last_error = ? WHERE event_id = ?",
		retryCount,
		nextAvailableAt.Unix(),
		strings.TrimSpace(reason),
		eventID,
	)
	return err
}

func MovePendingEventToDeadLetter(eventID, reason string) (int64, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return 0, errors.New("event id is required")
	}

	tx, err := DB.Begin()
	if err != nil {
		return 0, err
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	var (
		sp, sid    string
		tp, tid    string
		payload    string
		retryCount int
	)

	err = tx.QueryRow(
		"SELECT source_platform, source_id, target_platform, target_id, payload, retry_count FROM pending_events WHERE event_id = ?",
		eventID,
	).Scan(&sp, &sid, &tp, &tid, &payload, &retryCount)
	if errors.Is(err, sql.ErrNoRows) {
		rollback()
		return 0, sql.ErrNoRows
	}
	if err != nil {
		rollback()
		return 0, err
	}

	var event models.MediaEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		rollback()
		return 0, err
	}

	result, err := tx.Exec(
		`INSERT INTO dead_letters (
			event_id,
			source_platform,
			source_id,
			target_platform,
			target_id,
			file_name,
			payload,
			retry_count,
			failure_reason,
			failed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		sp,
		sid,
		tp,
		tid,
		event.FileName,
		payload,
		retryCount,
		strings.TrimSpace(reason),
		time.Now().Unix(),
	)
	if err != nil {
		rollback()
		return 0, err
	}

	deadLetterID, err := result.LastInsertId()
	if err != nil {
		rollback()
		return 0, err
	}

	if _, err := tx.Exec("DELETE FROM pending_events WHERE event_id = ?", eventID); err != nil {
		rollback()
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return deadLetterID, nil
}

func ListDeadLettersByTarget(targetPlatform, targetID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	rows, err := DB.Query(
		`SELECT dead_letter_id, event_id, source_platform, source_id, target_platform, target_id, file_name, retry_count, failure_reason, failed_at
		 FROM dead_letters
		 WHERE target_platform = ? AND target_id = ?
		 ORDER BY failed_at DESC
		 LIMIT ?`,
		targetPlatform,
		targetID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]DeadLetter, 0)
	for rows.Next() {
		var (
			item   DeadLetter
			failed int64
		)

		if err := rows.Scan(
			&item.ID,
			&item.EventID,
			&item.SourcePlatform,
			&item.SourceID,
			&item.TargetPlatform,
			&item.TargetID,
			&item.FileName,
			&item.RetryCount,
			&item.FailureReason,
			&failed,
		); err != nil {
			return nil, err
		}

		item.FailedAt = time.Unix(failed, 0)
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func ReplayDeadLetter(deadLetterID int64, targetPlatform, targetID string) (bool, error) {
	tx, err := DB.Begin()
	if err != nil {
		return false, err
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	var (
		eventID string
		sp, sid string
		tp, tid string
		payload string
	)

	err = tx.QueryRow(
		"SELECT event_id, source_platform, source_id, target_platform, target_id, payload FROM dead_letters WHERE dead_letter_id = ?",
		deadLetterID,
	).Scan(&eventID, &sp, &sid, &tp, &tid, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		rollback()
		return false, nil
	}
	if err != nil {
		rollback()
		return false, err
	}

	if strings.TrimSpace(targetPlatform) != "" && tp != strings.TrimSpace(targetPlatform) {
		rollback()
		return false, nil
	}
	if strings.TrimSpace(targetID) != "" && tid != strings.TrimSpace(targetID) {
		rollback()
		return false, nil
	}

	var processedEventID string
	err = tx.QueryRow("SELECT event_id FROM processed_events WHERE event_id = ?", eventID).Scan(&processedEventID)
	if err == nil {
		if _, err := tx.Exec("DELETE FROM dead_letters WHERE dead_letter_id = ?", deadLetterID); err != nil {
			rollback()
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		rollback()
		return false, err
	}

	now := time.Now().Unix()
	if _, err := tx.Exec(
		`INSERT INTO pending_events (
			event_id,
			source_platform,
			source_id,
			target_platform,
			target_id,
			payload,
			available_at,
			retry_count,
			last_error,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'replayed from dead letter', ?)
		 ON CONFLICT(event_id) DO UPDATE SET
			available_at = excluded.available_at,
			retry_count = 0,
			last_error = excluded.last_error`,
		eventID,
		sp,
		sid,
		tp,
		tid,
		payload,
		now,
		now,
	); err != nil {
		rollback()
		return false, err
	}

	if _, err := tx.Exec("DELETE FROM dead_letters WHERE dead_letter_id = ?", deadLetterID); err != nil {
		rollback()
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	return true, nil
}

func GetQueueStats() (int, int, error) {
	var queueDepth int
	var retryDepth sql.NullInt64

	err := DB.QueryRow(
		`SELECT COUNT(*), SUM(CASE WHEN retry_count > 0 THEN 1 ELSE 0 END)
		 FROM pending_events`,
	).Scan(&queueDepth, &retryDepth)
	if err != nil {
		return 0, 0, err
	}

	retries := 0
	if retryDepth.Valid {
		retries = int(retryDepth.Int64)
	}

	return queueDepth, retries, nil
}

// ---- Event History ----

type EventHistoryEntry struct {
	ID        int64
	EventID   string
	Status    string
	Details   string
	CreatedAt time.Time
}

// InsertEventHistory records a status transition for an event.
func InsertEventHistory(eventID, status, details string) error {
	_, err := DB.Exec(
		"INSERT INTO event_history (event_id, status, details, created_at) VALUES (?, ?, ?, ?)",
		strings.TrimSpace(eventID),
		strings.TrimSpace(status),
		strings.TrimSpace(details),
		time.Now().Unix(),
	)
	return err
}

// GetEventHistory retrieves the lifecycle log of a specific event.
func GetEventHistory(eventID string) ([]EventHistoryEntry, error) {
	rows, err := DB.Query(
		"SELECT id, event_id, status, details, created_at FROM event_history WHERE event_id = ? ORDER BY created_at ASC",
		eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]EventHistoryEntry, 0)
	for rows.Next() {
		var entry EventHistoryEntry
		var createdAt int64
		if err := rows.Scan(&entry.ID, &entry.EventID, &entry.Status, &entry.Details, &createdAt); err != nil {
			return nil, err
		}
		entry.CreatedAt = time.Unix(createdAt, 0)
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// ---- Audit Trail ----

func ensureAuditLogSchema() error {
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL,
		actor_platform TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		details TEXT DEFAULT '',
		created_at INTEGER NOT NULL
	)`)
	if err != nil {
		return err
	}
	_, err = DB.Exec("CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at)")
	return err
}

// InsertAuditLog records an administrative action.
func InsertAuditLog(action, actorPlatform, actorID, details string) error {
	_, err := DB.Exec(
		"INSERT INTO audit_log (action, actor_platform, actor_id, details, created_at) VALUES (?, ?, ?, ?, ?)",
		strings.TrimSpace(action),
		strings.TrimSpace(actorPlatform),
		strings.TrimSpace(actorID),
		strings.TrimSpace(details),
		time.Now().Unix(),
	)
	return err
}

// ListAuditLogs returns the most recent audit entries.
func ListAuditLogs(limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	rows, err := DB.Query(
		`SELECT id, action, actor_platform, actor_id, details, created_at
		 FROM audit_log ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]AuditEntry, 0)
	for rows.Next() {
		var entry AuditEntry
		var createdAt int64
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.ActorPlatform, &entry.ActorID, &entry.Details, &createdAt); err != nil {
			return nil, err
		}
		entry.CreatedAt = time.Unix(createdAt, 0)
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}
