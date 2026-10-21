package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestStoredHooksRouteInvalidationAndWaitsThroughIngestion(t *testing.T) {
	a, _, id := storedFixture(t)
	var notified, refreshed string
	a.BindStoredHistory(platforms.StoredHistoryHooks{
		Notify: func(id string) { notified = id },
		Refresh: func(ctx context.Context, id string) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded refresh")
			}
			refreshed = id
			return nil
		},
		Await: func(ctx context.Context, id string) bool {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded first read")
			}
			key, err := a.storedKey(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			s, _, err := a.history.SessionState(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			s.ReadFormat = statev2.ConversationReadFormat
			if _, err := a.history.Commit(ctx, statev2.Batch{Session: s, ExpectedRevision: s.Revision}); err != nil {
				t.Fatal(err)
			}
			return true
		},
	})
	a.MarkSessionDirty(id)
	if err := a.RefreshSession(t.Context(), id); err != nil || notified != id || refreshed != id {
		t.Fatalf("hooks bypassed ingestion: %s %s %v", notified, refreshed, err)
	}
	key, err := a.storedKey(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := a.history.SessionState(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	s.ReadFormat = 0
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: s, ExpectedRevision: s.Revision}); err != nil {
		t.Fatal(err)
	}
	if detail, err := a.Session(t.Context(), id, 10, 0); err != nil || len(detail.Messages) != 2 {
		t.Fatalf("wait did not repair initial format: %+v %v", detail, err)
	}
	a.BindStoredHistory(platforms.StoredHistoryHooks{})
	if err := a.RefreshSession(t.Context(), id); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
	if a.waitStored(t.Context(), id) {
		t.Fatal("missing waiter accepted")
	}
	if err := a.RefreshSession(t.Context(), "native"); err == nil {
		t.Fatal("invalid refresh identity accepted")
	}
	if err := a.history.Purge(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Session(t.Context(), id, 10, 0); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := a.SessionMetadata(t.Context(), id); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
}

type storedHaltSource struct {
	*nativePlatform
	public, native func(context.Context, string) (string, error)
}

func (p *storedHaltSource) SetSessionHaltIdentity(public, native func(context.Context, string) (string, error)) {
	p.public, p.native = public, native
}

func TestStoredHaltCallbacksNeverAcceptNativeOrForeignReceiptKeys(t *testing.T) {
	a, p, id := storedFixture(t)
	source := &storedHaltSource{nativePlatform: p}
	a.native = source
	a.bindStoredHaltIdentity()
	if public, err := source.public(t.Context(), "native"); err != nil || public != id {
		t.Fatalf("public halt identity: %s %v", public, err)
	}
	if native, err := source.native(t.Context(), id); err != nil || native != "native" {
		t.Fatalf("native halt identity: %s %v", native, err)
	}
	if native, err := source.native(t.Context(), "native"); err != nil || native != "" {
		t.Fatalf("unscoped native halt key leaked: %s %v", native, err)
	}
	if err := a.store.RetainSourceLegacyKeys(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}); err != nil {
		t.Fatal(err)
	}
	if public, err := source.public(t.Context(), "native"); err != nil || public != "native" {
		t.Fatalf("cutover lost frozen legacy receipt key: %s %v", public, err)
	}
	if native, err := source.native(t.Context(), "native"); err != nil || native != "native" {
		t.Fatalf("cutover lost legacy halt: %s %v", native, err)
	}
	a.unavailable = errors.New("offline")
	if _, err := source.public(t.Context(), "native"); err == nil {
		t.Fatal("offline prompt published new receipt")
	}
}
