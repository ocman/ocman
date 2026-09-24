package state

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func outboxDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "state.db"))
	requirePluginOK(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func appendReply(t *testing.T, d *DB, key PluginConversationKey, operation, text string) int64 {
	t.Helper()
	id, err := d.AppendPluginConversationReply(t.Context(), key, operation, text)
	requirePluginOK(t, err)
	return id
}

// clearBackoff makes a waiting delivery due now, standing in for the passage
// of time without making the test wait for it.
func clearBackoff(t *testing.T, d *DB, id int64) {
	t.Helper()
	_, err := d.db.ExecContext(t.Context(), `UPDATE plugin_conversation_outbox SET next_attempt_at=0 WHERE id=?`, id)
	requirePluginOK(t, err)
}

func claim(t *testing.T, d *DB) []PluginConversationDelivery {
	t.Helper()
	claimed, err := d.ClaimPluginConversationReplies(t.Context(), 10)
	requirePluginOK(t, err)
	return claimed
}

// TestConversationOutboxAppendIsIdempotent covers the append-side guarantee:
// one completed turn produces one delivery, and the receipt outlives the
// delivery so a later idle edge for the same turn cannot re-post it.
func TestConversationOutboxAppendIsIdempotent(t *testing.T) {
	d := outboxDB(t)
	key := conversationKey("T1", "C1:1.0")
	id := appendReply(t, d, key, "ses-1:m1", "done")
	if id == 0 {
		t.Fatal("the first append must queue a delivery")
	}
	if appendReply(t, d, key, "ses-1:m1", "done") != 0 {
		t.Fatal("a repeated idle edge queued a second delivery")
	}
	claimed := claim(t, d)
	if len(claimed) != 1 {
		t.Fatalf("claimed %d deliveries", len(claimed))
	}
	requirePluginOK(t, d.AckPluginConversationReply(t.Context(), claimed[0].ID))
	if claimed[0].ID != id {
		t.Fatalf("claimed delivery %d, appended %d", claimed[0].ID, id)
	}
	if appendReply(t, d, key, "ses-1:m1", "done") != 0 {
		t.Fatal("an acknowledged delivery was re-queued after its receipt")
	}
	if rest := claim(t, d); len(rest) != 0 {
		t.Fatalf("an acknowledged delivery was claimed again: %+v", rest)
	}
	// Acknowledging twice is not an error the caller can act on, but it must
	// not silently resurrect the row either.
	if err := d.AckPluginConversationReply(t.Context(), claimed[0].ID); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("second ack: %v", err)
	}
	for _, bad := range []struct {
		key             PluginConversationKey
		operation, text string
	}{
		{key: PluginConversationKey{}, operation: "o", text: "t"},
		{key: key, operation: "", text: "t"},
		{key: key, operation: "o", text: ""},
	} {
		if _, err := d.AppendPluginConversationReply(t.Context(), bad.key, bad.operation, bad.text); !errors.Is(err, ErrPluginInvalid) {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
}

// TestConversationOutboxOrdersWithinGroupOnly is the ordering contract: a reply
// never overtakes an earlier one in the same conversation, and an unrelated
// conversation is never held behind it.
func TestConversationOutboxOrdersWithinGroupOnly(t *testing.T) {
	d := outboxDB(t)
	first := conversationKey("T1", "C1:1.0")
	second := conversationKey("T1", "C2:2.0")
	other := conversationKey("T2", "C1:1.0")
	appendReply(t, d, first, "ses-1:m1", "first")
	appendReply(t, d, first, "ses-1:m2", "second")
	appendReply(t, d, second, "ses-2:m1", "other thread")
	appendReply(t, d, other, "ses-3:m1", "other workspace")

	claimed := claim(t, d)
	if len(claimed) != 3 {
		t.Fatalf("claimed %d, want one head per conversation", len(claimed))
	}
	if claimed[0].Text != "first" || claimed[0].Sequence >= claimed[1].Sequence {
		t.Fatalf("claims are not ordered by sequence: %+v", claimed)
	}
	// Same thread id in another workspace is a different ordering group.
	if claimed[1].Group() == claimed[2].Group() {
		t.Fatalf("unrelated conversations share an ordering group: %+v", claimed)
	}
	// The conversation's second reply stays held until the first is settled.
	requirePluginOK(t, d.AckPluginConversationReply(t.Context(), claimed[0].ID))
	next := claim(t, d)
	if len(next) != 3 {
		t.Fatalf("claimed %d after the first ack", len(next))
	}
	if next[0].Text != "second" {
		t.Fatalf("the conversation advanced out of order: %+v", next)
	}
}

// TestConversationOutboxBackoffAndDeadLetter walks a poison reply to its
// terminal state: bounded retries, then a dead letter that blocks its own
// conversation and nobody else's until a human decides.
func TestConversationOutboxBackoffAndDeadLetter(t *testing.T) {
	d := outboxDB(t)
	key := conversationKey("T1", "C1:1.0")
	unrelated := conversationKey("T1", "C2:2.0")
	appendReply(t, d, key, "ses-1:m1", "poison")
	appendReply(t, d, key, "ses-1:m2", "held behind it")
	appendReply(t, d, unrelated, "ses-2:m1", "unaffected")
	head := claim(t, d)[0]

	dead, err := d.FailPluginConversationReply(t.Context(), head.ID, "unavailable", time.Minute, 3)
	requirePluginOK(t, err)
	if dead {
		t.Fatal("one failure must not be terminal")
	}
	// A backoff that has not elapsed holds only its own conversation.
	claimed := claim(t, d)
	if len(claimed) != 1 || claimed[0].Text != "unaffected" {
		t.Fatalf("a retrying conversation blocked another: %+v", claimed)
	}

	// Once the backoff elapses the same delivery is retried, until the attempt
	// cap turns it into a dead letter.
	for attempt := 1; !dead; attempt++ {
		clearBackoff(t, d, head.ID)
		claimed := claim(t, d)
		if len(claimed) == 0 || claimed[0].ID != head.ID || claimed[0].Attempts != attempt {
			t.Fatalf("attempt %d did not reclaim the head: %+v", attempt, claimed)
		}
		if attempt > 3 {
			t.Fatal("retries are not bounded")
		}
		dead, err = d.FailPluginConversationReply(t.Context(), head.ID, "unavailable", time.Minute, 3)
		requirePluginOK(t, err)
	}

	status, err := d.PluginConversationBacklogStatus(t.Context(), key.PluginID)
	requirePluginOK(t, err)
	if status.Dead != 1 || len(status.DeadLetters) != 1 || status.DeadLetters[0].ID != head.ID {
		t.Fatalf("dead letter is not visible: %+v", status)
	}
	if status.DeadLetters[0].LastError != "unavailable" || status.DeadLetters[0].ThreadID != key.ThreadID {
		t.Fatalf("dead letter lacks actionable detail: %+v", status.DeadLetters[0])
	}
	// The conversation stops advancing rather than delivering out of order.
	for _, claimed := range claim(t, d) {
		if claimed.Key == key {
			t.Fatalf("a reply overtook a dead letter in its conversation: %+v", claimed)
		}
	}

	// Controls are plugin-scoped and only apply to a dead letter.
	if err := d.RetryPluginConversationReply(t.Context(), "org.example.other", head.ID); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("another plugin retried a dead letter: %v", err)
	}
	requirePluginOK(t, d.RetryPluginConversationReply(t.Context(), key.PluginID, head.ID))
	if claimed := claim(t, d); len(claimed) != 2 || claimed[0].ID != head.ID || claimed[0].Attempts != 0 {
		t.Fatalf("retry did not return the delivery to its conversation head: %+v", claimed)
	}
	if err := d.DiscardPluginConversationReply(t.Context(), key.PluginID, head.ID); !errors.Is(err, ErrPluginNotFound) {
		t.Fatalf("discarded a pending delivery: %v", err)
	}
	// Discarding the dead letter lets the conversation continue.
	dead, err = d.FailPluginConversationReply(t.Context(), head.ID, "unavailable", 0, 1)
	requirePluginOK(t, err)
	if !dead {
		t.Fatal("a retried delivery must be able to die again")
	}
	requirePluginOK(t, d.DiscardPluginConversationReply(t.Context(), key.PluginID, head.ID))
	claimed = claim(t, d)
	if len(claimed) != 2 || claimed[0].Text != "held behind it" {
		t.Fatalf("discard did not unblock the conversation: %+v", claimed)
	}
}

// TestConversationOutboxPressureSignalsPause covers the producer-side limits:
// reaching a cap is reported as paused, never as a dropped reply.
func TestConversationOutboxPressureSignalsPause(t *testing.T) {
	d := outboxDB(t)
	key := conversationKey("T1", "C1:1.0")
	requirePluginOK(t, d.PluginConversationBacklogFull(t.Context(), key.PluginID))
	for i := range PluginConversationOutboxMaxRows {
		appendReply(t, d, key, "ses-1:m"+strconv.Itoa(i), "reply")
	}
	if err := d.PluginConversationBacklogFull(t.Context(), key.PluginID); !errors.Is(err, ErrPluginConversationBacklogFull) {
		t.Fatalf("the row cap did not pause production: %v", err)
	}
	status, err := d.PluginConversationBacklogStatus(t.Context(), key.PluginID)
	requirePluginOK(t, err)
	if !status.Paused || status.Pending != PluginConversationOutboxMaxRows || status.Bytes == 0 {
		t.Fatalf("status %+v", status)
	}
	if status.MaxRows != PluginConversationOutboxMaxRows || status.MaxBytes != PluginConversationOutboxMaxBytes {
		t.Fatalf("status must publish the limits it enforces: %+v", status)
	}
	if status.OldestUnsent == 0 {
		t.Fatal("status must show how long work has been waiting")
	}
	// Another plugin's backlog is independent.
	requirePluginOK(t, d.PluginConversationBacklogFull(t.Context(), "org.example.other"))

	// A delivered reply frees capacity; a receipt is not backlog.
	head := claim(t, d)[0]
	requirePluginOK(t, d.AckPluginConversationReply(t.Context(), head.ID))
	requirePluginOK(t, d.PluginConversationBacklogFull(t.Context(), key.PluginID))
}

// TestConversationOutboxPrunesReceipts keeps the receipt table bounded without
// touching work that is still owed.
func TestConversationOutboxPrunesReceipts(t *testing.T) {
	d := outboxDB(t)
	key := conversationKey("T1", "C1:1.0")
	appendReply(t, d, key, "ses-1:m1", "delivered")
	appendReply(t, d, key, "ses-1:m2", "still owed")
	head := claim(t, d)[0]
	requirePluginOK(t, d.AckPluginConversationReply(t.Context(), head.ID))
	requirePluginOK(t, d.PrunePluginConversationOutbox(t.Context()))
	if appendReply(t, d, key, "ses-1:m1", "delivered") != 0 {
		t.Fatal("a fresh receipt was pruned")
	}

	// Age the receipt past its retention.
	_, err := d.db.ExecContext(t.Context(), `UPDATE plugin_conversation_outbox SET updated_at=? WHERE id=?`,
		time.Now().Add(-2*pluginConversationDoneRetention).UnixMilli(), head.ID)
	requirePluginOK(t, err)
	requirePluginOK(t, d.PrunePluginConversationOutbox(t.Context()))
	status, err := d.PluginConversationBacklogStatus(t.Context(), key.PluginID)
	requirePluginOK(t, err)
	if status.Pending != 1 {
		t.Fatalf("pruning touched undelivered work: %+v", status)
	}
}

// TestConversationOutboxKeepsLatestTurnReceipts is the regression for replies
// reposted daily: reconciliation re-derives the thread's latest turn every
// tick, so pruning that turn's receipt re-appended and re-posted the same
// answer once its retention lapsed. Only superseded turns may be pruned.
func TestConversationOutboxKeepsLatestTurnReceipts(t *testing.T) {
	d := outboxDB(t)
	key := conversationKey("T1", "C1:1.0")
	for _, op := range []string{"ses-1:m1", "ses-1:outcome:error:m2", "ses-1:m2"} {
		appendReply(t, d, key, op, "text")
		requirePluginOK(t, d.AckPluginConversationReply(t.Context(), claim(t, d)[0].ID))
	}
	_, err := d.db.ExecContext(t.Context(), `UPDATE plugin_conversation_outbox SET updated_at=?`,
		time.Now().Add(-2*pluginConversationDoneRetention).UnixMilli())
	requirePluginOK(t, err)
	requirePluginOK(t, d.PrunePluginConversationOutbox(t.Context()))

	for op, want := range map[string]bool{"ses-1:m2": false, "ses-1:outcome:error:m2": false, "ses-1:m1": true} {
		if got := appendReply(t, d, key, op, "text") != 0; got != want {
			t.Errorf("%s re-appended=%v, want %v", op, got, want)
		}
	}
}

func TestConversationOutboxRejectsInvalidReads(t *testing.T) {
	d := outboxDB(t)
	if _, err := d.PluginConversationBacklogStatus(t.Context(), ""); !errors.Is(err, ErrPluginInvalid) {
		t.Fatalf("accepted an empty plugin id: %v", err)
	}
	if claimed, err := d.ClaimPluginConversationReplies(t.Context(), 0); claimed != nil || err != nil {
		t.Fatalf("%+v %v", claimed, err)
	}
}
