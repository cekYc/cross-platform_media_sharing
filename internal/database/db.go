package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"
)

type Pairing struct {
	TGChatID     string
	DCChannelID  string
	BlockedWords []string
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

	if err := ensureIndexExists("idx_pairings_tg_chat_id", "pairings", "tg_chat_id"); err != nil {
		return err
	}

	if err := ensureIndexExists("idx_pairings_dc_channel_id", "pairings", "dc_channel_id"); err != nil {
		return err
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

	insertQuery := "INSERT INTO pairings (tg_chat_id, dc_channel_id, blocked_words) SELECT tg_chat_id, dc_channel_id, '' FROM pairings_legacy"
	if hasBlockedWords {
		insertQuery = "INSERT INTO pairings (tg_chat_id, dc_channel_id, blocked_words) SELECT tg_chat_id, dc_channel_id, COALESCE(blocked_words, '') FROM pairings_legacy"
	}

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
		"SELECT tg_chat_id, dc_channel_id, blocked_words FROM pairings WHERE tg_chat_id = ? ORDER BY dc_channel_id",
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
		"SELECT tg_chat_id, dc_channel_id, blocked_words FROM pairings WHERE dc_channel_id = ? ORDER BY tg_chat_id",
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

		if err := rows.Scan(&tgID, &dcChannelID, &blockedWords); err != nil {
			return nil, err
		}

		pairings = append(pairings, Pairing{
			TGChatID:     tgID,
			DCChannelID:  dcChannelID,
			BlockedWords: splitBlockedWords(blockedWords),
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pairings, nil
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
