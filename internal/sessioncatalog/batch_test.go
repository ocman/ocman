package sessioncatalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestGetManyMatchesIndividualLookups(t *testing.T) {
	s, _ := openStore(t)
	source := catalogSource()
	if _, err := s.Reconcile(t.Context(), source, []NativeSession{{ID: "one", Directory: "/repo"}, {ID: "two", ParentID: "one", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.FindBinding(t.Context(), Binding{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.FindBinding(t.Context(), Binding{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: "two"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMany(t.Context(), []string{first.ID, second.ID, first.ID, "missing"})
	if err != nil || !reflect.DeepEqual(got, map[string]Session{first.ID: first, second.ID: second}) {
		t.Fatalf("batch differs from individual reads: %+v %v", got, err)
	}
	if got, err := s.GetMany(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("empty batch: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ids := range [][]string{nil, {first.ID}} {
		if _, err := s.GetMany(ctx, ids); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled batch: %v", err)
		}
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMany(t.Context(), []string{first.ID}); err == nil {
		t.Fatal("closed catalog accepted a batch")
	}
}
