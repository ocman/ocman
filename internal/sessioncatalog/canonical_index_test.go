package sessioncatalog

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestLegacyIndexerDoesNotReadoptCanonicalHistoryKeys(t *testing.T) {
	s, _ := openStore(t)
	source := catalogSource()
	if _, err := s.Reconcile(t.Context(), source, []NativeSession{{ID: "native", Title: "original"}}); err != nil {
		t.Fatal(err)
	}
	bound, err := s.FindBinding(t.Context(), nativeBinding("native"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: s, OwnerID: source.OwnerID})
	metadata := statev2.SessionMetadata{Instance: statev2.Instance{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey}, Session: statev2.Session{Key: statev2.Key{SessionID: bound.ID}, Title: "canonical history"}}
	if err := m.project(t.Context(), []statev2.SessionMetadata{metadata}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.List(t.Context(), source.OwnerID, 500, 0); err != nil || len(rows) != 1 {
		t.Fatalf("canonical history was adopted as another native session: %+v %v", rows, err)
	}
	if _, err := s.FindBinding(t.Context(), nativeBinding(bound.ID)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canonical ID installed as a native execution key: %v", err)
	}
	metadata.Instance.SourceKey = "wrong-source"
	if err := m.project(t.Context(), []statev2.SessionMetadata{metadata}); !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("foreign canonical history was accepted: %v", err)
	}
}
