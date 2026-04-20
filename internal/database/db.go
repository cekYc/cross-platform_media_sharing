package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB() {
	var err error
	DB, err = sql.Open("sqlite", "bot.db")
	if err != nil {
		log.Fatal(err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS pairings (
		tg_chat_id TEXT PRIMARY KEY,
		dc_channel_id TEXT NOT NULL,
		blocked_words TEXT DEFAULT ''
	);`

	_, err = DB.Exec(query)
	if err != nil {
		log.Fatal("database table creation failed:", err)
	}

	if err := ensureColumnExists("pairings", "blocked_words", "TEXT DEFAULT ''"); err != nil {
		log.Fatal("database migration failed:", err)
	}
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

func GetDiscordChannel(tgID string) (string, error) {
	var dcID string
	err := DB.QueryRow("SELECT dc_channel_id FROM pairings WHERE tg_chat_id = ?", tgID).Scan(&dcID)
	return dcID, err
}

func AddBlockedWord(tgID string, word string) error {
	normalizedWord := normalizeBlockedWord(word)
	if normalizedWord == "" {
		return errors.New("blocked word cannot be empty")
	}

	var currentWords sql.NullString
	err := DB.QueryRow("SELECT blocked_words FROM pairings WHERE tg_chat_id = ?", tgID).Scan(&currentWords)
	if err != nil {
		return err
	}

	newWords := mergeBlockedWords(currentWords, normalizedWord)

	_, err = DB.Exec("UPDATE pairings SET blocked_words = ? WHERE tg_chat_id = ?", newWords, tgID)
	return err
}

func GetBlockedWords(tgID string) ([]string, error) {
	var words sql.NullString
	err := DB.QueryRow("SELECT blocked_words FROM pairings WHERE tg_chat_id = ?", tgID).Scan(&words)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return []string{}, err
	}

	return splitBlockedWords(words), nil
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
