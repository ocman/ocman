package sessioncatalog

import (
	"errors"
	"testing"
)

func TestScrubKeepsIdentityAndRejectsStaleDisplayMetadata(t *testing.T) {
	s, _ := openStore(t)
	metadata := NativeSession{ID: "session", Directory: "/private", Title: "private"}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	before, err := s.FindBinding(t.Context(), nativeBinding("session"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Scrub(t.Context(), catalogSource(), "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(t.Context(), before.ID)
	if err != nil || !after.Purged || after.Title != "" || after.Directory != "" || after.NativeID != before.NativeID {
		t.Fatalf("purged metadata restored: %+v %v", after, err)
	}
	if err := s.Scrub(t.Context(), Source{}, "session"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestScrubDefersUnboundNativeIdentity(t *testing.T) {
	s, _ := openStore(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Scrub(t.Context(), catalogSource(), binding().NativeID); !errors.Is(err, ErrCreating) {
		t.Fatalf("purge split unfinished identity: %v", err)
	}
	if _, err := s.Attach(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if err := s.Scrub(t.Context(), catalogSource(), binding().NativeID); err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(t.Context(), reserved.ID)
	if err != nil || !row.Purged || row.NativeID != binding().NativeID {
		t.Fatalf("purge lost native binding: %+v %v", row, err)
	}
}
