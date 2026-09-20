package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDatabaseSizeSamplesUpsertHourlyBucket(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
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
