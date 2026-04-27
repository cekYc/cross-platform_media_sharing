package database

import (
	"log"
	"time"
)

// RunRetentionCleanup deletes old records to prevent the database from growing indefinitely.
// It processes deletions in small batches to avoid locking the database for extended periods.
func RunRetentionCleanup() {
	log.Println("[RETENTION] Starting retention cleanup process")

	// Delete processed events older than 7 days
	cleanupTable("processed_events", "processed_at", time.Now().Add(-7*24*time.Hour).Unix(), 500)

	// Delete event history older than 7 days
	cleanupTable("event_history", "created_at", time.Now().Add(-7*24*time.Hour).Unix(), 500)

	// Delete dead letters older than 14 days
	cleanupTable("dead_letters", "failed_at", time.Now().Add(-14*24*time.Hour).Unix(), 500)

	// Delete audit logs older than 30 days
	cleanupTable("audit_log", "created_at", time.Now().Add(-30*24*time.Hour).Unix(), 500)

	log.Println("[RETENTION] Finished retention cleanup process")
}

// cleanupTable deletes rows from a table where timestampColumn < cutoff, in batches of batchSize.
func cleanupTable(tableName, timestampColumn string, cutoff int64, batchSize int) {
	for {
		query := "DELETE FROM " + tableName + " WHERE rowid IN (SELECT rowid FROM " + tableName + " WHERE " + timestampColumn + " < ? LIMIT ?)"
		result, err := DB.Exec(query, cutoff, batchSize)
		if err != nil {
			log.Printf("[RETENTION] Error cleaning up %s: %v\n", tableName, err)
			break
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			log.Printf("[RETENTION] Error getting rows affected for %s: %v\n", tableName, err)
			break
		}

		if rowsAffected == 0 {
			break
		}

		// Small sleep to yield to other database operations
		time.Sleep(50 * time.Millisecond)
	}
}
