package state

import (
	"path/filepath"
	"testing"
)

func openQueueTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnqueueAndHead_FIFOOrder(t *testing.T) {
	db := openQueueTestDB(t)
	for _, txt := range []string{"first", "second", "third"} {
		if err := db.EnqueueMessage(t.Context(), QueuedMessage{
			ID: "id-" + txt, Platform: "opencode", SessionID: "s1",
			Text: txt, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("EnqueueMessage %q: %v", txt, err)
		}
	}

	// Head is always the oldest (lowest position).
	head, err := db.HeadQueuedMessage(t.Context(), "opencode", "s1")
	if err != nil || head == nil {
		t.Fatalf("HeadQueuedMessage: %v head=%v", err, head)
	}
	if head.Text != "first" {
		t.Fatalf("head = %q, want first", head.Text)
	}

	// Delete head → next oldest surfaces.
	if _, err := db.DeleteQueuedMessage(t.Context(), head.ID); err != nil {
		t.Fatalf("DeleteQueuedMessage: %v", err)
	}
	head, _ = db.HeadQueuedMessage(t.Context(), "opencode", "s1")
	if head == nil || head.Text != "second" {
		t.Fatalf("head after delete = %v, want second", head)
	}
}

// The delivery path must never wildcard on platform: an empty platform
// used to match every platform, so an idle edge from the local instance
// could pop a remote session's head just because the bare ids collided.
func TestDeliveryQueriesDoNotWildcardPlatform(t *testing.T) {
	db := openQueueTestDB(t)
	if err := db.EnqueueMessage(t.Context(), QueuedMessage{
		ID: "id-1", Platform: "r-box:opencode", SessionID: "s1", Text: "hi", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}

	head, err := db.HeadQueuedMessage(t.Context(), "", "s1")
	if err != nil {
		t.Fatalf("HeadQueuedMessage: %v", err)
	}
	if head != nil {
		t.Fatalf("head = %+v, want nil: an empty platform is not an identity", head)
	}
	if n, err := db.CountQueuedMessages(t.Context(), "", "s1"); err != nil || n != 0 {
		t.Fatalf("count = %d (err %v), want 0 for an empty platform", n, err)
	}
	if msgs, err := db.ListQueuedMessages(t.Context(), "", "s1"); err != nil || len(msgs) != 0 {
		t.Fatalf("list = %+v (err %v), want none for an empty platform", msgs, err)
	}

	// Naming the owner resolves it.
	head, err = db.HeadQueuedMessage(t.Context(), "r-box:opencode", "s1")
	if err != nil || head == nil {
		t.Fatalf("HeadQueuedMessage(owner): %v head=%v", err, head)
	}
	if head.Platform != "r-box:opencode" {
		t.Fatalf("head platform = %q, want the stored compound id", head.Platform)
	}
}

// The read-only list endpoint keeps an explicit cross-platform query for
// clients that never learned to send ?platform=. It is the single
// documented exception, and it is never used to drain.
func TestListQueuedMessagesAnyPlatform(t *testing.T) {
	db := openQueueTestDB(t)
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "b", Platform: "r-box:opencode", SessionID: "s1", Text: "b", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "c", Platform: "opencode", SessionID: "s2", Text: "c", CreatedAt: 1})

	got, err := db.ListQueuedMessagesAnyPlatform(t.Context(), "s1")
	if err != nil {
		t.Fatalf("ListQueuedMessagesAnyPlatform: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list = %+v, want both platforms' rows for s1", got)
	}
}

func TestHead_EmptyQueue(t *testing.T) {
	db := openQueueTestDB(t)
	head, err := db.HeadQueuedMessage(t.Context(), "opencode", "nope")
	if err != nil {
		t.Fatalf("HeadQueuedMessage: %v", err)
	}
	if head != nil {
		t.Fatalf("head = %v, want nil for empty queue", head)
	}
}

func TestListQueuedMessages_ScopedAndOrdered(t *testing.T) {
	db := openQueueTestDB(t)
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "b", Platform: "opencode", SessionID: "s1", Text: "b", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "c", Platform: "opencode", SessionID: "s2", Text: "c", CreatedAt: 1})

	got, err := db.ListQueuedMessages(t.Context(), "opencode", "s1")
	if err != nil {
		t.Fatalf("ListQueuedMessages: %v", err)
	}
	if len(got) != 2 || got[0].Text != "a" || got[1].Text != "b" {
		t.Fatalf("list = %+v, want [a b] scoped to s1", got)
	}
}

func TestSessionsWithQueuedMessages_DistinctSessions(t *testing.T) {
	db := openQueueTestDB(t)
	// Two messages for s1, one for s2 → two distinct sessions.
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "b", Platform: "opencode", SessionID: "s1", Text: "b", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "c", Platform: "r-box:opencode", SessionID: "s2", Text: "c", CreatedAt: 1})

	got, err := db.SessionsWithQueuedMessages(t.Context())
	if err != nil {
		t.Fatalf("SessionsWithQueuedMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 distinct: %+v", len(got), got)
	}
	// Each session appears once, with its stored platform.
	seen := map[string]string{}
	for _, q := range got {
		seen[q.SessionID] = q.Platform
	}
	if seen["s1"] != "opencode" || seen["s2"] != "r-box:opencode" {
		t.Fatalf("sessions = %+v, want s1=opencode s2=r-box:opencode", seen)
	}
}

// TestSessionsWithQueuedMessages_SkipsArchived pins that the drain
// sweep ignores archived sessions. Sending into one advances its
// time_updated past archived_at, which auto-unarchives it — the session
// resurrects itself and burns tokens on an abandoned turn.
func TestSessionsWithQueuedMessages_SkipsArchived(t *testing.T) {
	db := openQueueTestDB(t)
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "a", Platform: "opencode", SessionID: "live", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "b", Platform: "opencode", SessionID: "gone", Text: "b", CreatedAt: 1})
	if err := db.ArchiveSession(t.Context(), "opencode", "gone", 100); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	got, err := db.SessionsWithQueuedMessages(t.Context())
	if err != nil {
		t.Fatalf("SessionsWithQueuedMessages: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "live" {
		t.Fatalf("got %+v, want only the unarchived session", got)
	}

	// Unarchiving makes it eligible again.
	if err := db.UnarchiveSession(t.Context(), "opencode", "gone"); err != nil {
		t.Fatalf("UnarchiveSession: %v", err)
	}
	if got, _ := db.SessionsWithQueuedMessages(t.Context()); len(got) != 2 {
		t.Fatalf("got %+v after unarchive, want both sessions", got)
	}
}

// TestQueuedMessageBlocking pins the dead-letter path: a message that
// keeps failing to send must stop blocking the rest of its session's
// queue. The drain is strictly head-first, so one permanently
// unsendable message silently stalled every later message forever.
func TestQueuedMessageBlocking(t *testing.T) {
	db := openQueueTestDB(t)
	for _, id := range []string{"stuck", "next"} {
		if err := db.EnqueueMessage(t.Context(), QueuedMessage{ID: id, Platform: "opencode", SessionID: "s1", Text: id, CreatedAt: 1}); err != nil {
			t.Fatalf("EnqueueMessage %s: %v", id, err)
		}
	}

	// Failures below the limit keep the message at the head for retry.
	for i := 1; i < QueuedMessageAttemptLimit; i++ {
		blocked, err := db.RecordQueuedMessageFailure(t.Context(), "stuck", "boom")
		if err != nil {
			t.Fatalf("RecordQueuedMessageFailure: %v", err)
		}
		if blocked {
			t.Fatalf("message blocked after %d attempts, want %d", i, QueuedMessageAttemptLimit)
		}
		head, _ := db.HeadQueuedMessage(t.Context(), "opencode", "s1")
		if head == nil || head.ID != "stuck" {
			t.Fatalf("head = %v, want stuck while retrying", head)
		}
	}

	blocked, err := db.RecordQueuedMessageFailure(t.Context(), "stuck", "session deleted")
	if err != nil {
		t.Fatalf("RecordQueuedMessageFailure: %v", err)
	}
	if !blocked {
		t.Fatalf("message not blocked after %d attempts", QueuedMessageAttemptLimit)
	}

	// The rest of the queue drains past it.
	head, err := db.HeadQueuedMessage(t.Context(), "opencode", "s1")
	if err != nil {
		t.Fatalf("HeadQueuedMessage: %v", err)
	}
	if head == nil || head.ID != "next" {
		t.Fatalf("head = %v, want next once stuck is blocked", head)
	}

	// The blocked row stays visible to the user, with its reason.
	all, err := db.ListQueuedMessages(t.Context(), "opencode", "s1")
	if err != nil {
		t.Fatalf("ListQueuedMessages: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("listed %d messages, want both", len(all))
	}
	var stuck *QueuedMessage
	for i := range all {
		if all[i].ID == "stuck" {
			stuck = &all[i]
		}
	}
	if stuck == nil || !stuck.Blocked || stuck.LastError != "session deleted" {
		t.Fatalf("blocked row = %+v, want Blocked with its last error", stuck)
	}

	// A session whose whole queue is blocked is not swept.
	if _, err := db.RecordQueuedMessageFailure(t.Context(), "next", "boom"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < QueuedMessageAttemptLimit; i++ {
		if _, err := db.RecordQueuedMessageFailure(t.Context(), "next", "boom"); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := db.SessionsWithQueuedMessages(t.Context()); len(got) != 0 {
		t.Fatalf("got %+v, want no sweepable sessions when all rows are blocked", got)
	}
}

func TestSessionsWithQueuedMessages_EmptyWhenNoQueue(t *testing.T) {
	db := openQueueTestDB(t)
	got, err := db.SessionsWithQueuedMessages(t.Context())
	if err != nil {
		t.Fatalf("SessionsWithQueuedMessages: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d, want 0 for an empty queue", len(got))
	}
}

func TestMoveQueuedMessage_SwapsAndRespectsBoundary(t *testing.T) {
	db := openQueueTestDB(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := db.EnqueueMessage(t.Context(), QueuedMessage{ID: id, Platform: "opencode", SessionID: "s1", Text: id, CreatedAt: 1}); err != nil {
			t.Fatalf("EnqueueMessage %s: %v", id, err)
		}
	}
	order := func() []string {
		msgs, _ := db.ListQueuedMessages(t.Context(), "opencode", "s1")
		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		return ids
	}

	// Move 'b' up → [b a c].
	moved, err := db.MoveQueuedMessage(t.Context(), "b", -1)
	if err != nil || !moved {
		t.Fatalf("Move b up = (%v,%v)", moved, err)
	}
	if got := order(); got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("order = %v, want [b a c]", got)
	}

	// Move head 'b' up again → boundary no-op.
	moved, _ = db.MoveQueuedMessage(t.Context(), "b", -1)
	if moved {
		t.Fatal("moving head up should be a no-op")
	}

	// Invalid direction is rejected.
	if _, err := db.MoveQueuedMessage(t.Context(), "a", 0); err == nil {
		t.Fatal("direction 0 should error")
	}
}

func TestGetQueuedMessageSession(t *testing.T) {
	db := openQueueTestDB(t)
	_ = db.EnqueueMessage(t.Context(), QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi", CreatedAt: 1})
	plat, sess, ok, err := db.GetQueuedMessageSession(t.Context(), "x")
	if err != nil || !ok || plat != "opencode" || sess != "s1" {
		t.Fatalf("Get = (%q,%q,%v,%v), want (opencode,s1,true,nil)", plat, sess, ok, err)
	}
	_, _, ok, _ = db.GetQueuedMessageSession(t.Context(), "ghost")
	if ok {
		t.Fatal("ghost id reported ok")
	}
}

func TestDeleteQueuedMessage_ReportsMiss(t *testing.T) {
	db := openQueueTestDB(t)
	ok, err := db.DeleteQueuedMessage(t.Context(), "ghost")
	if err != nil {
		t.Fatalf("DeleteQueuedMessage: %v", err)
	}
	if ok {
		t.Fatal("deleting a non-existent id reported a hit")
	}
}
