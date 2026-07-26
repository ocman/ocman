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
		if err := db.EnqueueMessage(QueuedMessage{
			ID: "id-" + txt, Platform: "opencode", SessionID: "s1",
			Text: txt, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("EnqueueMessage %q: %v", txt, err)
		}
	}

	// Head is always the oldest (lowest position).
	head, err := db.HeadQueuedMessage("opencode", "s1")
	if err != nil || head == nil {
		t.Fatalf("HeadQueuedMessage: %v head=%v", err, head)
	}
	if head.Text != "first" {
		t.Fatalf("head = %q, want first", head.Text)
	}

	// Delete head → next oldest surfaces.
	if _, err := db.DeleteQueuedMessage(head.ID); err != nil {
		t.Fatalf("DeleteQueuedMessage: %v", err)
	}
	head, _ = db.HeadQueuedMessage("opencode", "s1")
	if head == nil || head.Text != "second" {
		t.Fatalf("head after delete = %v, want second", head)
	}
}

func TestHead_EmptyPlatformMatchesAny(t *testing.T) {
	db := openQueueTestDB(t)
	if err := db.EnqueueMessage(QueuedMessage{
		ID: "id-1", Platform: "r-box:opencode", SessionID: "s1", Text: "hi", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("EnqueueMessage: %v", err)
	}
	// The idle-driven flush only knows the session id.
	head, err := db.HeadQueuedMessage("", "s1")
	if err != nil || head == nil {
		t.Fatalf("HeadQueuedMessage empty platform: %v head=%v", err, head)
	}
	if head.Platform != "r-box:opencode" {
		t.Fatalf("head platform = %q, want the stored compound id", head.Platform)
	}
}

func TestHead_EmptyQueue(t *testing.T) {
	db := openQueueTestDB(t)
	head, err := db.HeadQueuedMessage("opencode", "nope")
	if err != nil {
		t.Fatalf("HeadQueuedMessage: %v", err)
	}
	if head != nil {
		t.Fatalf("head = %v, want nil for empty queue", head)
	}
}

func TestListQueuedMessages_ScopedAndOrdered(t *testing.T) {
	db := openQueueTestDB(t)
	_ = db.EnqueueMessage(QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(QueuedMessage{ID: "b", Platform: "opencode", SessionID: "s1", Text: "b", CreatedAt: 1})
	_ = db.EnqueueMessage(QueuedMessage{ID: "c", Platform: "opencode", SessionID: "s2", Text: "c", CreatedAt: 1})

	got, err := db.ListQueuedMessages("opencode", "s1")
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
	_ = db.EnqueueMessage(QueuedMessage{ID: "a", Platform: "opencode", SessionID: "s1", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(QueuedMessage{ID: "b", Platform: "opencode", SessionID: "s1", Text: "b", CreatedAt: 1})
	_ = db.EnqueueMessage(QueuedMessage{ID: "c", Platform: "r-box:opencode", SessionID: "s2", Text: "c", CreatedAt: 1})

	got, err := db.SessionsWithQueuedMessages()
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
	_ = db.EnqueueMessage(QueuedMessage{ID: "a", Platform: "opencode", SessionID: "live", Text: "a", CreatedAt: 1})
	_ = db.EnqueueMessage(QueuedMessage{ID: "b", Platform: "opencode", SessionID: "gone", Text: "b", CreatedAt: 1})
	if err := db.ArchiveSession("opencode", "gone", 100); err != nil {
		t.Fatalf("ArchiveSession: %v", err)
	}

	got, err := db.SessionsWithQueuedMessages()
	if err != nil {
		t.Fatalf("SessionsWithQueuedMessages: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "live" {
		t.Fatalf("got %+v, want only the unarchived session", got)
	}

	// Unarchiving makes it eligible again.
	if err := db.UnarchiveSession("opencode", "gone"); err != nil {
		t.Fatalf("UnarchiveSession: %v", err)
	}
	if got, _ := db.SessionsWithQueuedMessages(); len(got) != 2 {
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
		if err := db.EnqueueMessage(QueuedMessage{ID: id, Platform: "opencode", SessionID: "s1", Text: id, CreatedAt: 1}); err != nil {
			t.Fatalf("EnqueueMessage %s: %v", id, err)
		}
	}

	// Failures below the limit keep the message at the head for retry.
	for i := 1; i < QueuedMessageAttemptLimit; i++ {
		blocked, err := db.RecordQueuedMessageFailure("stuck", "boom")
		if err != nil {
			t.Fatalf("RecordQueuedMessageFailure: %v", err)
		}
		if blocked {
			t.Fatalf("message blocked after %d attempts, want %d", i, QueuedMessageAttemptLimit)
		}
		head, _ := db.HeadQueuedMessage("opencode", "s1")
		if head == nil || head.ID != "stuck" {
			t.Fatalf("head = %v, want stuck while retrying", head)
		}
	}

	blocked, err := db.RecordQueuedMessageFailure("stuck", "session deleted")
	if err != nil {
		t.Fatalf("RecordQueuedMessageFailure: %v", err)
	}
	if !blocked {
		t.Fatalf("message not blocked after %d attempts", QueuedMessageAttemptLimit)
	}

	// The rest of the queue drains past it.
	head, err := db.HeadQueuedMessage("opencode", "s1")
	if err != nil {
		t.Fatalf("HeadQueuedMessage: %v", err)
	}
	if head == nil || head.ID != "next" {
		t.Fatalf("head = %v, want next once stuck is blocked", head)
	}

	// The blocked row stays visible to the user, with its reason.
	all, err := db.ListQueuedMessages("opencode", "s1")
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
	if _, err := db.RecordQueuedMessageFailure("next", "boom"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < QueuedMessageAttemptLimit; i++ {
		if _, err := db.RecordQueuedMessageFailure("next", "boom"); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := db.SessionsWithQueuedMessages(); len(got) != 0 {
		t.Fatalf("got %+v, want no sweepable sessions when all rows are blocked", got)
	}
}

func TestSessionsWithQueuedMessages_EmptyWhenNoQueue(t *testing.T) {
	db := openQueueTestDB(t)
	got, err := db.SessionsWithQueuedMessages()
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
		if err := db.EnqueueMessage(QueuedMessage{ID: id, Platform: "opencode", SessionID: "s1", Text: id, CreatedAt: 1}); err != nil {
			t.Fatalf("EnqueueMessage %s: %v", id, err)
		}
	}
	order := func() []string {
		msgs, _ := db.ListQueuedMessages("opencode", "s1")
		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		return ids
	}

	// Move 'b' up → [b a c].
	moved, err := db.MoveQueuedMessage("b", -1)
	if err != nil || !moved {
		t.Fatalf("Move b up = (%v,%v)", moved, err)
	}
	if got := order(); got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("order = %v, want [b a c]", got)
	}

	// Move head 'b' up again → boundary no-op.
	moved, _ = db.MoveQueuedMessage("b", -1)
	if moved {
		t.Fatal("moving head up should be a no-op")
	}

	// Invalid direction is rejected.
	if _, err := db.MoveQueuedMessage("a", 0); err == nil {
		t.Fatal("direction 0 should error")
	}
}

func TestGetQueuedMessageSession(t *testing.T) {
	db := openQueueTestDB(t)
	_ = db.EnqueueMessage(QueuedMessage{ID: "x", Platform: "opencode", SessionID: "s1", Text: "hi", CreatedAt: 1})
	plat, sess, ok, err := db.GetQueuedMessageSession("x")
	if err != nil || !ok || plat != "opencode" || sess != "s1" {
		t.Fatalf("Get = (%q,%q,%v,%v), want (opencode,s1,true,nil)", plat, sess, ok, err)
	}
	_, _, ok, _ = db.GetQueuedMessageSession("ghost")
	if ok {
		t.Fatal("ghost id reported ok")
	}
}

func TestDeleteQueuedMessage_ReportsMiss(t *testing.T) {
	db := openQueueTestDB(t)
	ok, err := db.DeleteQueuedMessage("ghost")
	if err != nil {
		t.Fatalf("DeleteQueuedMessage: %v", err)
	}
	if ok {
		t.Fatal("deleting a non-existent id reported a hit")
	}
}

func TestEnqueueClaimedChildResultIsAtomicAndNotReplayable(t *testing.T) {
	db := openQueueTestDB(t)
	if err := db.InsertChildSession(ChildSession{
		ID: "child-1", Platform: "opencode", ParentSessionID: "parent-1",
		Intent: "task", Status: "completed", CreatedAt: 1, ResultDelivery: "async_queueing",
	}); err != nil {
		t.Fatal(err)
	}
	msg := QueuedMessage{ID: "child-result:1", Platform: "opencode", SessionID: "parent-1", Text: "done", CreatedAt: 2}

	queued, err := db.EnqueueClaimedChildResult("child-1", msg)
	if err != nil || !queued {
		t.Fatalf("first enqueue = %v, %v", queued, err)
	}
	child, _ := db.GetChildSession("child-1")
	if child.ResultDelivery != "delivered" {
		t.Fatalf("delivery = %q, want delivered", child.ResultDelivery)
	}
	if deleted, err := db.DeleteQueuedMessage(msg.ID); err != nil || !deleted {
		t.Fatalf("draining queued result = %v, %v", deleted, err)
	}
	queued, err = db.EnqueueClaimedChildResult("child-1", msg)
	if err != nil || queued {
		t.Fatalf("replay after drain = %v, %v; want no-op", queued, err)
	}
	if messages, _ := db.ListQueuedMessages("opencode", "parent-1"); len(messages) != 0 {
		t.Fatalf("replayed %d child results", len(messages))
	}
}

func TestEnqueueClaimedChildResultRollsBackFailedQueueInsert(t *testing.T) {
	db := openQueueTestDB(t)
	if err := db.InsertChildSession(ChildSession{
		ID: "child-1", Platform: "opencode", ParentSessionID: "parent-1",
		Intent: "task", Status: "completed", CreatedAt: 1, ResultDelivery: "async_queueing",
	}); err != nil {
		t.Fatal(err)
	}
	msg := QueuedMessage{ID: "collision", Platform: "opencode", SessionID: "parent-1", Text: "done", CreatedAt: 2}
	if err := db.EnqueueMessage(QueuedMessage{ID: msg.ID, Platform: "opencode", SessionID: "other", Text: "existing", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if queued, err := db.EnqueueClaimedChildResult("child-1", msg); err == nil || queued {
		t.Fatalf("colliding enqueue = %v, %v; want rollback", queued, err)
	}
	child, _ := db.GetChildSession("child-1")
	if child.ResultDelivery != "async_queueing" {
		t.Fatalf("delivery after rollback = %q", child.ResultDelivery)
	}
	if _, err := db.DeleteQueuedMessage(msg.ID); err != nil {
		t.Fatal(err)
	}
	if queued, err := db.EnqueueClaimedChildResult("child-1", msg); err != nil || !queued {
		t.Fatalf("recovered enqueue = %v, %v", queued, err)
	}
}
