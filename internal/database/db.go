package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"tg-discord-bot/internal/models"

	_ "modernc.org/sqlite"
)

type Pairing struct {
	TGChatID     string
	DCChannelID  string
	BlockedWords []string
	RuleConfig   models.RuleConfig
}

type QueuedEvent struct {
	Event      models.MediaEvent
	RetryCount int
}

type DeadLetter struct {
	ID            int64
	EventID       string
	SourceTGID    string
	TargetDCID    string
	FileName      string
	RetryCount    int
	FailureReason string
	FailedAt      time.Time
}

var DB *sql.DB

func InitDB() {
	var err error
	DB, err = sql.Open("sqlite", "bot.db")
	if err != nil {
		log.Fatal(err)
	}

	if err := ensurePairingsSchema(); err != nil {
		log.Fatal("database migration failed:", err)
	}

	if err := ensureQueueSchema(); err != nil {
		log.Fatal("queue schema migration failed:", err)
	}
}

func ensurePairingsSchema() error {
	exists, err := tableExists("pairings")
	if err != nil {
		return err
	}

	if !exists {
		return createPairingsTable()
	}

	legacy, err := isLegacyPairingsSchema()
	if err != nil {
		return err
	}
	if legacy {
		if err := migrateLegacyPairingsTable(); err != nil {
			return err
		}
	}

	if err := ensureColumnExists("pairings", "blocked_words", "TEXT DEFAULT ''"); err != nil {
		return err
	}

	if err := ensureColumnExists("pairings", "rule_config", "TEXT DEFAULT '{}'"); err != nil {
		return err
	}

	if err := ensureIndexExists("idx_pairings_tg_chat_id", "pairings", "tg_chat_id"); err != nil {
		return err
	}

	if err := ensureIndexExists("idx_pairings_dc_channel_id", "pairings", "dc_channel_id"); err != nil {
		return err
	}

	return nil
}

func ensureQueueSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS pending_events (
			event_id TEXT PRIMARY KEY,
			source_tg_id TEXT NOT NULL,
			target_dc_channel_id TEXT NOT NULL,
			payload TEXT NOT NULL,
			available_at INTEGER NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		"CREATE INDEX IF NOT EXISTS idx_pending_events_available_at ON pending_events(available_at)",
		"CREATE INDEX IF NOT EXISTS idx_pending_events_target_dc_channel_id ON pending_events(target_dc_channel_id)",
		`CREATE TABLE IF NOT EXISTS processed_events (
			event_id TEXT PRIMARY KEY,
			processed_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS dead_letters (
			dead_letter_id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL,
			source_tg_id TEXT NOT NULL,
			target_dc_channel_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			payload TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			failure_reason TEXT NOT NULL,
			failed_at INTEGER NOT NULL
		)`,
		"CREATE INDEX IF NOT EXISTS idx_dead_letters_target_dc_channel_id ON dead_letters(target_dc_channel_id)",
		"CREATE INDEX IF NOT EXISTS idx_dead_letters_failed_at ON dead_letters(failed_at)",
	}

	for _, query := range queries {
		if _, err := DB.Exec(query); err != nil {
			return err
		}
	}

	return nil
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

func createPairingsTable() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS pairings (
			tg_chat_id TEXT NOT NULL,
			dc_channel_id TEXT NOT NULL,
			blocked_words TEXT DEFAULT '',
			rule_config TEXT DEFAULT '{}',
			PRIMARY KEY (tg_chat_id, dc_channel_id)
		)`,
		"CREATE INDEX IF NOT EXISTS idx_pairings_tg_chat_id ON pairings(tg_chat_id)",
		"CREATE INDEX IF NOT EXISTS idx_pairings_dc_channel_id ON pairings(dc_channel_id)",
	}

	for _, query := range queries {
		if _, err := DB.Exec(query); err != nil {
			return err
		}
	}

	return nil
}

func createPairingsTableTx(tx *sql.Tx) error {
	queries := []string{
		`CREATE TABLE pairings (
			tg_chat_id TEXT NOT NULL,
			dc_channel_id TEXT NOT NULL,
			blocked_words TEXT DEFAULT '',
			rule_config TEXT DEFAULT '{}',
			PRIMARY KEY (tg_chat_id, dc_channel_id)
		)`,
		"CREATE INDEX IF NOT EXISTS idx_pairings_tg_chat_id ON pairings(tg_chat_id)",
		"CREATE INDEX IF NOT EXISTS idx_pairings_dc_channel_id ON pairings(dc_channel_id)",
	}

	for _, query := range queries {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}

	return nil
}

func isLegacyPairingsSchema() (bool, error) {
	rows, err := DB.Query("PRAGMA table_info(pairings)")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	hasTGChatID := false
	hasDCChannelID := false
	primaryKeyColumns := make(map[string]struct{})

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var pk int

		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}

		if name == "tg_chat_id" {
			hasTGChatID = true
		}
		if name == "dc_channel_id" {
			hasDCChannelID = true
		}
		if pk > 0 {
			primaryKeyColumns[name] = struct{}{}
		}
	}

	if err := rows.Err(); err != nil {
		return false, err
	}

	if !hasTGChatID || !hasDCChannelID {
		return false, nil
	}

	if len(primaryKeyColumns) == 1 {
		_, isLegacy := primaryKeyColumns["tg_chat_id"]
		return isLegacy, nil
	}

	return false, nil
}

func migrateLegacyPairingsTable() error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}

	if _, err := tx.Exec("DROP TABLE IF EXISTS pairings_legacy"); err != nil {
		tx.Rollback()
		return err
	}

	if _, err := tx.Exec("ALTER TABLE pairings RENAME TO pairings_legacy"); err != nil {
		tx.Rollback()
		return err
	}

	if err := createPairingsTableTx(tx); err != nil {
		tx.Rollback()
		return err
	}

	hasBlockedWords, err := tableHasColumnTx(tx, "pairings_legacy", "blocked_words")
	if err != nil {
		tx.Rollback()
		return err
	}

	hasRuleConfig, err := tableHasColumnTx(tx, "pairings_legacy", "rule_config")
	if err != nil {
		tx.Rollback()
		return err
	}

	blockedCol := "''"
	if hasBlockedWords {
		blockedCol = "COALESCE(blocked_words, '')"
	}

	ruleCol := "'{}'"
	if hasRuleConfig {
		ruleCol = "COALESCE(rule_config, '{}')"
	}

	insertQuery := fmt.Sprintf("INSERT INTO pairings (tg_chat_id, dc_channel_id, blocked_words, rule_config) SELECT tg_chat_id, dc_channel_id, %s, %s FROM pairings_legacy", blockedCol, ruleCol)

	if _, err := tx.Exec(insertQuery); err != nil {
		tx.Rollback()
		return err
	}

	if _, err := tx.Exec("DROP TABLE pairings_legacy"); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func ensureColumnExists(tableName, columnName, columnDefinition string) error {
	rows, err := DB.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var pk int

		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}

		if name == columnName {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	_, err = DB.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tableName, columnName, columnDefinition))
	return err
}

func ensureIndexExists(indexName, tableName, columnName string) error {
	_, err := DB.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(%s)", indexName, tableName, columnName))
	return err
}

func tableHasColumnTx(tx *sql.Tx, tableName, columnName string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var pk int

		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}

		if name == columnName {
			return true, nil
		}
	}

	if err := rows.Err(); err != nil {
		return false, err
	}

	return false, nil
}

func LinkChannel(tgID, dcChannelID string) error {
	tgID = strings.TrimSpace(tgID)
	dcChannelID = strings.TrimSpace(dcChannelID)

	if tgID == "" || dcChannelID == "" {
		return errors.New("telegram chat id and discord channel id are required")
	}

	_, err := DB.Exec(
		"INSERT INTO pairings (tg_chat_id, dc_channel_id) VALUES (?, ?) ON CONFLICT(tg_chat_id, dc_channel_id) DO NOTHING",
		tgID,
		dcChannelID,
	)
	return err
}

func UnlinkChannel(tgID, dcChannelID string) (bool, error) {
	result, err := DB.Exec("DELETE FROM pairings WHERE tg_chat_id = ? AND dc_channel_id = ?", tgID, dcChannelID)
	if err != nil {
		return false, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rowsAffected > 0, nil
}

func GetPairingsByTelegramChat(tgID string) ([]Pairing, error) {
	rows, err := DB.Query(
		"SELECT tg_chat_id, dc_channel_id, blocked_words, rule_config FROM pairings WHERE tg_chat_id = ? ORDER BY dc_channel_id",
		tgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPairings(rows)
}

func GetPairingsByDiscordChannel(dcChannelID string) ([]Pairing, error) {
	rows, err := DB.Query(
		"SELECT tg_chat_id, dc_channel_id, blocked_words, rule_config FROM pairings WHERE dc_channel_id = ? ORDER BY tg_chat_id",
		dcChannelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPairings(rows)
}

func CountPairingsByTelegramChat(tgID string) (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM pairings WHERE tg_chat_id = ?", tgID).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func scanPairings(rows *sql.Rows) ([]Pairing, error) {
	pairings := make([]Pairing, 0)

	for rows.Next() {
		var tgID string
		var dcChannelID string
		var blockedWords sql.NullString
		var ruleConfigStr sql.NullString

		if err := rows.Scan(&tgID, &dcChannelID, &blockedWords, &ruleConfigStr); err != nil {
			return nil, err
		}

		var ruleConfig models.RuleConfig
		if ruleConfigStr.Valid && ruleConfigStr.String != "" {
			_ = json.Unmarshal([]byte(ruleConfigStr.String), &ruleConfig)
		}

		pairings = append(pairings, Pairing{
			TGChatID:     tgID,
			DCChannelID:  dcChannelID,
			BlockedWords: splitBlockedWords(blockedWords),
			RuleConfig:   ruleConfig,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pairings, nil
}

func UpdateRuleConfig(tgID, dcChannelID string, config models.RuleConfig) error {
	configBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}

	_, err = DB.Exec(
		"UPDATE pairings SET rule_config = ? WHERE tg_chat_id = ? AND dc_channel_id = ?",
		string(configBytes),
		tgID,
		dcChannelID,
	)
	return err
}

func GetRuleConfig(tgID, dcChannelID string) (models.RuleConfig, error) {
	var ruleConfigStr sql.NullString
	err := DB.QueryRow(
		"SELECT rule_config FROM pairings WHERE tg_chat_id = ? AND dc_channel_id = ?",
		tgID,
		dcChannelID,
	).Scan(&ruleConfigStr)
	
	if err != nil {
		return models.RuleConfig{}, err
	}

	var config models.RuleConfig
	if ruleConfigStr.Valid && ruleConfigStr.String != "" {
		_ = json.Unmarshal([]byte(ruleConfigStr.String), &config)
	}
	return config, nil
}

func GetBlockedWords(tgID, dcChannelID string) ([]string, error) {
	var words sql.NullString
	err := DB.QueryRow(
		"SELECT blocked_words FROM pairings WHERE tg_chat_id = ? AND dc_channel_id = ?",
		tgID,
		dcChannelID,
	).Scan(&words)
	if err != nil {
		return []string{}, err
	}

	return splitBlockedWords(words), nil
}

func AddBlockedWord(tgID, dcChannelID, word string) error {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return errors.New("blocked word cannot be empty")
	}

	var currentWords sql.NullString
	err := DB.QueryRow(
		"SELECT blocked_words FROM pairings WHERE tg_chat_id = ? AND dc_channel_id = ?",
		tgID,
		dcChannelID,
	).Scan(&currentWords)
	if err != nil {
		return err
	}

	newWords := mergeBlockedWords(currentWords, normalizedWord)

	_, err = DB.Exec(
		"UPDATE pairings SET blocked_words = ? WHERE tg_chat_id = ? AND dc_channel_id = ?",
		newWords,
		tgID,
		dcChannelID,
	)
	return err
}

func AddBlockedWordForAllChannels(tgID, word string) (int, error) {
	pairings, err := GetPairingsByTelegramChat(tgID)
	if err != nil {
		return 0, err
	}

	if len(pairings) == 0 {
		return 0, sql.ErrNoRows
	}

	updatedChannels := 0
	for _, pairing := range pairings {
		if err := AddBlockedWord(tgID, pairing.DCChannelID, word); err != nil {
			return updatedChannels, err
		}

		updatedChannels++
	}

	return updatedChannels, nil
}

func RemoveBlockedWord(tgID, dcChannelID, word string) (bool, error) {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return false, errors.New("blocked word cannot be empty")
	}

	var currentWords sql.NullString
	err := DB.QueryRow(
		"SELECT blocked_words FROM pairings WHERE tg_chat_id = ? AND dc_channel_id = ?",
		tgID,
		dcChannelID,
	).Scan(&currentWords)
	if err != nil {
		return false, err
	}

	newWords, removed := removeBlockedWord(currentWords, normalizedWord)
	if !removed {
		return false, nil
	}

	_, err = DB.Exec(
		"UPDATE pairings SET blocked_words = ? WHERE tg_chat_id = ? AND dc_channel_id = ?",
		newWords,
		tgID,
		dcChannelID,
	)
	if err != nil {
		return false, err
	}

	return true, nil
}

func RemoveBlockedWordFromAllChannels(tgID, word string) (int, error) {
	pairings, err := GetPairingsByTelegramChat(tgID)
	if err != nil {
		return 0, err
	}

	if len(pairings) == 0 {
		return 0, sql.ErrNoRows
	}

	removedChannels := 0
	for _, pairing := range pairings {
		removed, err := RemoveBlockedWord(tgID, pairing.DCChannelID, word)
		if err != nil {
			return removedChannels, err
		}
		if removed {
			removedChannels++
		}
	}

	return removedChannels, nil
}

func ClearBlockedWords(tgID, dcChannelID string) error {
	_, err := DB.Exec(
		"UPDATE pairings SET blocked_words = '' WHERE tg_chat_id = ? AND dc_channel_id = ?",
		tgID,
		dcChannelID,
	)
	return err
}

func ClearBlockedWordsForAllChannels(tgID string) (int, error) {
	result, err := DB.Exec("UPDATE pairings SET blocked_words = '' WHERE tg_chat_id = ?", tgID)
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

func GetBlockedWordListsByTelegramChat(tgID string) (map[string][]string, error) {
	pairings, err := GetPairingsByTelegramChat(tgID)
	if err != nil {
		return nil, err
	}

	if len(pairings) == 0 {
		return map[string][]string{}, nil
	}

	result := make(map[string][]string, len(pairings))
	for _, pairing := range pairings {
		result[pairing.DCChannelID] = pairing.BlockedWords
	}

	return result, nil
}

func normalizeBlockedWord(word string) string {
	return strings.ToLower(strings.TrimSpace(word))
}

func splitBlockedWords(words sql.NullString) []string {
	if !words.Valid || strings.TrimSpace(words.String) == "" {
		return []string{}
	}

	parts := strings.Split(words.String, ",")
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

func mergeBlockedWords(existing sql.NullString, word string) string {
	normalizedWord := normalizeBlockedWord(word)
	existingWords := splitBlockedWords(existing)
	if normalizedWord == "" {
		return strings.Join(existingWords, ",")
	}

	for _, existingWord := range existingWords {
		if existingWord == normalizedWord {
			return strings.Join(existingWords, ",")
		}
	}

	existingWords = append(existingWords, normalizedWord)
	return strings.Join(existingWords, ",")
}

func removeBlockedWord(existing sql.NullString, word string) (string, bool) {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return strings.Join(splitBlockedWords(existing), ","), false
	}

	existingWords := splitBlockedWords(existing)
	filteredWords := make([]string, 0, len(existingWords))
	removed := false

	for _, existingWord := range existingWords {
		if existingWord == normalizedWord {
			removed = true
			continue
		}
		filteredWords = append(filteredWords, existingWord)
	}

	return strings.Join(filteredWords, ","), removed
}

func EnqueuePendingEvent(event models.MediaEvent) (bool, error) {
	eventID := strings.TrimSpace(event.EventID)
	if eventID == "" {
		return false, errors.New("event id is required")
	}

	event.EventID = eventID
	event.SourceTGID = strings.TrimSpace(event.SourceTGID)
	event.TargetDCID = strings.TrimSpace(event.TargetDCID)

	if event.SourceTGID == "" || event.TargetDCID == "" {
		return false, errors.New("source telegram id and target discord channel id are required")
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
			source_tg_id,
			target_dc_channel_id,
			payload,
			available_at,
			retry_count,
			last_error,
			created_at
		) VALUES (?, ?, ?, ?, ?, 0, '', ?) ON CONFLICT(event_id) DO NOTHING`,
		eventID,
		event.SourceTGID,
		event.TargetDCID,
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
		sourceTGID string
		targetDCID string
		payload    string
		retryCount int
	)

	err = tx.QueryRow(
		`SELECT event_id, source_tg_id, target_dc_channel_id, payload, retry_count
		 FROM pending_events
		 WHERE available_at <= ?
		 ORDER BY available_at ASC, created_at ASC
		 LIMIT 1`,
		now.Unix(),
	).Scan(&eventID, &sourceTGID, &targetDCID, &payload, &retryCount)
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
	if strings.TrimSpace(event.SourceTGID) == "" {
		event.SourceTGID = sourceTGID
	}
	if strings.TrimSpace(event.TargetDCID) == "" {
		event.TargetDCID = targetDCID
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
		sourceTGID string
		targetDCID string
		payload    string
		retryCount int
	)

	err = tx.QueryRow(
		"SELECT source_tg_id, target_dc_channel_id, payload, retry_count FROM pending_events WHERE event_id = ?",
		eventID,
	).Scan(&sourceTGID, &targetDCID, &payload, &retryCount)
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
			source_tg_id,
			target_dc_channel_id,
			file_name,
			payload,
			retry_count,
			failure_reason,
			failed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		sourceTGID,
		targetDCID,
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

func ListDeadLettersByChannel(dcChannelID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	rows, err := DB.Query(
		`SELECT dead_letter_id, event_id, source_tg_id, target_dc_channel_id, file_name, retry_count, failure_reason, failed_at
		 FROM dead_letters
		 WHERE target_dc_channel_id = ?
		 ORDER BY failed_at DESC
		 LIMIT ?`,
		dcChannelID,
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
			&item.SourceTGID,
			&item.TargetDCID,
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

func ReplayDeadLetter(deadLetterID int64, dcChannelID string) (bool, error) {
	tx, err := DB.Begin()
	if err != nil {
		return false, err
	}

	rollback := func() {
		_ = tx.Rollback()
	}

	var (
		eventID    string
		sourceTGID string
		targetDCID string
		payload    string
	)

	err = tx.QueryRow(
		"SELECT event_id, source_tg_id, target_dc_channel_id, payload FROM dead_letters WHERE dead_letter_id = ?",
		deadLetterID,
	).Scan(&eventID, &sourceTGID, &targetDCID, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		rollback()
		return false, nil
	}
	if err != nil {
		rollback()
		return false, err
	}

	if strings.TrimSpace(dcChannelID) != "" && targetDCID != strings.TrimSpace(dcChannelID) {
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
			source_tg_id,
			target_dc_channel_id,
			payload,
			available_at,
			retry_count,
			last_error,
			created_at
		) VALUES (?, ?, ?, ?, ?, 0, 'replayed from dead letter', ?)
		 ON CONFLICT(event_id) DO UPDATE SET
			available_at = excluded.available_at,
			retry_count = 0,
			last_error = excluded.last_error`,
		eventID,
		sourceTGID,
		targetDCID,
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
