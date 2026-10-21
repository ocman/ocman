package owned

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type runtimeRecoveryNative struct {
	*nativePlatform
	identities []db.Session
	seen       string
}

func (p *runtimeRecoveryNative) GetSessionIdentities(context.Context) ([]db.Session, error) {
	return p.identities, nil
}
func (p *runtimeRecoveryNative) GetSessionLifecycle(_ context.Context, id string) (db.SessionLifecycle, error) {
	p.seen = id
	return db.SessionLifecycle{LatestMessageID: "native-message", LatestMessageFinish: "tool-calls", Status: db.StatusBusy}, nil
}
func (p *runtimeRecoveryNative) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	for _, row := range p.identities {
		if row.ID == id {
			return &row, nil
		}
	}
	return nil, platforms.ErrNotFound
}

func TestRuntimeRecoveryIncludesHiddenRowsAndKeepsNativeMessageEvidence(t *testing.T) {
	a, p, catalogue := fixture(t)
	native := &runtimeRecoveryNative{nativePlatform: p, identities: []db.Session{{ID: "native", Directory: "/repo", Title: "preserved title"}, {ID: "hidden", ParentID: "native", Directory: "/repo"}}}
	a.native = native
	rows, err := a.GetSessionIdentities(t.Context())
	if err != nil || len(rows) != 2 || !identity.IsSessionID(rows[0].ID) || rows[1].ParentID != rows[0].ID {
		t.Fatalf("runtime identity inventory: %+v %v", rows, err)
	}
	if lifecycle, err := a.GetSessionLifecycle(t.Context(), rows[1].ID); err != nil || native.seen != "hidden" || lifecycle.LatestMessageID != "native-message" || lifecycle.LatestMessageFinish != "tool-calls" {
		t.Fatalf("persisted native evidence changed: %+v %v", lifecycle, err)
	}
	if _, err := a.GetSessionLifecycle(t.Context(), "hidden"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("raw runtime reference accepted: %v", err)
	}
	if err := catalogue.Scrub(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, "hidden"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetSessionLifecycle(t.Context(), rows[1].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("purged runtime evidence reactivated: %v", err)
	}
	rows, err = a.GetSessionIdentities(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("purged identity returned as recovery candidate: %+v %v", rows, err)
	}
	root, err := catalogue.Get(t.Context(), rows[0].ID)
	if err != nil || root.Title != "preserved title" {
		t.Fatalf("minimal runtime inventory erased catalogue metadata: %+v %v", root, err)
	}
}

func TestRuntimeInventoryFailsClosedOnUnattributedInFlightCreation(t *testing.T) {
	a, p, catalogue := fixture(t)
	native := &runtimeRecoveryNative{nativePlatform: p, identities: []db.Session{{ID: "unattached", Directory: "/repo"}}}
	a.native = native
	if _, _, err := catalogue.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if rows, err := a.GetSessionIdentities(t.Context()); !errors.Is(err, sessioncatalog.ErrCreating) || rows != nil {
		t.Fatalf("unattributed creation disappeared from stop fence: %+v %v", rows, err)
	}
}
