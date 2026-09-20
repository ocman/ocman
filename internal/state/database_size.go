package state

import (
	"context"
	"fmt"
)

type DatabaseSizeSample struct {
	Database  string `json:"database"`
	SampledAt int64  `json:"sampledAt"`
	SizeBytes int64  `json:"sizeBytes"`
}

func (d *DB) RecordDatabaseSizeSamples(ctx context.Context, samples []DatabaseSizeSample) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning database size samples: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, sample := range samples {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO database_size_sample (database, sampled_at, size_bytes)
			VALUES (?, ?, ?)
			ON CONFLICT(database, sampled_at) DO UPDATE SET size_bytes = excluded.size_bytes
		`, sample.Database, sample.SampledAt, sample.SizeBytes); err != nil {
			return fmt.Errorf("recording %s database size: %w", sample.Database, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing database size samples: %w", err)
	}
	return nil
}

func (d *DB) DatabaseSizeSamples(ctx context.Context, since int64) ([]DatabaseSizeSample, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT database, sampled_at, size_bytes
		FROM database_size_sample
		WHERE sampled_at >= ?
		ORDER BY sampled_at, database
	`, since)
	if err != nil {
		return nil, fmt.Errorf("listing database size samples: %w", err)
	}
	defer rows.Close()

	var samples []DatabaseSizeSample
	for rows.Next() {
		var sample DatabaseSizeSample
		if err := rows.Scan(&sample.Database, &sample.SampledAt, &sample.SizeBytes); err != nil {
			return nil, fmt.Errorf("scanning database size sample: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading database size samples: %w", err)
	}
	return samples, nil
}
