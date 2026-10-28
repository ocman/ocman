package owned

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type deferredHistoryProbe struct {
	*historyPlatform
	metadata *db.Session
	err      error
}

func (p *deferredHistoryProbe) SessionIdentityMetadata(context.Context, string) (*db.Session, error) {
	return p.metadata, p.err
}

func TestDeferredHistoryMetadataProbeFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata *db.Session
		probeErr error
		want     error
	}{
		{name: "missing", probeErr: platforms.ErrNotFound, want: sessioncatalog.ErrCreating},
		{name: "unavailable", probeErr: platforms.ErrHistoryUnavailable, want: platforms.ErrHistoryUnavailable},
		{name: "nil metadata", want: sessioncatalog.ErrInvalid},
		{name: "foreign metadata", metadata: &db.Session{ID: "foreign"}, want: sessioncatalog.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, p := historyFixture(t)
			a.native = &deferredHistoryProbe{historyPlatform: p, metadata: tc.metadata, err: tc.probeErr}
			if _, err := a.resolveDeferredHistoryIdentity(t.Context(), "hidden-child"); !errors.Is(err, tc.want) {
				t.Fatalf("metadata probe: got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHistoryDiscoveryDoesNotDeferSessionsOlderThanPendingCreation(t *testing.T) {
	for _, mode := range []string{"inventory", "sequences"} {
		t.Run(mode, func(t *testing.T) {
			a, p := historyFixture(t)
			p.rows = []db.Session{{ID: "hidden-child", Directory: "/repo", TimeCreated: time.Now().Add(-time.Hour).UnixMilli()}}
			_, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "inventory" {
				entries, err := a.HistoryInventory(t.Context())
				if err != nil || len(entries) != 1 {
					t.Fatalf("historical session blocks analytics readiness: %+v %v", entries, err)
				}
			} else {
				sequences, err := a.HistorySequences(t.Context())
				if err != nil || len(sequences) != 1 {
					t.Fatalf("historical session blocks analytics readiness: %+v %v", sequences, err)
				}
			}
		})
	}
}

func TestHistoryDiscoveryStillDefersPossibleCreationResult(t *testing.T) {
	a, p := historyFixture(t)
	p.rows = []db.Session{{ID: "hidden-child", Directory: "/repo", TimeCreated: time.Now().Add(time.Hour).UnixMilli()}}
	_, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := a.HistoryInventory(t.Context())
	var deferred *platforms.HistoryDeferred
	if !errors.As(err, &deferred) || deferred.Count != 1 || len(entries) != 0 {
		t.Fatalf("possible creation result escaped reservation fence: %+v %v", entries, err)
	}
	if _, err := a.store.FindBinding(t.Context(), a.binding("hidden-child")); !errors.Is(err, sessioncatalog.ErrNotFound) {
		t.Fatalf("discovery stole pending creation identity: %v", err)
	}
}

func TestHistoryDiscoveryKeepsReadableSessionsDuringPendingCreation(t *testing.T) {
	a, p := historyFixture(t)
	if _, err := a.historyIdentity(t.Context(), "native"); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	p.entries = []db.HistoryEntry{{ID: "native", Version: "ready"}, {ID: "unknown-during-create"}}
	p.sequence = map[string]int64{"native": 1, "unknown-during-create": 2}
	entries, err := a.HistoryInventory(t.Context())
	if err == nil || len(entries) != 1 || entries[0].Version != "ready" {
		t.Fatalf("pending creation blocked unrelated inventory: %+v %v", entries, err)
	}
	sequences, err := a.HistorySequences(t.Context())
	if err == nil || len(sequences) != 1 || sequences[entries[0].ID] != 1 {
		t.Fatalf("pending creation blocked unrelated sequences: %+v %v", sequences, err)
	}
}
