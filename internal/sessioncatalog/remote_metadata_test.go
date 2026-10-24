package sessioncatalog

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func remoteRow() Session {
	return Session{ID: "ocm_" + uuid.NewString(), OwnerID: "remote", Platform: "opencode", SourceKey: "remote-source", Origin: External, CreationState: Ready, CreatedAt: 1, UpdatedAt: 2}
}

func TestCanonicalRemoteMetadataNeverInstallsAnExecutionBinding(t *testing.T) {
	store, _ := openStore(t)
	row := remoteRow()
	if err := store.MirrorCanonical(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	actual, err := store.GetCanonical(t.Context(), row.ID)
	if err != nil || actual != row {
		t.Fatalf("remote metadata changed: %+v %v", actual, err)
	}
	if _, err := store.Get(t.Context(), row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remote identity acquired local binding: %v", err)
	}
	for _, mutate := range []func(*Session){func(r *Session) { r.OwnerID = "other" }, func(r *Session) { r.Platform = "claudecode" }, func(r *Session) { r.SourceKey = "changed" }} {
		conflict := row
		mutate(&conflict)
		if err := store.MirrorCanonical(t.Context(), conflict); !errors.Is(err, ErrBindingConflict) {
			t.Fatalf("remote identity rebound: %v", err)
		}
	}
	row.Purged, row.UpdatedAt = true, 3
	if err := store.MirrorCanonical(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	row.Purged, row.UpdatedAt = false, 4
	if err := store.MirrorCanonical(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	actual, err = store.GetCanonical(t.Context(), row.ID)
	if err != nil || !actual.Purged {
		t.Fatalf("remote purge tombstone lost: %+v %v", actual, err)
	}
}

func TestCanonicalRemoteMetadataRejectsNativeFieldsAndForeignParentLinks(t *testing.T) {
	store, _ := openStore(t)
	row := remoteRow()
	row.NativeID = "native"
	if err := store.MirrorCanonical(t.Context(), row); !errors.Is(err, ErrInvalid) {
		t.Fatalf("native remote identity accepted: %v", err)
	}
	row.NativeID = ""
	parent := remoteRow()
	row.ParentID = parent.ID
	if err := store.MirrorCanonical(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	parent.OwnerID = "different-owner"
	if err := store.MirrorCanonical(t.Context(), parent); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("late parent crossed owners: %v", err)
	}
	parent.OwnerID = row.OwnerID
	parent.ParentID = row.ID
	if err := store.MirrorCanonical(t.Context(), parent); !errors.Is(err, ErrInvalid) {
		t.Fatalf("remote parent cycle accepted: %v", err)
	}
	if _, err := store.GetCanonical(t.Context(), parent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed mirror committed partial parent: %v", err)
	}
}
