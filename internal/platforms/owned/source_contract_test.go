package owned

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
)

// Discovery binds an unknown native ID only after the integration proves its
// source has it; binding metadata is never browsing content.
func TestCanonicalDiscoveryRequiresSourceProof(t *testing.T) {
	a, p, store := fixture(t)
	a.native = struct{ platforms.Integration }{p}
	if _, err := a.canonical(t.Context(), "native"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("integration without a probe acquired identity: %v", err)
	}
	a.native = p
	if _, err := a.canonical(t.Context(), "foreign"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("unproven native ID acquired identity: %v", err)
	}
	created, err := a.canonical(t.Context(), "native")
	if err != nil || created.NativeID != "native" || created.Directory != "/repo" || !sessioncatalog.IsID(created.ID) {
		t.Fatalf("proven discovery failed: %+v %v", created, err)
	}
	p.rows = nil
	again, err := a.canonical(t.Context(), "native")
	if err != nil || again.ID != created.ID {
		t.Fatalf("bound identity needed another probe: %+v %v", again, err)
	}
	if err := store.Scrub(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, "native"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.canonical(t.Context(), "native"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("discovery reactivated tombstone: %v", err)
	}
	if _, err := a.canonical(t.Context(), ""); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCanonicalSourceInitializationAndDriftFenceAllHistoryReaders(t *testing.T) {
	a, p, store := fixture(t)
	history := a.history
	for _, tc := range []struct {
		native  platforms.Integration
		store   *sessioncatalog.Store
		history *statev2.DB
		owner   string
	}{
		{nil, store, history, "owner"}, {p, nil, history, "owner"}, {p, store, nil, "owner"}, {p, store, history, ""},
	} {
		if _, err := New(t.Context(), tc.native, tc.store, tc.history, tc.owner); !errors.Is(err, sessioncatalog.ErrInvalid) {
			t.Fatalf("invalid boundary initialized: %v", err)
		}
	}
	if _, err := New(t.Context(), struct{ platforms.Integration }{p}, store, history, "owner"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("missing physical source initialized: %v", err)
	}
	p.source = ""
	if _, err := New(t.Context(), p, store, history, "owner"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("empty physical source initialized: %v", err)
	}
	p.source = "source"
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	h := &historyPlatform{nativePlatform: p}
	a.native = h
	p.source = "drifted"
	for name, read := range map[string]func() error{
		"identity":  func() error { _, err := a.HistoryIdentity(t.Context()); return err },
		"inventory": func() error { _, err := a.HistoryInventory(t.Context()); return err },
		"snapshot":  func() error { _, err := a.HistorySnapshot(t.Context(), row.ID); return err },
		"sequences": func() error { _, err := a.HistorySequences(t.Context()); return err },
		"messages":  func() error { _, err := a.HistoryMessages(t.Context(), row.ID, nil); return err },
		"watch": func() error {
			return a.WatchHistory(t.Context(), func() {}, func(platforms.HistoryChange) { t.Fatal("drifted watcher published") })
		},
		"history allocation": func() error { _, err := a.historyIdentity(t.Context(), "native"); return err },
		"session allocation": func() error { _, err := a.canonical(t.Context(), "native"); return err },
		"inventory import":   func() error { return a.importRows(t.Context(), p.rows) },
		"creation commit":    func() error { return a.CommitCreation(t.Context(), row.ID) },
	} {
		if err := read(); !errors.Is(err, platforms.ErrHistoryUnavailable) {
			t.Errorf("%s crossed source: %v", name, err)
		}
	}
	p.source = "source"
	a.native = p
	for name, read := range map[string]func() error{
		"inventory": func() error { _, err := a.HistoryInventory(t.Context()); return err },
		"snapshot":  func() error { _, err := a.HistorySnapshot(t.Context(), row.ID); return err },
		"sequences": func() error { _, err := a.HistorySequences(t.Context()); return err },
		"messages":  func() error { _, err := a.HistoryMessages(t.Context(), row.ID, nil); return err },
		"watch":     func() error { return a.WatchHistory(t.Context(), func() {}, func(platforms.HistoryChange) {}) },
	} {
		if err := read(); !errors.Is(err, platforms.ErrUnsupported) {
			t.Errorf("%s invented native support: %v", name, err)
		}
	}
	if _, err := a.historyIdentity(t.Context(), ""); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatal(err)
	}
}
