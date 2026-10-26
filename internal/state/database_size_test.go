package state

import (
	"context"
	"database/sql"
	"testing"
)

func TestHistoryDatabaseSizeMigrationPreservesExistingSamples(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := migrateToV93(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO database_size_sample VALUES ('ocman', 1, 100), ('opencode', 1, 200)`); err != nil {
		t.Fatal(err)
	}
	if err := migrateHistoryDatabaseSize(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO database_size_sample VALUES ('statev2', 1, 300)`); err != nil {
		t.Fatal(err)
	}
	var count, total int
	if err := tx.QueryRow(`SELECT count(*), sum(size_bytes) FROM database_size_sample`).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 3 || total != 600 {
		t.Fatalf("lost samples: count %d total %d", count, total)
	}
}

func TestDatabaseSizeSamplesUpsertHourlyBucket(t *testing.T) {
	store, err := Open(templateDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.RecordDatabaseSizeSamples(ctx, []DatabaseSizeSample{
		{Database: "opencode", SampledAt: 3_600_000, SizeBytes: 100},
		{Database: "ocman", SampledAt: 3_600_000, SizeBytes: 200},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDatabaseSizeSamples(ctx, []DatabaseSizeSample{
		{Database: "opencode", SampledAt: 3_600_000, SizeBytes: 150},
	}); err != nil {
		t.Fatal(err)
	}

	samples, err := store.DatabaseSizeSamples(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}
	if samples[0].Database != "ocman" || samples[0].SizeBytes != 200 {
		t.Fatalf("first sample = %+v", samples[0])
	}
	if samples[1].Database != "opencode" || samples[1].SizeBytes != 150 {
		t.Fatalf("second sample = %+v", samples[1])
	}
}

func TestPlatformStorageMigrationPreservesSamples(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := migrateHistoryDatabaseSize(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO database_size_sample VALUES ('opencode', 1, 200), ('statev2', 1, 300)`); err != nil {
		t.Fatal(err)
	}
	if err := migratePlatformStorage(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO database_size_sample VALUES ('claudecode:raw', 1, 400), ('claudecode:index', 1, 100)`); err != nil {
		t.Fatal(err)
	}
	var count, total int
	if err := tx.QueryRow(`SELECT count(*), sum(size_bytes) FROM database_size_sample`).Scan(&count, &total); err != nil {
		t.Fatal(err)
	}
	if count != 4 || total != 1000 {
		t.Fatalf("lost samples: count %d total %d", count, total)
	}
	for _, query := range []string{
		`INSERT INTO database_size_sample VALUES ('', 1, 1)`,
		`INSERT INTO database_size_sample VALUES ('invalid', 1, -1)`,
	} {
		if _, err := tx.Exec(query); err == nil {
			t.Fatalf("invalid sample accepted: %s", query)
		}
	}
}
