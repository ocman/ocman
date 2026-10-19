package conversationingest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// fakeFeed is a feed source: sequences, per-message reads and a live stream.
type fakeFeed struct {
	*fakeHistory
	sequences    map[string]int64
	messageReads [][]string
	messageErr   error
	changes      chan platforms.HistoryChange
	connects     int
}

func newFakeFeed() *fakeFeed {
	f := &fakeFeed{fakeHistory: fakeSource("feed"), sequences: map[string]int64{"session": 3}, changes: make(chan platforms.HistoryChange, 10)}
	return f
}

func (f *fakeFeed) HistorySequences(context.Context) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int64{}
	for k, v := range f.sequences {
		out[k] = v
	}
	return out, nil
}

func (f *fakeFeed) HistoryMessages(_ context.Context, sessionID string, ids []string) (*db.HistorySnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messageReads = append(f.messageReads, ids)
	if f.messageErr != nil {
		return nil, f.messageErr
	}
	full := f.snapshots[sessionID]
	out := &db.HistorySnapshot{Session: full.Session, Version: full.Version, Messages: []db.Message{}, Parts: []db.Part{}}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, m := range full.Messages {
		if want[m.ID] {
			out.Messages = append(out.Messages, m)
		}
	}
	for _, p := range full.Parts {
		if want[p.MessageID] {
			out.Parts = append(out.Parts, p)
		}
	}
	return out, nil
}

func (f *fakeFeed) WatchHistory(ctx context.Context, connected func(), change func(platforms.HistoryChange)) error {
	f.mu.Lock()
	f.connects++
	f.mu.Unlock()
	connected()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c, ok := <-f.changes:
			if !ok {
				return errors.New("disconnected")
			}
			change(c)
		}
	}
}

func feedImporter(t *testing.T, f *fakeFeed) (*sourceWorker, *statev2.DB) {
	t.Helper()
	store, err := statev2.Open(t.TempDir() + "/statev2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	m := NewModule(Deps{Store: store, OwnerID: "owner", PollInterval: time.Hour, Debounce: time.Millisecond, ReadTimeout: time.Second,
		Sources: []Source{{AdapterID: "feed", Reader: f}}})
	w := m.workers[0]
	w.feed = f
	w.feedState = feedState{pending: map[string]*platforms.HistoryChange{}, catchUp: true}
	return w, store
}

func feedKey(w *sourceWorker) statev2.Key {
	return statev2.Key{InstanceID: w.instance.ID, SessionID: "session"}
}

func receipt(t *testing.T, store *statev2.DB, w *sourceWorker) string {
	t.Helper()
	state, err := store.SourceState(t.Context(), w.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	return state["read-ready/session"]
}

func TestFeedCatchUpReadsOnlySessionsWithNewSequences(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "feed" || f.reads != 1 {
		t.Fatalf("initial import = %q after %d reads", text, f.reads)
	}
	if got := receipt(t, store, w); got != "seq:3" {
		t.Fatalf("read receipt = %q, want the applied sequence", got)
	}
	if _, ready := w.module.ReadyInstances(); !ready {
		t.Fatal("caught-up feed source is not ready")
	}
	// A reconnect with unchanged sequences reads no conversation content.
	w.feedState.catchUp = true
	w.drainFeed(t.Context())
	if f.reads != 1 {
		t.Fatalf("unchanged catch-up reread %d sessions", f.reads-1)
	}
	// After downtime, only the session whose sequence moved is reread.
	f.snapshots["session"] = historySnapshot("changed offline")
	f.sequences["session"] = 9
	restarted, _ := feedImporter(t, f)
	restarted.module.deps.Store = store
	restarted.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "changed offline" || f.reads != 2 {
		t.Fatalf("catch-up = %q after %d reads", text, f.reads)
	}
	if got := receipt(t, store, w); got != "seq:9" {
		t.Fatalf("read receipt after catch-up = %q", got)
	}
}

func TestFeedLiveChangesReadOnlyNamedMessages(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	// A new assistant message arrives: only it is read, not the session.
	f.snapshots["session"].Messages = append(f.snapshots["session"].Messages, db.Message{ID: "reply", SessionID: "session", TimeCreated: 5,
		Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)})
	f.snapshots["session"].Parts = append(f.snapshots["session"].Parts, db.Part{ID: "reply-text", SessionID: "session", MessageID: "reply",
		Data: json.RawMessage(`{"type":"text","text":"live reply"}`)})
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"reply"}})
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 5, MessageIDs: []string{"reply"}})
	w.drainFeed(t.Context())
	if f.reads != 1 || len(f.messageReads) != 1 || len(f.messageReads[0]) != 1 || f.messageReads[0][0] != "reply" {
		t.Fatalf("live change read %d snapshots and messages %v", f.reads-1, f.messageReads)
	}
	page, err := store.ReadConversation(t.Context(), feedKey(w), 30, 0)
	if err != nil || page.TotalMessages != 2 || page.Messages[1].ID != "reply" || page.Parts[len(page.Parts)-1].Text != "live reply" {
		t.Fatalf("live change not applied: %+v %v", page, err)
	}
	if got := receipt(t, store, w); got != "seq:5" {
		t.Fatalf("receipt = %q, want merged latest sequence", got)
	}
	// Replaying an already applied sequence changes nothing.
	revision := page.Session.Revision
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 5, MessageIDs: []string{"reply"}})
	w.drainFeed(t.Context())
	if again, _ := store.ReadConversation(t.Context(), feedKey(w), 30, 0); again.Session.Revision != revision {
		t.Fatal("duplicate event rewrote history")
	}
}

func TestFeedResyncDeletionAndFailureRepair(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.snapshots["session"] = historySnapshot("rewritten by revert")
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 6, Resync: true})
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "rewritten by revert" || f.reads != 2 {
		t.Fatalf("resync = %q after %d reads", text, f.reads)
	}
	// A failed change leaves the source unready and repairs by catch-up.
	f.messageErr = errors.New("database busy")
	f.sequences["session"] = 7
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 7, MessageIDs: []string{"message"}})
	w.drainFeed(t.Context())
	if _, ready := w.module.ReadyInstances(); ready || !w.feedState.catchUp {
		t.Fatal("failed live change left the source ready without repair")
	}
	f.messageErr = nil
	w.drainFeed(t.Context())
	if got := receipt(t, store, w); got != "seq:7" {
		t.Fatalf("repair receipt = %q", got)
	}
	if _, ready := w.module.ReadyInstances(); !ready {
		t.Fatal("repaired source is not ready")
	}
	// Deletion retains history as source-deleted.
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 8, Deleted: true})
	w.drainFeed(t.Context())
	state, purged, err := store.SessionState(t.Context(), feedKey(w))
	if err != nil || purged || !state.SourceDeleted {
		t.Fatalf("deleted session = %+v purged=%v %v", state, purged, err)
	}
	if text, _ := importText(t, store, feedKey(w)); text != "rewritten by revert" {
		t.Fatal("deletion discarded retained content")
	}
}

func TestFeedRepairRequestRereadsOneSessionAtCurrentSequence(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	// A stale stored read asks for repair without a feed event.
	f.snapshots["session"] = historySnapshot("repaired")
	f.sequences["session"] = 11
	w.module.Notify("feed", "session")
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "repaired" || f.reads != 2 {
		t.Fatalf("repair = %q after %d reads", text, f.reads)
	}
	if got := receipt(t, store, w); got != "seq:11" {
		t.Fatalf("repair receipt = %q", got)
	}
	// A request for every session runs a sequence catch-up, not a full read.
	w.module.Notify("feed", "")
	w.drainFeed(t.Context())
	if f.reads != 2 {
		t.Fatalf("catch-up after a broad repair request reread %d sessions", f.reads-2)
	}
}

func TestAwaitReturnsWhenQueuedChangeApplies(t *testing.T) {
	f := newFakeFeed()
	store, err := statev2.Open(t.TempDir() + "/statev2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewModule(Deps{Store: store, OwnerID: "owner", PollInterval: time.Hour, Debounce: time.Hour, ReadTimeout: time.Second,
		Sources: []Source{{AdapterID: "feed", Reader: f}}})
	var done sync.WaitGroup
	defer done.Wait() // runs after cancel: deferred calls unwind in reverse
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done.Go(func() { m.Run(ctx) })
	waitCtx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if !m.Await(waitCtx, "feed", "session", "seq:3") {
		t.Fatal("initial import never satisfied a waiting read")
	}
	f.mu.Lock()
	reads := f.reads
	f.mu.Unlock()
	// The turn's final event is queued; a read for it must not wait for the
	// polling-era debounce or force a full-session reread.
	f.changes <- platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"message"}}
	started := time.Now()
	waitCtx, stop = context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if !m.Await(waitCtx, "feed", "session", "seq:4") {
		t.Fatal("read did not observe the applied change")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("waiting read took %s", elapsed)
	}
	f.mu.Lock()
	if f.reads != reads {
		f.mu.Unlock()
		t.Fatal("waiting read forced a full-session reread")
	}
	f.mu.Unlock()
	// Unknown sources and expired waits report false rather than blocking.
	expired, cancelExpired := context.WithCancel(t.Context())
	cancelExpired()
	if m.Await(expired, "feed", "session", "seq:99") || m.Await(t.Context(), "other", "session", "seq:1") {
		t.Fatal("await reported progress it did not observe")
	}
}

func TestFeedSourcesDoNotPoll(t *testing.T) {
	f := newFakeFeed()
	store, err := statev2.Open(t.TempDir() + "/statev2.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewModule(Deps{Store: store, OwnerID: "owner", PollInterval: 20 * time.Millisecond, Debounce: time.Millisecond, ReadTimeout: time.Second,
		Sources: []Source{{AdapterID: "feed", Reader: f}}})
	ctx, cancel := context.WithCancel(t.Context())
	var done sync.WaitGroup
	done.Go(func() { m.Run(ctx) })
	ready := func() bool { _, ready := m.ReadyInstances(); return ready }
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				cancel()
				t.Fatal(what)
			}
		}
	}
	waitFor("feed source never became ready", ready)
	f.mu.Lock()
	f.snapshots["session"].Parts = append(f.snapshots["session"].Parts, db.Part{ID: "live", SessionID: "session", MessageID: "message",
		Data: json.RawMessage(`{"type":"text","text":"streamed"}`)})
	f.mu.Unlock()
	f.changes <- platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"message"}}
	waitFor("live change was not applied", func() bool {
		state, err := store.SourceState(t.Context(), m.workers[0].instance.ID)
		return err == nil && state["read-ready/session"] == "seq:4"
	})
	// Many poll intervals pass while connected: no inventory or snapshot read.
	f.mu.Lock()
	reads := f.reads
	f.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	// Readiness is refreshed on the poll tick, which a busy scheduler may
	// delay; it must come back without any read.
	waitFor("connected feed source lost readiness without reads", ready)
	f.mu.Lock()
	if f.reads != reads {
		f.mu.Unlock()
		cancel()
		t.Fatal("connected feed source polled snapshots")
	}
	f.mu.Unlock()
	cancel()
	done.Wait()
}
