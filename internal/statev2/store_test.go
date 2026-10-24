package statev2

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	register(t, d, "first")
	return d
}

func register(t *testing.T, d *DB, id string) {
	t.Helper()
	if err := d.RegisterInstance(t.Context(), Instance{ID: id, OwnerID: "owner", Platform: "opencode", SourceKey: id}); err != nil {
		t.Fatal(err)
	}
}

func fixture(instance string) Batch {
	return Batch{
		Session: Session{Key: Key{InstanceID: instance, SessionID: "session"}, Title: "retained title", Directory: "/project",
			CreatedAt: 10, UpdatedAt: 20, Extensions: json.RawMessage(`{"native":true}`)},
		Messages:   []Message{{ID: "message", Role: "assistant", Model: "provider/model", CreatedAt: 11, CompletedAt: 19}},
		Parts:      []Part{{ID: "part", MessageID: "message", Kind: "text", Text: "retained content"}},
		Checkpoint: &CheckpointUpdate{Stream: "runtime/generation", Cursor: "1"},
	}
}

func commit(t *testing.T, d *DB, b Batch) CommitResult {
	t.Helper()
	r, err := d.Commit(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInstanceIdentity(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second") // same owner and platform, different source
	for _, id := range []string{"first", "second"} {
		b := fixture(id) // identical native message/session/part IDs
		b.Parts[0].Text = id
		commit(t, d, b)
		if got := read(t, d, b.Session.Key); got.Parts[0].Text != id {
			t.Fatalf("instance collision: %+v", got)
		}
	}
	for _, i := range []Instance{
		{ID: "first", OwnerID: "different", Platform: "opencode", SourceKey: "first"},
		{ID: "first", OwnerID: "owner", Platform: "claudecode", SourceKey: "first"},
		{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "different"},
		{ID: "third", OwnerID: "owner", Platform: "opencode", SourceKey: "first"},
	} {
		if err := d.RegisterInstance(t.Context(), i); !errors.Is(err, ErrIdentityConflict) {
			t.Fatalf("rebound %+v: %v", i, err)
		}
	}
	i := Instance{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "first", Name: "renamed"}
	if err := d.RegisterInstance(t.Context(), i); err != nil {
		t.Fatal(err)
	}
	got, err := d.Instance(t.Context(), "first")
	if err != nil || got != i {
		t.Fatalf("instance: %+v, %v", got, err)
	}
	if _, err := d.Instance(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, i := range []Instance{{}, {ID: "x"}, {ID: "x", OwnerID: "o"}, {ID: "x", OwnerID: "o", Platform: "p"}} {
		if err := d.RegisterInstance(t.Context(), i); err == nil {
			t.Fatalf("accepted invalid instance: %+v", i)
		}
	}
}

func TestCommitReplayAndPartialUpdates(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	if result := commit(t, d, b); result.Revision != 1 {
		t.Fatal(result)
	}
	before := read(t, d, b.Session.Key)
	if result := commit(t, d, b); !result.Duplicate {
		t.Fatal(result)
	}
	if after := read(t, d, b.Session.Key); !reflect.DeepEqual(after, before) || len(changes(t, d)) != 1 {
		t.Fatal("duplicate changed history or published twice")
	}
	b.ExpectedRevision = 1
	b.Checkpoint = &CheckpointUpdate{Stream: "runtime/generation", ExpectedCursor: "1", Cursor: "2"}
	b.Messages = nil // a partial snapshot must not erase the message
	b.Parts[0].Text = "completed text"
	b.Parts[0].Extensions = json.RawMessage(`{"tool":"metadata"}`)
	if result := commit(t, d, b); result.Revision != 2 {
		t.Fatal(result)
	}
	got := read(t, d, b.Session.Key)
	if len(got.Messages) != 1 || got.Parts[0].Text != "completed text" || string(got.Parts[0].Extensions) != `{"tool":"metadata"}` {
		t.Fatalf("partial upsert: %+v", got)
	}
	if got.Session.Title != b.Session.Title || string(got.Session.Extensions) != `{"native":true}` || string(got.Messages[0].Extensions) != "{}" {
		t.Fatal("metadata did not round-trip")
	}
	if page := changes(t, d); len(page) != 2 || page[1].Revision != 2 || page[1].Kind != "updated" {
		t.Fatalf("change page: %+v", page)
	}
}

func TestFailedBatchesAreAtomic(t *testing.T) {
	for name, mutate := range map[string]func(*Batch){
		"stale session":   func(b *Batch) { b.ExpectedRevision = 0 },
		"stale cursor":    func(b *Batch) { b.Checkpoint.ExpectedCursor = "stale" },
		"orphan part":     func(b *Batch) { b.Parts[0].MessageID = "missing" },
		"invalid JSON":    func(b *Batch) { b.Parts[0].Extensions = json.RawMessage(`{`) },
		"invalid message": func(b *Batch) { b.Messages[0].Position = -1 },
		"invalid session": func(b *Batch) { b.Session.CreatedAt = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			d := openTestDB(t)
			b := fixture("first")
			commit(t, d, b)
			before := read(t, d, b.Session.Key)
			b.ExpectedRevision = 1
			b.Checkpoint = &CheckpointUpdate{Stream: "runtime/generation", ExpectedCursor: "1", Cursor: "2"}
			b.Session.Title = "must roll back"
			mutate(&b)
			if _, err := d.Commit(t.Context(), b); err == nil {
				t.Fatal("accepted bad batch")
			}
			cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation")
			if err != nil || cursor != "1" || len(changes(t, d)) != 1 || !reflect.DeepEqual(before, read(t, d, b.Session.Key)) {
				t.Fatalf("partial commit: cursor %q %v", cursor, err)
			}
		})
	}
}

func TestCheckpointAndOutboxFailuresRollBackContent(t *testing.T) {
	for _, table := range []string{"checkpoint", "change_outbox"} {
		t.Run(table, func(t *testing.T) {
			d := openTestDB(t)
			b := fixture("first")
			commit(t, d, b)
			before := read(t, d, b.Session.Key)
			if _, err := d.db.Exec("CREATE TRIGGER fail_write BEFORE INSERT ON " + table +
				" BEGIN SELECT RAISE(ABORT, 'test persistence failure'); END"); err != nil {
				t.Fatal(err)
			}
			b.ExpectedRevision = 1
			b.Session.Title, b.Parts[0].Text = "not committed", "not committed"
			b.Checkpoint = &CheckpointUpdate{Stream: "runtime/generation", ExpectedCursor: "1", Cursor: "2"}
			if _, err := d.Commit(t.Context(), b); err == nil {
				t.Fatal("accepted failed persistence")
			}
			cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation")
			if err != nil || cursor != "1" || len(changes(t, d)) != 1 || !reflect.DeepEqual(before, read(t, d, b.Session.Key)) {
				t.Fatalf("persistence failure partially committed: %q %v", cursor, err)
			}
		})
	}
}

func TestCheckpointGenerationsAndRestartReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statev2.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	commit(t, d, b)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if result := commit(t, d, b); !result.Duplicate || len(changes(t, d)) != 1 {
		t.Fatal("restart replay duplicated an invalidation")
	}
	b.ExpectedRevision = 1
	b.Checkpoint = &CheckpointUpdate{Stream: "runtime/new-generation", Cursor: "1"}
	if result := commit(t, d, b); result.Duplicate || result.Revision != 2 {
		t.Fatalf("new generation was mistaken for old replay: %+v", result)
	}
}

func TestSourceDeletionRetainsHistory(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	commit(t, d, b)
	b.ExpectedRevision, b.Checkpoint = 1, nil
	b.Session.SourceDeleted, b.Messages[0].SourceDeleted, b.Parts[0].SourceDeleted = true, true, true
	commit(t, d, b)
	got := read(t, d, b.Session.Key)
	if !got.Session.SourceDeleted || !got.Messages[0].SourceDeleted || !got.Parts[0].SourceDeleted || got.Parts[0].Text != "retained content" {
		t.Fatalf("source deletion erased history: %+v", got)
	}
}

func TestPaginationAndOrdering(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Messages = []Message{{ID: "b", Role: "user", Position: 0, ConversationVisible: true}, {ID: "a", Role: "assistant", Position: 0, ConversationVisible: true}, {ID: "c", Role: "system", Position: 1, ConversationVisible: true}}
	b.Parts = []Part{{ID: "z", MessageID: "b", Kind: "unknown", Position: 1},
		{ID: "a", MessageID: "b", Kind: "text", Position: 0}, {ID: "z", MessageID: "a", Kind: "text"}}
	commit(t, d, b)
	got, err := d.ReadConversation(t.Context(), b.Session.Key, 1, 1)
	if err != nil || got.TotalMessages != 3 || len(got.Messages) != 1 || got.Messages[0].ID != "b" || len(got.Parts) != 2 || got.Parts[0].ID != "a" {
		t.Fatalf("page: %+v %v", got, err)
	}
	got, err = d.ReadConversation(t.Context(), b.Session.Key, 1, 10)
	if err != nil || len(got.Messages) != 0 || len(got.Parts) != 0 || got.TotalMessages != 3 {
		t.Fatalf("empty page: %+v %v", got, err)
	}
}

func TestConcurrentRepairRevisionGuard(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	commit(t, d, b)
	b.ExpectedRevision, b.Checkpoint = 1, nil
	var wg sync.WaitGroup
	outcomes := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			_, err := d.Commit(t.Context(), b)
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	successes := 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 || len(changes(t, d)) != 2 {
		t.Fatalf("concurrent repairs accepted: %d", successes)
	}
}

func TestInputValidationAndCancellation(t *testing.T) {
	d := openTestDB(t)
	for _, mutate := range []func(*Batch){
		func(b *Batch) { b.Session.InstanceID = "" },
		func(b *Batch) { b.Session.SessionID = "" },
		func(b *Batch) { b.ExpectedRevision = -1 },
		func(b *Batch) { b.Checkpoint.Stream = "" },
		func(b *Batch) { b.Checkpoint.Cursor = "" },
		func(b *Batch) { b.Checkpoint.ExpectedCursor = "1" },
		func(b *Batch) { b.Session.InstanceID = "unregistered" },
	} {
		b := fixture("first")
		mutate(&b)
		if _, err := d.Commit(t.Context(), b); err == nil {
			t.Fatal("accepted invalid batch")
		}
	}
	cursor, err := d.Checkpoint(t.Context(), "first", "missing")
	if err != nil || cursor != "" {
		t.Fatalf("missing checkpoint %q %v", cursor, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.Commit(ctx, fixture("first")); err == nil {
		t.Fatal("committed cancelled batch")
	}
	if err := d.RegisterInstance(ctx, Instance{ID: "x", OwnerID: "o", Platform: "p", SourceKey: "s"}); err == nil {
		t.Fatal("registered with cancelled context")
	}
}
