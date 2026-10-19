package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestCollectDatabaseSizes(t *testing.T) {
	srv := newFixture(t)
	sampledAt := time.Date(2026, time.September, 18, 10, 37, 0, 0, time.UTC)

	if err := srv.collectDatabaseSizes(context.Background(), sampledAt); err != nil {
		t.Fatal(err)
	}
	samples, err := srv.deps.Store.DatabaseSizeSamples(context.Background(), 0)
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

func TestCollectHistoryDatabaseSizeBeforeImportReady(t *testing.T) {
	srv := newFixture(t)
	store, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv.deps.HistoryAnalytics = func() *HistoryAnalytics { return &HistoryAnalytics{Store: store} }
	if err := srv.collectDatabaseSizes(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	samples, err := srv.deps.Store.DatabaseSizeSamples(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if s.Database == "statev2" && s.SizeBytes > 0 {
			return
		}
	}
	t.Fatalf("statev2 size not recorded: %+v", samples)
}

func TestHistorySizeFailureDoesNotSuppressOtherDatabaseSamples(t *testing.T) {
	srv := newFixture(t)
	store, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	srv.deps.HistoryAnalytics = func() *HistoryAnalytics { return &HistoryAnalytics{Store: store} }
	if err := srv.collectDatabaseSizes(t.Context(), time.Now()); err == nil {
		t.Fatal("closed history size did not report error")
	}
	samples, err := srv.deps.Store.DatabaseSizeSamples(t.Context(), 0)
	if err != nil || len(samples) != 2 {
		t.Fatalf("other samples lost: %+v, %v", samples, err)
	}
}

func TestCollectDatabaseSizesWithoutOpenCode(t *testing.T) {
	srv := newFixture(t)
	srv.deps.DB = nil

	if err := srv.collectDatabaseSizes(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	samples, err := srv.deps.Store.DatabaseSizeSamples(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].Database != "ocman" {
		t.Fatalf("samples = %+v, want one ocman sample", samples)
	}
}

func TestDatabaseSizeCollectionWithoutStateDB(t *testing.T) {
	srv := newTestModule(Deps{})
	if err := srv.collectDatabaseSizes(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	srv.runDatabaseSizeLoop(context.Background())
}
