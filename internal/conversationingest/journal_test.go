package conversationingest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// recordTranslator turns each record into one message for its session. A
// record of type "fail" makes translation fail transiently.
type recordTranslator struct {
	store  *statev2.DB
	calls  int
	failed bool
}

func (r *recordTranslator) refresh(ctx context.Context, records []historyjournal.Record) error {
	r.calls++
	for _, rec := range records {
		if rec.Type == "fail" && !r.failed {
			r.failed = true
			return errors.New("source temporarily unavailable")
		}
		key := statev2.Key{InstanceID: "source", SessionID: rec.Session}
		stream := "test-refresh/" + rec.Session
		previous, err := r.store.Checkpoint(ctx, key.InstanceID, stream)
		if err != nil {
			return err
		}
		cursor, _ := strconv.Atoi(previous)
		_, err = r.store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: key.InstanceID,
			Checkpoint: statev2.CheckpointUpdate{Stream: stream, ExpectedCursor: previous, Cursor: strconv.Itoa(cursor + 1)}, Batches: []statev2.StreamBatch{{
				Session:  &statev2.Session{Key: key, Title: rec.Session, Extensions: json.RawMessage(`{}`), ReadFormat: statev2.ConversationReadFormat},
				Messages: []statev2.Message{{ID: rec.Type, Role: "assistant", ConversationVisible: true, Extensions: json.RawMessage(`{"role":"assistant"}`)}},
			}}})
		if err != nil {
			return err
		}
	}
	return nil
}

func journalFixture(t *testing.T) (*journalConsumer, *statev2.DB, *recordTranslator, string, *[]string) {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	instance := statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "/source"}
	if err := store.RegisterInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "journal")
	translator := &recordTranslator{store: store}
	gaps := &[]string{}
	c := &journalConsumer{store: store, instanceID: instance.ID, dir: dir, refresh: translator.refresh, batchSize: 2,
		onGap: func(segment string) { *gaps = append(*gaps, segment) }}
	return c, store, translator, dir, gaps
}

func appendRecord(t *testing.T, dir, segment, session, kind string) {
	t.Helper()
	if err := historyjournal.Append(dir, segment, historyjournal.Record{Session: session, Type: kind}); err != nil {
		t.Fatal(err)
	}
}

func messageIDs(t *testing.T, store *statev2.DB, session string) []string {
	t.Helper()
	page, err := store.ReadConversation(t.Context(), statev2.Key{InstanceID: "source", SessionID: session}, 100, 0)
	if errors.Is(err, statev2.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range page.Messages {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestJournalConsumerAppliesAndResumesAfterRestart(t *testing.T) {
	c, store, _, dir, _ := journalFixture(t)
	for _, kind := range []string{"m1", "m2", "m3"} {
		appendRecord(t, dir, "a.jsonl", "one", kind)
	}
	appendRecord(t, dir, "b.jsonl", "two", "m1")
	applied, err := c.drain(t.Context())
	if err != nil || applied != 4 {
		t.Fatalf("drain = %d, %v", applied, err)
	}
	if got := messageIDs(t, store, "one"); len(got) != 3 || got[2] != "m3" {
		t.Fatalf("session one = %v", got)
	}
	// A new consumer, as after a restart, resumes at the committed offsets.
	restarted := &journalConsumer{store: store, instanceID: "source", dir: dir, refresh: (&recordTranslator{store: store}).refresh, batchSize: 10}
	appendRecord(t, dir, "a.jsonl", "one", "m4")
	if applied, err := restarted.drain(t.Context()); err != nil || applied != 1 {
		t.Fatalf("resume = %d, %v", applied, err)
	}
	if got := messageIDs(t, store, "one"); len(got) != 4 {
		t.Fatalf("missed or replayed events after restart: %v", got)
	}
}

func TestJournalConsumerRetriesTransientFailuresWithoutSkipping(t *testing.T) {
	c, store, translator, dir, _ := journalFixture(t)
	appendRecord(t, dir, "a.jsonl", "one", "fail")
	appendRecord(t, dir, "a.jsonl", "one", "m2")
	if _, err := c.drain(t.Context()); err == nil {
		t.Fatal("transient translation failure was ignored")
	}
	if got := messageIDs(t, store, "one"); got != nil {
		t.Fatalf("cursor advanced past a failed translation: %v", got)
	}
	if applied, err := c.drain(t.Context()); err != nil || applied != 2 || translator.calls < 2 {
		t.Fatalf("retry = %d, %v", applied, err)
	}
}

func TestJournalConsumerReplaysRefreshCommittedBeforeFailure(t *testing.T) {
	c, store, _, dir, _ := journalFixture(t)
	appendRecord(t, dir, "a.jsonl", "one", "m1")
	appendRecord(t, dir, "a.jsonl", "one", "fail")
	if _, err := c.drain(t.Context()); err == nil {
		t.Fatal("expected failure after the first content commit")
	}
	if got := messageIDs(t, store, "one"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("refresh did not commit before failure: %v", got)
	}
	if cursor, err := store.Checkpoint(t.Context(), "source", journalStream("a.jsonl")); err != nil || cursor != "" {
		t.Fatalf("failed refresh advanced journal checkpoint: %q %v", cursor, err)
	}
	// A restart repeats the already committed refresh without duplicating it.
	c.refresh = (&recordTranslator{store: store, failed: true}).refresh
	if applied, err := c.drain(t.Context()); err != nil || applied != 2 {
		t.Fatalf("replay: %d %v", applied, err)
	}
	if got := messageIDs(t, store, "one"); len(got) != 2 {
		t.Fatalf("replay lost or duplicated content: %v", got)
	}
}

func TestJournalConsumerSkipsMalformedRecords(t *testing.T) {
	c, store, _, dir, _ := journalFixture(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appendRecord(t, dir, "a.jsonl", "one", "after")
	applied, err := c.drain(t.Context())
	if err != nil || applied != 1 {
		t.Fatalf("drain = %d, %v", applied, err)
	}
	if got := messageIDs(t, store, "one"); len(got) != 1 || got[0] != "after" {
		t.Fatalf("a malformed record stalled later events: %v", got)
	}
}

func TestJournalConsumerReportsGapsAndRestartsSegment(t *testing.T) {
	c, store, _, dir, gaps := journalFixture(t)
	appendRecord(t, dir, "a.jsonl", "one", "m1")
	appendRecord(t, dir, "a.jsonl", "one", "m2")
	if _, err := c.drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The segment was replaced by a shorter one: events in between are lost.
	if err := os.Remove(filepath.Join(dir, "a.jsonl")); err != nil {
		t.Fatal(err)
	}
	appendRecord(t, dir, "a.jsonl", "one", "new")
	if _, err := c.drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(*gaps) != 1 || (*gaps)[0] != "a.jsonl" {
		t.Fatalf("gap not reported for repair: %v", *gaps)
	}
	if got := messageIDs(t, store, "one"); len(got) != 3 || got[2] != "new" {
		t.Fatalf("replacement segment was not consumed from its start: %v", got)
	}
}

func TestJournalConsumerPrunesConsumedSegments(t *testing.T) {
	c, _, _, dir, _ := journalFixture(t)
	c.pruneIdle = time.Nanosecond
	appendRecord(t, dir, "a.jsonl", "one", "m1")
	time.Sleep(time.Millisecond)
	if _, err := c.drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := c.prune(t.Context()); err != nil {
		t.Fatal(err)
	}
	if segments, _ := historyjournal.Segments(dir); len(segments) != 0 {
		t.Fatalf("consumed segment retained: %v", segments)
	}
}
