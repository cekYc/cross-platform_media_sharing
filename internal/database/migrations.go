package database

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

type Migration struct {
	Version int
	Name    string
	Up      func(tx *sql.Tx) error
}

var migrations = []Migration{
	{
		Version: 1,
		Name:    "init_baseline",
		Up:      v1Init,
	},
	{
		Version: 2,
		Name:    "normalize_blocked_words",
		Up:      v2NormalizeBlockedWords,
	},
	{
		Version: 3,
		Name:    "create_event_history",
		Up:      v3CreateEventHistory,
	},
}

// RunMigrations executes all pending migrations in order.
func RunMigrations() error {
	// Create schema_migrations table if it doesn't exist
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations: %w", err)
	}

	// Check current version
	var currentVersion int
	err = DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&currentVersion)
	if err != nil && err != sql.ErrNoRows {
		// If the table is empty, MAX returns NULL, and Scan() might return an error
		// depending on how it's handled, but we ignore it and assume 0.
		currentVersion = 0
	}

	// Special case: migrating from an unversioned schema
	if currentVersion == 0 {
		exists, err := tableExists("pairings")
		if err != nil {
			return err
		}
		if exists {
			// Unversioned database already exists!
			// We should assume v1 is already applied implicitly.
			// But wait, the unversioned database might not have all columns.
			// `v1Init` is designed to be idempotent (IF NOT EXISTS) and will run ensure logic.
			// So it's safe to just run v1.
		}
	}

	for _, m := range migrations {
		if m.Version > currentVersion {
			log.Printf("[MIGRATION] Applying v%d: %s", m.Version, m.Name)
			tx, err := DB.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin tx for v%d: %w", m.Version, err)
			}

			if err := m.Up(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration v%d failed: %w", m.Version, err)
			}

			_, err = tx.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.Version, time.Now().Unix())
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to record migration v%d: %w", m.Version, err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit migration v%d: %w", m.Version, err)
			}
			log.Printf("[MIGRATION] Successfully applied v%d", m.Version)
		}
	}

	return nil
}

// v1Init brings the database up to the state it was right before Section 8.
func v1Init(tx *sql.Tx) error {
	// 1. Pairings Schema
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pairings (
		source_platform TEXT NOT NULL,
		source_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_id TEXT NOT NULL,
		blocked_words TEXT DEFAULT '',
		rule_config TEXT DEFAULT '{}',
		webhook_secret TEXT DEFAULT '',
		PRIMARY KEY (source_platform, source_id, target_platform, target_id)
	)`)
	if err != nil { return err }

	_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_pairings_source ON pairings(source_platform, source_id)")
	if err != nil { return err }

	_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_pairings_target ON pairings(target_platform, target_id)")
	if err != nil { return err }

	// Ensure old columns exist in case this was an old legacy unversioned DB
	if err := ensureColumnExistsTx(tx, "pairings", "blocked_words", "TEXT DEFAULT ''"); err != nil { return err }
	if err := ensureColumnExistsTx(tx, "pairings", "rule_config", "TEXT DEFAULT '{}'"); err != nil { return err }
	if err := ensureColumnExistsTx(tx, "pairings", "webhook_secret", "TEXT DEFAULT ''"); err != nil { return err }

	// 2. Queue Schema
	queueQueries := []string{
		`CREATE TABLE IF NOT EXISTS pending_events (
			event_id TEXT PRIMARY KEY,
			source_platform TEXT NOT NULL,
			source_id TEXT NOT NULL,
			target_platform TEXT NOT NULL,
			target_id TEXT NOT NULL,
			payload TEXT NOT NULL,
			available_at INTEGER NOT NULL,
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		"CREATE INDEX IF NOT EXISTS idx_pending_events_available_at ON pending_events(available_at)",
		"CREATE INDEX IF NOT EXISTS idx_pending_events_target ON pending_events(target_platform, target_id)",
		`CREATE TABLE IF NOT EXISTS processed_events (
			event_id TEXT PRIMARY KEY,
			processed_at INTEGER NOT NULL
		)`,
		// Index added here as requested for fast retention cleanup
		"CREATE INDEX IF NOT EXISTS idx_processed_events_processed_at ON processed_events(processed_at)",
		`CREATE TABLE IF NOT EXISTS dead_letters (
			dead_letter_id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id TEXT NOT NULL,
			source_platform TEXT NOT NULL,
			source_id TEXT NOT NULL,
			target_platform TEXT NOT NULL,
			target_id TEXT NOT NULL,
			file_name TEXT NOT NULL,
			payload TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			failure_reason TEXT NOT NULL,
			failed_at INTEGER NOT NULL
		)`,
		"CREATE INDEX IF NOT EXISTS idx_dead_letters_target ON dead_letters(target_platform, target_id)",
		"CREATE INDEX IF NOT EXISTS idx_dead_letters_failed_at ON dead_letters(failed_at)",
	}
	for _, query := range queueQueries {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}

	// 3. Audit Log Schema
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL,
		actor_platform TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		details TEXT DEFAULT '',
		created_at INTEGER NOT NULL
	)`)
	if err != nil { return err }
	_, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at)")
	if err != nil { return err }

	return nil
}

// v2NormalizeBlockedWords extracts blocked words into a separate table and rebuilds pairings without that column.
func v2NormalizeBlockedWords(tx *sql.Tx) error {
	// 1. Create blocked_words table
	_, err := tx.Exec(`CREATE TABLE blocked_words (
		source_platform TEXT NOT NULL,
		source_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_id TEXT NOT NULL,
		word TEXT NOT NULL,
		PRIMARY KEY (source_platform, source_id, target_platform, target_id, word)
	)`)
	if err != nil { return err }
	_, err = tx.Exec("CREATE INDEX idx_blocked_words_source ON blocked_words(source_platform, source_id)")
	if err != nil { return err }

	// 2. Read existing pairings and insert words
	rows, err := tx.Query("SELECT source_platform, source_id, target_platform, target_id, blocked_words FROM pairings")
	if err != nil {
		// If blocked_words column doesn't exist, we probably already dropped it, skip.
		// But in a strict migration flow this shouldn't happen unless partially applied.
		return err
	}
	defer rows.Close()

	type legacyPairing struct {
		sp, si, tp, ti string
		words          sql.NullString
	}
	var pairs []legacyPairing
	for rows.Next() {
		var p legacyPairing
		if err := rows.Scan(&p.sp, &p.si, &p.tp, &p.ti, &p.words); err != nil {
			return err
		}
		pairs = append(pairs, p)
	}

	insertStmt, err := tx.Prepare("INSERT OR IGNORE INTO blocked_words (source_platform, source_id, target_platform, target_id, word) VALUES (?, ?, ?, ?, ?)")
	if err != nil { return err }
	defer insertStmt.Close()

	for _, p := range pairs {
		if p.words.Valid && p.words.String != "" {
			for _, w := range splitBlockedWordsLegacy(p.words.String) {
				if _, err := insertStmt.Exec(p.sp, p.si, p.tp, p.ti, w); err != nil {
					return err
				}
			}
		}
	}

	// 3. Rebuild pairings table without blocked_words
	_, err = tx.Exec("ALTER TABLE pairings RENAME TO pairings_old")
	if err != nil { return err }

	_, err = tx.Exec(`CREATE TABLE pairings (
		source_platform TEXT NOT NULL,
		source_id TEXT NOT NULL,
		target_platform TEXT NOT NULL,
		target_id TEXT NOT NULL,
		rule_config TEXT DEFAULT '{}',
		webhook_secret TEXT DEFAULT '',
		PRIMARY KEY (source_platform, source_id, target_platform, target_id)
	)`)
	if err != nil { return err }

	_, err = tx.Exec("DROP INDEX IF EXISTS idx_pairings_source")
	if err != nil { return err }
	_, err = tx.Exec("CREATE INDEX idx_pairings_source ON pairings(source_platform, source_id)")
	if err != nil { return err }

	_, err = tx.Exec("DROP INDEX IF EXISTS idx_pairings_target")
	if err != nil { return err }
	_, err = tx.Exec("CREATE INDEX idx_pairings_target ON pairings(target_platform, target_id)")
	if err != nil { return err }

	_, err = tx.Exec(`INSERT INTO pairings (source_platform, source_id, target_platform, target_id, rule_config, webhook_secret)
		SELECT source_platform, source_id, target_platform, target_id, rule_config, webhook_secret FROM pairings_old`)
	if err != nil { return err }

	_, err = tx.Exec("DROP TABLE pairings_old")
	if err != nil { return err }

	return nil
}

// v3CreateEventHistory creates the event_history table for auditing lifecycle of events.
func v3CreateEventHistory(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS event_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL,
		status TEXT NOT NULL,
		details TEXT DEFAULT '',
		created_at INTEGER NOT NULL
	)`)
	if err != nil { return err }

	_, err = tx.Exec("CREATE INDEX idx_event_history_event_id ON event_history(event_id)")
	if err != nil { return err }
	
	// Add index for retention cleanup as requested
	_, err = tx.Exec("CREATE INDEX idx_event_history_created_at ON event_history(created_at)")
	if err != nil { return err }

	return nil
}

func ensureColumnExistsTx(tx *sql.Tx, table, column, def string) error {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil { return err }
	defer rows.Close()

	hasCol := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil { return err }
		if name == column { hasCol = true }
	}

	if !hasCol {
		_, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
		return err
	}
	return nil
}
