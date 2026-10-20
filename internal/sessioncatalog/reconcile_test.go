package sessioncatalog

import (
	"errors"
	"testing"
)

func catalogSource() Source {
	b := binding()
	return Source{OwnerID: b.OwnerID, Platform: b.Platform, SourceKey: b.SourceKey}
}

func nativeBinding(id string) Binding { b := binding(); b.NativeID = id; return b }

func TestReconcileAssignsStableIDsAndRepairsLateParents(t *testing.T) {
	s, _ := openStore(t)
	child := NativeSession{ID: "child", ParentID: "parent", Directory: "/repo", Title: "child"}
	result, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{child})
	if err != nil || result.Created != 1 {
		t.Fatalf("child import: %+v %v", result, err)
	}
	before, err := s.FindBinding(t.Context(), nativeBinding("child"))
	if err != nil || before.ParentID == "" || before.NativeParentID != "parent" {
		t.Fatalf("missing parent not retained: %+v %v", before, err)
	}
	parent := NativeSession{ID: "parent", Directory: "/repo", Title: "parent", Origin: Routine, OriginID: "routine"}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{parent}); err != nil {
		t.Fatal(err)
	}
	after, err := s.FindBinding(t.Context(), nativeBinding("child"))
	if err != nil || after.ID != before.ID {
		t.Fatalf("child identity changed: %+v %v", after, err)
	}
	root, err := s.FindBinding(t.Context(), nativeBinding("parent"))
	if err != nil || after.ParentID != root.ID || root.OriginID != "routine" {
		t.Fatalf("late parent not linked: %+v %+v %v", root, after, err)
	}
	result, err = s.Reconcile(t.Context(), catalogSource(), []NativeSession{child, parent})
	if err != nil || result.Created != 0 {
		t.Fatalf("replay allocated identities: %+v %v", result, err)
	}
	child.ParentID = ""
	child.Title = "renamed"
	child.Directory = "/moved"
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{child}); err != nil {
		t.Fatal(err)
	}
	after, err = s.FindBinding(t.Context(), nativeBinding("child"))
	if err != nil || after.ParentID != "" || after.NativeParentID != "" || after.Title != "renamed" || after.Directory != "/moved" {
		t.Fatalf("metadata not refreshed: %+v %v", after, err)
	}
	// Absence is not authority to remove a previously imported identity.
	if _, err := s.Reconcile(t.Context(), catalogSource(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), root.ID); err != nil {
		t.Fatal(err)
	}
}

func TestReconcilePreservesReservedOriginsAndDefersUnknownNativeRows(t *testing.T) {
	s, _ := openStore(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	metadata := NativeSession{ID: binding().NativeID, Directory: reserved.Directory, Title: "native title"}
	result, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata})
	if err != nil || result.Created != 0 || result.Deferred != 1 {
		t.Fatalf("early announcement split identity: %+v %v", result, err)
	}
	if _, err := s.Attach(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(t.Context(), reserved.ID)
	if err != nil || row.CreationState != Configuring || row.Title != reserved.Title || row.Origin != Routine {
		t.Fatalf("discovery completed configuration: %+v %v", row, err)
	}
	if _, err := s.Bind(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	row, err = s.Get(t.Context(), reserved.ID)
	if err != nil || row.Title != metadata.Title || row.OriginID != reserved.OriginID {
		t.Fatalf("discovery erased intent: %+v %v", row, err)
	}
}

func TestReconcileBackfillsKnownOriginsWithoutOverwritingCreatedOrigins(t *testing.T) {
	s, _ := openStore(t)
	metadata := NativeSession{ID: "old", Directory: "/repo"}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	before, err := s.FindBinding(t.Context(), nativeBinding("old"))
	if err != nil {
		t.Fatal(err)
	}
	metadata.Origin, metadata.OriginID = Factory, "attempt"
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	after, err := s.FindBinding(t.Context(), nativeBinding("old"))
	if err != nil || after.ID != before.ID || after.Origin != Factory || after.OriginID != "attempt" {
		t.Fatalf("backfill lost origin: %+v %v", after, err)
	}
	metadata.Origin, metadata.OriginID = Routine, "other"
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{metadata}); err != nil {
		t.Fatal(err)
	}
	after, err = s.FindBinding(t.Context(), nativeBinding("old"))
	if err != nil || after.Origin != Factory || after.OriginID != "attempt" {
		t.Fatalf("origin rebound: %+v %v", after, err)
	}
}

func TestReconcileRejectsCyclesAtomically(t *testing.T) {
	s, _ := openStore(t)
	_, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "a", ParentID: "b"}, {ID: "b", ParentID: "a"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle accepted: %v", err)
	}
	if _, err := s.FindBinding(t.Context(), nativeBinding("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial cycle committed: %v", err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "a", ParentID: "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "b", ParentID: "a"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("late cycle accepted: %v", err)
	}
}

func TestReconcileAllowsEmptyHistoricalDirectoryAndSeparatesSources(t *testing.T) {
	s, _ := openStore(t)
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "same"}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.FindBinding(t.Context(), nativeBinding("same"))
	if err != nil {
		t.Fatal(err)
	}
	other := catalogSource()
	other.SourceKey = "other"
	if _, err := s.Reconcile(t.Context(), other, []NativeSession{{ID: "same"}}); err != nil {
		t.Fatal(err)
	}
	b := nativeBinding("same")
	b.SourceKey = other.SourceKey
	second, err := s.FindBinding(t.Context(), b)
	if err != nil || first.ID == second.ID {
		t.Fatalf("sources merged: %+v %+v %v", first, second, err)
	}
}
