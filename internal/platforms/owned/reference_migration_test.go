package owned

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestOfflineReferenceQuarantineRetainsRecordsWithoutMintingRemoteSessionIDs(t *testing.T) {
	db := statetest.Open(t, ReferenceSchema)
	store, err := NewReferences(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"r-offline:test", "r-other:test"} {
		if err := db.PinSession(t.Context(), scope, "same-native"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "queue", Platform: "r-offline:test", SessionID: "same-native", Text: "retained"}); err != nil {
		t.Fatal(err)
	}
	if err := store.QuarantineState(t.Context(), db, "r-offline:test"); err != nil {
		t.Fatal(err)
	}
	refs, err := db.SessionReferences(t.Context(), "r-offline:test")
	if err != nil || len(refs) != 1 || !identity.IsPendingReference(refs[0]) || sessioncatalog.IsID(refs[0]) {
		t.Fatalf("hub minted a remote session instead of a reference: %+v %v", refs, err)
	}
	ref := refs[0]
	row, err := store.get(t.Context(), ref)
	if err != nil || row.NativeID != "same-native" || row.CanonicalID != "" {
		t.Fatalf("compatibility association lost: %+v %v", row, err)
	}
	queue, err := db.ListQueuedMessages(t.Context(), "r-offline:test", ref)
	if err != nil || len(queue) != 1 || queue[0].ID != "queue" || queue[0].Text != "retained" {
		t.Fatalf("quarantine discarded queued work: %+v %v", queue, err)
	}
	if err := store.QuarantineState(t.Context(), db, "r-offline:test"); err != nil {
		t.Fatal(err)
	}
	canonical := "ocm_" + uuid.NewString()
	if err := store.bind(t.Context(), "r-other:test", ref, canonical); err == nil {
		t.Fatal("unrelated owner bound pending reference")
	}
	if err := store.bind(t.Context(), "r-offline:test", ref, canonical); err != nil {
		t.Fatal(err)
	}
	if err := store.bind(t.Context(), "r-offline:test", ref, "ocm_"+uuid.NewString()); err == nil {
		t.Fatal("reference was rebound to a different session")
	}
	if err := store.QuarantineState(t.Context(), db, "r-offline:test"); err != nil {
		t.Fatal(err)
	}
	refs, err = db.SessionReferences(t.Context(), "r-offline:test")
	if err != nil || len(refs) != 1 || refs[0] != canonical {
		t.Fatalf("owner binding did not replace pending core references: %+v %v", refs, err)
	}
	foreign, err := db.SessionReferences(t.Context(), "r-other:test")
	if err != nil || len(foreign) != 1 || foreign[0] != "same-native" {
		t.Fatalf("another owner was adopted: %+v %v", foreign, err)
	}
}

func TestUnattributedLegacyStateIsQuarantinedWithoutGuessingLocalOwner(t *testing.T) {
	db := statetest.Open(t, ReferenceSchema)
	store, err := NewReferences(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PinSession(t.Context(), "", "unattributed-native"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO inbox_item(id,title,body,created_at,session_json) VALUES('legacy-notice','retained','retained',1,'{"sessionId":"unattributed-native"}')`); err != nil {
		t.Fatal(err)
	}
	scopes, err := db.SessionReferenceScopes(t.Context())
	if err != nil || len(scopes) != 1 || scopes[0] != "" {
		t.Fatalf("unknown scope was inferred: %+v %v", scopes, err)
	}
	if err := store.QuarantineState(t.Context(), db, ""); err != nil {
		t.Fatal(err)
	}
	refs, err := db.UnattributedSessionReferences(t.Context())
	if err != nil || len(refs) != 1 || !identity.IsPendingReference(refs[0]) {
		t.Fatalf("unknown association retained native core keys: %+v %v", refs, err)
	}
	if err := store.bind(t.Context(), "", refs[0], "ocm_"+uuid.NewString()); err == nil {
		t.Fatal("unknown owner acquired execution identity")
	}
	if err := db.QuarantineUnattributedSessionReferences(t.Context(), map[string]string{refs[0]: "ocm_" + uuid.NewString()}); err == nil {
		t.Fatal("core assigned unattributed reference to a session")
	}
}
