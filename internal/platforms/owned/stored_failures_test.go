package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type liveStoredCatalog struct {
	*nativePlatform
	response *platforms.DirectoryCatalog
	err      error
}

func (p *liveStoredCatalog) LiveDirectoryCatalog(context.Context, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	return p.response, p.err
}

func TestStoredCatalogDefaultsDoNotMutateSharedLiveConfiguration(t *testing.T) {
	a, p, _ := storedFixture(t)
	response := &platforms.DirectoryCatalog{DefaultModel: "live/default", Models: &platforms.SessionModelsResponse{SessionDefault: "live/default"}}
	source := &liveStoredCatalog{nativePlatform: p, response: response}
	a.native = source
	catalog, err := a.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"})
	if err != nil || catalog.DefaultModel != "p/model" || catalog.Models.SessionDefault != "p/model" || response.DefaultModel != "live/default" || response.Models.SessionDefault != "live/default" {
		t.Fatalf("stored defaults contaminated live catalogue: %+v %v", catalog, err)
	}
	source.err = errors.New("configuration failed")
	if _, err := a.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"}); err == nil {
		t.Fatal("live catalogue error ignored")
	}
	source.err = nil
	source.response = nil
	if catalog, err := a.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"}); err != nil || catalog != nil {
		t.Fatalf("nil catalogue: %+v %v", catalog, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := a.AgentCatalog(t.Context(), "native"); err == nil {
		t.Fatal("invalid catalogue session accepted")
	}
	if _, err := a.storedDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{}, "native"); err == nil {
		t.Fatal("invalid excluded identity accepted")
	}
	if _, _, err := a.storedCatalogHistory(t.Context(), "", "native"); err == nil {
		t.Fatal("invalid historical exclusion accepted")
	}
	for _, model := range []string{"p/not-in-recents", "unqualified"} {
		catalog := historicalCatalog(nil, db.SessionDefaults{Model: model})
		if len(catalog.Models.Models) != 1 || !catalog.Models.Models[0].IsSessionDefault || catalog.Models.Models[0].IsAvailable {
			t.Fatalf("retained default lost or became executable: %+v", catalog)
		}
	}
}

func TestStoredDetailReadsFailClosedAtEachUnavailableBoundary(t *testing.T) {
	a, p, id := storedFixture(t)
	if _, err := a.SessionChanges(t.Context(), id); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := a.SessionInfo(t.Context(), id); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	a.native = &storedProjector{nativePlatform: p, t: t}
	if _, err := a.SessionChanges(t.Context(), "native"); err == nil {
		t.Fatal("invalid identity reached change projector")
	}
	if _, err := a.SessionInfo(t.Context(), "native"); err == nil {
		t.Fatal("invalid identity reached info projector")
	}
	if _, err := a.Session(t.Context(), "native", 10, 0); err == nil {
		t.Fatal("invalid identity reached conversation reader")
	}
	key, err := a.storedKey(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.history.SessionState(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	s.ReadFormat = 0
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: s, ExpectedRevision: s.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Session(t.Context(), id, 10, 0); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
	_ = a.history.Close()
	for name, read := range map[string]func() error{
		"conversation": func() error { _, err := a.Session(t.Context(), id, 10, 0); return err },
		"catalogue": func() error {
			_, err := a.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{})
			return err
		},
	} {
		if err := read(); err == nil {
			t.Fatalf("closed store reached native %s", name)
		}
	}
}

// Before its first ingestion a source lists nothing, never native rows, and
// stays available through its integration.
func TestUningestedSourceListsEmptyWithoutNativeFallback(t *testing.T) {
	_, p, store := fixture(t)
	a, err := New(t.Context(), p, store, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Available(t.Context()) {
		t.Fatal("available integration hidden before ingestion")
	}
	if rows, err := a.Sessions(t.Context(), "", 0); err != nil || len(rows) != 0 {
		t.Fatalf("pre-ingestion list: %+v %v", rows, err)
	}
	if rows, err := a.SessionsInactiveBefore(t.Context(), 1<<62); err != nil || len(rows) != 0 {
		t.Fatalf("pre-ingestion archive inventory: %+v %v", rows, err)
	}
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SessionSummary(t.Context(), row.ID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("pre-ingestion summary: %v", err)
	}
	a.unavailable = errors.New("offline")
	if a.Available(t.Context()) {
		t.Fatal("offline source without history reported available")
	}
}
