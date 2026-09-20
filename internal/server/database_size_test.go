package server

import (
	"context"
	"testing"
	"time"
)

func TestCollectDatabaseSizes(t *testing.T) {
	srv := testServer(t)
	sampledAt := time.Date(2026, time.September, 18, 10, 37, 0, 0, time.UTC)

	if err := srv.collectDatabaseSizes(context.Background(), sampledAt); err != nil {
		t.Fatal(err)
	}
	samples, err := srv.stateDB.DatabaseSizeSamples(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}
	wantBucket := sampledAt.Truncate(time.Hour).UnixMilli()
	for _, sample := range samples {
		if sample.SampledAt != wantBucket {
			t.Errorf("%s sampled_at = %d, want %d", sample.Database, sample.SampledAt, wantBucket)
		}
		if sample.SizeBytes <= 0 {
			t.Errorf("%s size = %d, want positive", sample.Database, sample.SizeBytes)
		}
	}
}

func TestCollectDatabaseSizesWithoutOpenCode(t *testing.T) {
	srv := testServer(t)
	srv.db = nil

	if err := srv.collectDatabaseSizes(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	samples, err := srv.stateDB.DatabaseSizeSamples(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].Database != "ocman" {
		t.Fatalf("samples = %+v, want one ocman sample", samples)
	}
}

func TestDatabaseSizeCollectionWithoutStateDB(t *testing.T) {
	srv := &Server{}
	if err := srv.collectDatabaseSizes(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	srv.runDatabaseSizeLoop(context.Background())
}
