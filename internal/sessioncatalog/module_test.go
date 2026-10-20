package sessioncatalog

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
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
	if _, err := m.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := s.FindBinding(t.Context(), nativeBinding("new"))
	if err != nil || row.ParentID != parent.ID {
		t.Fatalf("change stream did not discover session: %+v %v", row, err)
	}
	if err := h.Purge(t.Context(), statev2.Key{InstanceID: "history", SessionID: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if retained, err := s.Get(t.Context(), row.ID); err != nil || retained.NativeID != "new" || !retained.Purged || retained.Title != "" || retained.Directory != "" {
		t.Fatalf("purge recycled identity: %+v %v", retained, err)
	}
}

func TestCatalogueModuleSeesOfflinePurgesAtStartup(t *testing.T) {
	s, _ := openStore(t)
	h := metadataHistory(t)
	writeMetadata(t, h, "private", "", "/private")
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := s.FindBinding(t.Context(), nativeBinding("private"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Purge(t.Context(), statev2.Key{InstanceID: "history", SessionID: "private"}); err != nil {
		t.Fatal(err)
	}
	m = NewModule(m.deps)
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(t.Context(), before.ID)
	if err != nil || !after.Purged || after.Title != "" || after.Directory != "" {
		t.Fatalf("offline purge left queryable metadata: %+v %v", after, err)
	}
	if _, err := s.Reconcile(t.Context(), catalogSource(), []NativeSession{{ID: "private", Title: "stale", Directory: "/private"}}); err != nil {
		t.Fatal(err)
	}
	after, err = s.Get(t.Context(), before.ID)
	if err != nil || after.Title != "" {
		t.Fatalf("stale inventory restored purged title: %+v %v", after, err)
	}
}

func TestCatalogueModuleRetriesEarlyAnnouncementsWithoutAnotherHook(t *testing.T) {
	s, _ := openStore(t)
	h := metadataHistory(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	writeMetadata(t, h, binding().NativeID, "", reserved.Directory)
	m := NewModule(Deps{Store: s, History: h, OwnerID: reserved.OwnerID, Interval: time.Millisecond})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(m.pending) != 1 {
		t.Fatalf("early announcement dropped: %d", len(m.pending))
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	if _, err := s.Bind(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, err := s.Get(t.Context(), reserved.ID)
		if err == nil && row.Title == binding().NativeID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("metadata did not retry: %+v %v", row, err)
		}
		time.Sleep(time.Millisecond)
	}
}

type failingMetadata struct {
	*statev2.DB
	fail      atomic.Bool
	attempts  atomic.Int32
	afterPage func()
}

func (h *failingMetadata) Metadata(ctx context.Context, key statev2.Key) (statev2.SessionMetadata, error) {
	h.attempts.Add(1)
	if h.fail.Load() {
		return statev2.SessionMetadata{}, errors.New("temporarily unavailable")
	}
	return h.DB.Metadata(ctx, key)
}

func TestCatalogueModuleRunnerRetriesWithoutNewWrites(t *testing.T) {
	s, _ := openStore(t)
	h := &failingMetadata{DB: metadataHistory(t)}
	var reports atomic.Int32
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID, Interval: time.Millisecond, OnError: func(error) { reports.Add(1) }})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeMetadata(t, h.DB, "retry", "", "/repo")
	h.fail.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for h.attempts.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("failed metadata read was not retried")
		}
		time.Sleep(time.Millisecond)
	}
	h.fail.Store(false)
	for {
		_, err := s.FindBinding(t.Context(), nativeBinding("retry"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery required another source write: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if reports.Load() != 1 {
		t.Fatalf("identical index errors reported %d times", reports.Load())
	}
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
	if _, err := m.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(t.Context(), "foreign", 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("foreign metadata indexed on hub: %+v %v", rows, err)
	}
}

func (h *failingMetadata) MetadataPage(ctx context.Context, owner string, after statev2.Key, limit int) ([]statev2.SessionMetadata, error) {
	rows, err := h.DB.MetadataPage(ctx, owner, after, limit)
	if h.afterPage != nil {
		callback := h.afterPage
		h.afterPage = nil
		callback()
	}
	return rows, err
}

func TestCatalogueModuleInventoryFenceRecoversConcurrentInsert(t *testing.T) {
	s, _ := openStore(t)
	h := &failingMetadata{DB: metadataHistory(t)}
	writeMetadata(t, h.DB, "z", "", "/repo")
	h.afterPage = func() { writeMetadata(t, h.DB, "a", "", "/repo") }
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	// "a" sorts before the page cursor and can only be caught by the fence.
	if _, err := s.FindBinding(t.Context(), nativeBinding("a")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fixture did not insert behind cursor: %v", err)
	}
	if _, err := m.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindBinding(t.Context(), nativeBinding("a")); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogueModuleFailedReadsDoNotConsumeChanges(t *testing.T) {
	s, _ := openStore(t)
	h := &failingMetadata{DB: metadataHistory(t)}
	m := NewModule(Deps{Store: s, History: h, OwnerID: catalogSource().OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	writeMetadata(t, h.DB, "late", "", "/repo")
	h.fail.Store(true)
	if _, err := m.step(t.Context()); err == nil || m.cursor != 0 {
		t.Fatalf("failed read advanced cursor: %d %v", m.cursor, err)
	}
	h.fail.Store(false)
	if _, err := m.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindBinding(t.Context(), nativeBinding("late")); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogueModuleBoundsDeferredMetadata(t *testing.T) {
	s, _ := openStore(t)
	h := metadataHistory(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]statev2.SessionMetadata, 5000)
	for i := range rows {
		rows[i] = statev2.SessionMetadata{Instance: statev2.Instance{OwnerID: reserved.OwnerID, Platform: reserved.Platform, SourceKey: binding().SourceKey}, Session: statev2.Session{Key: statev2.Key{InstanceID: "history", SessionID: fmt.Sprint(i)}, Directory: reserved.Directory}}
	}
	m := NewModule(Deps{Store: s, History: h, OwnerID: reserved.OwnerID})
	if err := m.project(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	if len(m.pending) > 4096 || !m.rebuild {
		t.Fatalf("unbounded pending metadata: %d %v", len(m.pending), m.rebuild)
	}
	m.schedulePending()
	if !m.pendingRetryAt.After(time.Now()) {
		t.Fatal("deferred rebuild retries without backoff")
	}
	for range 20 {
		m.schedulePending()
	}
	if m.pendingDelay > 30*time.Second {
		t.Fatal("unbounded retry delay")
	}
}
