package sessioncatalog

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

func metadataHistory(t *testing.T) *statev2.DB {
	t.Helper()
	h, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	source := catalogSource()
	if err := h.RegisterInstance(t.Context(), statev2.Instance{ID: "history", OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey}); err != nil {
		t.Fatal(err)
	}
	return h
}

func writeMetadata(t *testing.T, h *statev2.DB, id, parent, directory string) {
	t.Helper()
	if _, err := h.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "history", SessionID: id}, ParentID: parent, Directory: directory, Title: id}}); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogueModuleBackfillsPagesAndMaintainsLateParents(t *testing.T) {
	s, _ := openStore(t)
	h := metadataHistory(t)
	writeMetadata(t, h, "000-child", "zzz-parent", "/repo")
	for i := range 500 {
		writeMetadata(t, h, fmt.Sprintf("middle-%03d", i), "", "/repo")
	}
	writeMetadata(t, h, "zzz-parent", "", "/repo")
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	child, err := s.FindBinding(t.Context(), nativeBinding("000-child"))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.FindBinding(t.Context(), nativeBinding("zzz-parent"))
	if err != nil || child.ParentID != parent.ID {
		t.Fatalf("pagination lost parent link: %+v %+v %v", child, parent, err)
	}
	before := child.ID
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	child, err = s.FindBinding(t.Context(), nativeBinding("000-child"))
	if err != nil || child.ID != before {
		t.Fatalf("restart reassigned identity: %+v %v", child, err)
	}
	writeMetadata(t, h, "new", "zzz-parent", "/repo")
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := s.FindBinding(t.Context(), nativeBinding("new"))
	if err != nil || row.ParentID != parent.ID {
		t.Fatalf("change stream did not discover session: %+v %v", row, err)
	}
}

type failingMetadata struct {
	*statev2.DB
	fail bool
}

func TestCatalogueModuleRejectsMissingDependenciesAndPrepareFailure(t *testing.T) {
	if err := NewModule(Deps{}).Start(t.Context()); err == nil {
		t.Fatal("missing index dependencies accepted")
	}
	s, _ := openStore(t)
	h := metadataHistory(t)
	want := errors.New("legacy provenance unavailable")
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID, Prepare: func(context.Context) error { return want }})
	if err := m.Start(t.Context()); !errors.Is(err, want) {
		t.Fatalf("prepare failure ignored: %v", err)
	}
}

func TestCatalogueModuleDoesNotIndexAnotherOwner(t *testing.T) {
	s, _ := openStore(t)
	h := metadataHistory(t)
	if err := h.RegisterInstance(t.Context(), statev2.Instance{ID: "foreign", OwnerID: "foreign", Platform: "opencode", SourceKey: "private"}); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "foreign", SessionID: "secret"}, Title: "private"}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(t.Context(), "foreign", 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("foreign metadata indexed on hub: %+v %v", rows, err)
	}
}

func (h *failingMetadata) MetadataPage(ctx context.Context, owner string, after statev2.Key, limit int) ([]statev2.SessionMetadata, error) {
	if h.fail {
		return nil, errors.New("temporarily unavailable")
	}
	return h.DB.MetadataPage(ctx, owner, after, limit)
}

func TestCatalogueBootstrapCanRetryFailedInventory(t *testing.T) {
	s, _ := openStore(t)
	h := &failingMetadata{DB: metadataHistory(t)}
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeMetadata(t, h.DB, "late", "", "/repo")
	h.fail = true
	if err := m.Start(t.Context()); err == nil {
		t.Fatal("failed inventory ignored")
	}
	h.fail = false
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindBinding(t.Context(), nativeBinding("late")); err != nil {
		t.Fatal(err)
	}
}
