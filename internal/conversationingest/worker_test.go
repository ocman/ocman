package conversationingest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type fakeHistory struct {
	mu                                     sync.Mutex
	key                                    string
	entries                                []db.HistoryEntry
	snapshots                              map[string]*db.HistorySnapshot
	identityErr, inventoryErr, snapshotErr error
	reads                                  int
	changingReads                          int // leading snapshot reads that fail as the source is written
	block                                  bool
	afterSnapshot                          func()
}

func fakeSource(key string) *fakeHistory {
	return &fakeHistory{key: key, entries: []db.HistoryEntry{{ID: "session", Version: "v1"}}, snapshots: map[string]*db.HistorySnapshot{"session": historySnapshot(key)}}
}

func (f *fakeHistory) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: f.key, Name: "test"}, f.identityErr
}

func (f *fakeHistory) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	f.mu.Lock()
	entries, err, block := append([]db.HistoryEntry{}, f.entries...), f.inventoryErr, f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return entries, err
}

func (f *fakeHistory) HistorySnapshot(_ context.Context, id string) (*db.HistorySnapshot, error) {
	f.mu.Lock()
	f.reads++
	snapshot, err, after := f.snapshots[id], f.snapshotErr, f.afterSnapshot
	if f.changingReads > 0 {
		f.changingReads--
		err = db.ErrHistoryChanged
	}
	f.mu.Unlock()
	if after != nil {
		after()
	}
	return snapshot, err
}

func testImporter(t *testing.T, sources ...*fakeHistory) (*Module, *statev2.DB) {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	deps := Deps{Store: store, OwnerID: "owner", PollInterval: time.Hour, Debounce: time.Millisecond, ReadTimeout: 100 * time.Millisecond}
	for _, source := range sources {
		deps.Sources = append(deps.Sources, Source{AdapterID: source.key, Reader: source})
	}
	m := NewModule(deps)
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return m, store
}

func sourceKey(source string) statev2.Key {
	return statev2.Key{InstanceID: instanceID("owner", platforms.HistoryIdentity{Platform: "opencode", SourceKey: source}), SessionID: "session"}
}

func importText(t *testing.T, store *statev2.DB, key statev2.Key) (string, int64) {
	t.Helper()
	detail, err := store.Read(t.Context(), key, 500, 0)
	if err != nil || len(detail.Parts) != 1 {
		t.Fatalf("read imported content: %+v %v", detail, err)
	}
	return detail.Parts[0].Text, detail.Session.Revision
}

func repair(t *testing.T, w *sourceWorker, dirty map[string]bool) {
	t.Helper()
	if err := w.repair(t.Context(), dirty); err != nil {
		t.Fatal(err)
	}
}

func TestInitialImportAndRestartDeduplicate(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	if text, revision := importText(t, store, sourceKey("first")); text != "first" || revision != 1 {
		t.Fatalf("initial import: %q %d", text, revision)
	}
	repair(t, w, nil)
	if f.reads != 1 {
		t.Fatal("unchanged source hint caused another read")
	}
	restarted := NewModule(m.deps)
	repair(t, restarted.workers[0], nil)
	if f.reads != 1 {
		t.Fatal("restart reread a session whose persisted hint is unchanged")
	}
	w.verifiedAt = time.Time{}
	repair(t, w, nil)
	if _, revision := importText(t, store, sourceKey("first")); revision != 1 || f.reads != 2 {
		t.Fatal("verification rewrote an unchanged snapshot")
	}
	changes, err := store.Changes(t.Context(), 0, 500)
	if err != nil || len(changes) != 1 {
		t.Fatalf("restart replay published twice: %+v %v", changes, err)
	}
	// A late import can change old content without advancing session timestamps.
	// Normal passes trust the hint; only verification finds and repairs it.
	f.snapshots["session"] = historySnapshot("changed without newer timestamp")
	repair(t, w, nil)
	if text, _ := importText(t, store, sourceKey("first")); text != "first" || f.reads != 2 {
		t.Fatal("normal pass ignored the version hint")
	}
	w.verifiedAt = time.Time{}
	repair(t, w, nil)
	if text, revision := importText(t, store, sourceKey("first")); text != "changed without newer timestamp" || revision != 2 {
		t.Fatalf("verification missed old content: %q %d", text, revision)
	}
	if time.Since(w.verifiedAt) > time.Minute {
		t.Fatal("verification time not recorded")
	}
	f.entries[0].Version = "v2"
	f.snapshots["session"].Version = "v2"
	repair(t, w, nil)
	if f.reads != 4 {
		t.Fatal("changed version hint was not reread")
	}
}

func TestVerificationTimePersistsAcrossRestart(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	repair(t, m.workers[0], nil)
	key := m.workers[0].instance.ID
	if err := store.SetSourceState(t.Context(), key, verifiedAtKey, "1"); err != nil {
		t.Fatal(err)
	}
	restarted := NewModule(m.deps)
	repair(t, restarted.workers[0], nil)
	if f.reads != 2 {
		t.Fatal("overdue verification did not run after restart")
	}
	again := NewModule(m.deps)
	repair(t, again.workers[0], nil)
	if f.reads != 2 {
		t.Fatal("verification repeated after restart before its interval")
	}
}

func TestSourceDeletionAndReappearance(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	f.entries = nil
	repair(t, w, nil)
	detail, err := store.Read(t.Context(), sourceKey("first"), 500, 0)
	if err != nil || !detail.Session.SourceDeleted || detail.Parts[0].Text != "first" {
		t.Fatalf("source deletion removed history: %+v %v", detail, err)
	}
	f.entries = []db.HistoryEntry{{ID: "session", Version: "v1"}}
	repair(t, w, nil)
	detail, err = store.Read(t.Context(), sourceKey("first"), 500, 0)
	if err != nil || detail.Session.SourceDeleted || detail.Session.Revision != 3 {
		t.Fatalf("identical source did not reappear: %+v %v", detail, err)
	}
}

func TestQueueOverflowRepairsUnchangedVersionHints(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	// Content changed without a new hint; its invalidation was lost to overflow.
	f.snapshots["session"] = historySnapshot("changed under the same hint")
	for i := range 5000 {
		m.Notify("first", fmt.Sprint("other-", i))
	}
	w.sweep(t.Context())
	if text, _ := importText(t, store, sourceKey("first")); text != "changed under the same hint" {
		t.Fatalf("overflow repair skipped an invalidated session: %q", text)
	}
}

func TestSourceDeletionRetainsParentAndProject(t *testing.T) {
	f := fakeSource("first")
	f.snapshots["session"].Session.ParentID = "parent"
	f.snapshots["session"].Session.ProjectID = "project"
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	f.entries = nil
	repair(t, w, nil)
	detail, err := store.Read(t.Context(), sourceKey("first"), 500, 0)
	if err != nil || !detail.Session.SourceDeleted || detail.Session.ParentID != "parent" || detail.Session.ProjectID != "project" {
		t.Fatalf("source deletion cleared parent/project: %+v %v", detail.Session, err)
	}
	stored, _, err := store.SessionState(t.Context(), sourceKey("first"))
	if err != nil || stored.ParentID != "parent" || stored.ProjectID != "project" {
		t.Fatalf("session state lost parent/project: %+v %v", stored, err)
	}
}

func TestFailedInventoriesAndSnapshotsPreserveHistory(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	f.entries, f.inventoryErr = nil, errors.New("disconnected")
	if err := w.repair(t.Context(), nil); err == nil {
		t.Fatal("accepted failed inventory")
	}
	f.inventoryErr = nil
	f.entries = []db.HistoryEntry{{ID: "session"}, {ID: "session"}}
	if err := w.repair(t.Context(), nil); err == nil {
		t.Fatal("accepted duplicate inventory")
	}
	f.entries = []db.HistoryEntry{{}}
	if err := w.repair(t.Context(), nil); err == nil {
		t.Fatal("accepted missing inventory identity")
	}
	f.entries = []db.HistoryEntry{{ID: "session", Version: "v2"}}
	f.snapshotErr = errors.New("temporarily unavailable")
	var failures int
	m.deps.OnError = func(_, _ string, _ error) { failures++ }
	if err := w.repair(t.Context(), nil); err == nil {
		t.Fatal("failed session looked like a successful scan")
	}
	if _, revision := importText(t, store, sourceKey("first")); revision != 1 || failures != 1 {
		t.Fatal("snapshot failure changed retained history")
	}
	f.snapshotErr = nil
	f.snapshots["session"] = historySnapshot("recovered")
	repair(t, w, nil)
	if text, revision := importText(t, store, sourceKey("first")); text != "recovered" || revision != 2 {
		t.Fatalf("failed snapshot was not retried: %q %d", text, revision)
	}
}

func TestCompleteRepairRetainsRemovedParts(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	snapshot := historySnapshot("unused")
	snapshot.Messages, snapshot.Parts = nil, nil
	f.snapshots["session"] = snapshot
	repair(t, w, map[string]bool{"session": true})
	detail, err := store.Read(t.Context(), sourceKey("first"), 500, 0)
	if err != nil || len(detail.Messages) != 1 || !detail.Messages[0].SourceDeleted || !detail.Parts[0].SourceDeleted || detail.Parts[0].Text != "first" {
		t.Fatalf("removed content not retained: %+v %v", detail, err)
	}
}

func TestPurgeIsNeverReadOrResurrectedByRepair(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	if err := store.Purge(t.Context(), sourceKey("first")); err != nil {
		t.Fatal(err)
	}
	repair(t, w, map[string]bool{"": true})
	if f.reads != 1 {
		t.Fatal("read source content after explicit purge")
	}
	if _, err := store.Read(t.Context(), sourceKey("first"), 1, 0); !errors.Is(err, statev2.ErrNotFound) {
		t.Fatal("purged history resurrected")
	}
}

func TestConcurrentRepairCannotOverwriteNewerCommit(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	var once sync.Once
	f.afterSnapshot = func() {
		once.Do(func() {
			batch, _, err := Normalize(sourceKey("first"), historySnapshot("newer commit"))
			if err != nil {
				t.Fatal(err)
			}
			batch.ExpectedRevision = 1
			if _, err := store.Commit(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
		})
	}
	f.snapshots["session"] = historySnapshot("stale snapshot")
	var failure error
	m.deps.OnError = func(_, _ string, err error) { failure = err }
	if err := w.repair(t.Context(), map[string]bool{"session": true}); err == nil {
		t.Fatal("conflicting snapshot looked like a successful scan")
	}
	if text, _ := importText(t, store, sourceKey("first")); text != "newer commit" || !errors.Is(failure, statev2.ErrConflict) {
		t.Fatalf("concurrent write overwritten: %q %v", text, failure)
	}
}

func TestIndependentSourcesAndJoinedShutdown(t *testing.T) {
	blocked, healthy := fakeSource("blocked"), fakeSource("healthy")
	blocked.block = true
	m, store := testImporter(t, blocked, healthy)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := store.Read(t.Context(), sourceKey("healthy"), 1, 0)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked source prevented independent ingestion")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workers outlived cancellation")
	}
	// Notification after shutdown may enqueue only; it cannot access the DB.
	store.Close()
	m.Notify("", "session")
}

func TestNotifyRoutingAndBoundedQueue(t *testing.T) {
	m, _ := testImporter(t, fakeSource("first"), fakeSource("second"))
	m.Notify("first", "session")
	if !m.workers[0].dirty["session"] || m.workers[1].dirty["session"] {
		t.Fatal("invalidation crossed explicit source routing")
	}
	m.Notify("", "both")
	if !m.workers[0].dirty["both"] || !m.workers[1].dirty["both"] {
		t.Fatal("legacy invalidation missed a source")
	}
	for i := range 5000 {
		m.Notify("first", string(rune(i)))
	}
	if len(m.workers[0].dirty) > 4096 || !m.workers[0].dirty[""] {
		t.Fatal("invalidation queue is not bounded")
	}
	if err := NewModule(Deps{}).Start(t.Context()); err == nil {
		t.Fatal("accepted missing database/owner")
	}
	if len(NewModule(Deps{Sources: []Source{{}}}).workers) != 0 {
		t.Fatal("registered nil history reader")
	}
}

func TestMultipleInstancesOfOnePlatformRetainSeparateHistory(t *testing.T) {
	m, store := testImporter(t, fakeSource("first"), fakeSource("second"))
	for _, worker := range m.workers {
		repair(t, worker, nil)
	}
	for _, source := range []string{"first", "second"} {
		if text, _ := importText(t, store, sourceKey(source)); text != source {
			t.Fatalf("cross-instance history: %q", text)
		}
	}
	if m.workers[0].instance.Platform != m.workers[1].instance.Platform || m.workers[0].instance.ID == m.workers[1].instance.ID {
		t.Fatal("platform type was confused with instance identity")
	}
}

func TestSourceIdentityCannotBeReboundWhileRunning(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	f.key = "different-configuration"
	if err := w.repair(t.Context(), nil); !errors.Is(err, statev2.ErrIdentityConflict) {
		t.Fatalf("rebound physical source: %v", err)
	}
	if text, revision := importText(t, store, sourceKey("first")); text != "first" || revision != 1 {
		t.Fatal("changed identity overwrote retained source")
	}
}

func TestFailedSessionDoesNotReportSuccessfulRepair(t *testing.T) {
	f := fakeSource("first")
	f.snapshotErr = errors.New("source read failed")
	m, _ := testImporter(t, f)
	if err := m.workers[0].repair(t.Context(), nil); err == nil {
		t.Fatal("a scan with failed sessions was reported as successful")
	}
}

func TestLiveInvalidationDuringImportIsNotLost(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	var once sync.Once
	f.afterSnapshot = func() {
		once.Do(func() {
			f.mu.Lock()
			f.snapshots["session"] = historySnapshot("newer content with same version hint")
			f.mu.Unlock()
			m.Notify("first", "session")
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(3 * time.Second); ; {
		detail, err := store.Read(t.Context(), sourceKey("first"), 500, 0)
		if err == nil && len(detail.Parts) == 1 && detail.Parts[0].Text == "newer content with same version hint" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("invalidation arriving during import was lost")
		}
		time.Sleep(time.Millisecond)
	}
}
