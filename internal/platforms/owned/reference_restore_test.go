package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/google/uuid"
)

type restoringRemote struct {
	platforms.Platform
	result map[string]platforms.SessionIdentity
	calls  int
	err    error
}

func (*restoringRemote) ID() platforms.ID          { return "r-owner:test" }
func (*restoringRemote) CanonicalSessionIDs() bool { return true }
func (p *restoringRemote) ResolveLegacyReferences(_ context.Context, refs []string) (map[string]platforms.SessionIdentity, error) {
	p.calls++
	return p.result, p.err
}

func restoreFixture(t *testing.T) (*state.DB, *ReferenceStore, *restoringRemote, *IdentityGateway) {
	t.Helper()
	db, err := state.Open(statetest.Path(t, ReferenceSchema, sessioncatalog.Schema), ReferenceSchema, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	refs, err := NewReferences(db)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	remote := &restoringRemote{}
	gateway := NewIdentityGateway(catalogue, "hub", nil, func(_ context.Context, scope string) (platforms.Platform, error) {
		if scope != string(remote.ID()) {
			return nil, platforms.ErrPlatformUnreachable
		}
		return remote, nil
	}, func(string) error { return sessioncatalog.ErrInvalid })
	return db, refs, remote, gateway
}

func TestRestoreQuarantinedStateUsesAuthenticatedOwnerAndPreservesQueuedWork(t *testing.T) {
	db, refs, remote, gateway := restoreFixture(t)
	scope := string(remote.ID())
	if err := db.PinSession(t.Context(), scope, "native"); err != nil {
		t.Fatal(err)
	}
	if err := db.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "queue", Platform: scope, SessionID: "native", Text: "retained"}); err != nil {
		t.Fatal(err)
	}
	if err := refs.QuarantineState(t.Context(), db, scope); err != nil {
		t.Fatal(err)
	}
	remote.err = platforms.ErrPlatformUnreachable
	if err := refs.RestoreState(t.Context(), db, gateway, scope); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("offline owner: %v", err)
	}
	page, err := refs.referencePage(t.Context(), scope, "")
	if err != nil || len(page) != 1 || page[0].CanonicalID != "" {
		t.Fatalf("offline owner gained identity: %+v %v", page, err)
	}
	id := "ocm_" + uuid.NewString()
	remote.err = nil
	remote.result = map[string]platforms.SessionIdentity{"native": {ID: id, OwnerID: "owner", Platform: "test", SourceKey: "source", CreationState: string(sessioncatalog.Ready), Origin: string(sessioncatalog.External)}}
	if err := refs.RestoreState(t.Context(), db, gateway, scope); err != nil {
		t.Fatal(err)
	}
	queue, err := db.ListQueuedMessages(t.Context(), scope, id)
	if err != nil || len(queue) != 1 || queue[0].ID != "queue" || queue[0].Text != "retained" {
		t.Fatalf("restoration lost queue: %+v %v", queue, err)
	}
	calls := remote.calls
	remote.err = platforms.ErrPlatformUnreachable
	if err := refs.RestoreState(t.Context(), db, gateway, scope); err != nil || remote.calls != calls {
		t.Fatalf("bound reference contacted owner again: %v", err)
	}
	if err := refs.RestoreState(t.Context(), db, gateway, ""); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("unattributed state gained owner: %v", err)
	}
}

func TestQuarantineRestorationRejectsUnrelatedOwnersAndNativeResponses(t *testing.T) {
	for _, info := range []platforms.SessionIdentity{
		{ID: "native", OwnerID: "owner", Platform: "test"},
		{ID: "ocm_" + uuid.NewString(), OwnerID: "other", Platform: "test"},
		{ID: "ocm_" + uuid.NewString(), OwnerID: "owner", Platform: "test", NativeID: "native"},
	} {
		db, refs, remote, gateway := restoreFixture(t)
		if err := db.PinSession(t.Context(), string(remote.ID()), "native"); err != nil {
			t.Fatal(err)
		}
		if err := refs.QuarantineState(t.Context(), db, string(remote.ID())); err != nil {
			t.Fatal(err)
		}
		remote.result = map[string]platforms.SessionIdentity{"native": info}
		if err := refs.RestoreState(t.Context(), db, gateway, string(remote.ID())); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
			t.Fatalf("untrusted response accepted: %+v %v", info, err)
		}
		page, err := refs.referencePage(t.Context(), string(remote.ID()), "")
		if err != nil || len(page) != 1 || page[0].CanonicalID != "" {
			t.Fatalf("untrusted response bound state: %+v %v", page, err)
		}
	}
}
