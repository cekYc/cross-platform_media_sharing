package database

// ListAllPairings returns pairings across all source/target combinations.
func ListAllPairings(limit int) ([]Pairing, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	rows, err := DB.Query(
		`SELECT source_platform, source_id, target_platform, target_id, rule_config, webhook_secret
		 FROM pairings
		 ORDER BY source_platform, source_id, target_platform, target_id
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPairings(rows)
}

// CountPairings returns the number of active pairings.
func CountPairings() (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM pairings").Scan(&count)
	return count, err
}

// CountDeadLetters returns the number of dead-letter events currently stored.
func CountDeadLetters() (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM dead_letters").Scan(&count)
	return count, err
}
