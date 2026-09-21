package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

func conversationOutboxKey(thread string) state.PluginConversationKey {
	return state.PluginConversationKey{
		PluginID: conversationPluginDescription().ID, AccountID: conversationTestAccount, ThreadID: thread,
	}
}

// appendReply records one completed reply the way a settled turn does.
func (f *conversationFixture) appendReply(t *testing.T, thread, operation, text string) int64 {
	t.Helper()
	id, err := f.s.stateDB.AppendPluginConversationReply(t.Context(), conversationOutboxKey(thread), operation, text)
	if err != nil || id == 0 {
		t.Fatalf("appending %q: %v %v", operation, id, err)
	}
	return id
}

// pump runs one delivery pass to completion, so tests never depend on the
// background ticker.
func (f *conversationFixture) pump(t *testing.T, s *Server) {
	t.Helper()
	s.pumpConversationOutbox(t.Context())
	if err := s.conversationDeliveryWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (f *conversationFixture) backlog(t *testing.T) state.PluginConversationBacklog {
	t.Helper()
	return f.backlogFor(t, conversationPluginDescription().ID)
}

func (f *conversationFixture) backlogFor(t *testing.T, pluginID string) state.PluginConversationBacklog {
	t.Helper()
	status, err := f.s.stateDB.PluginConversationBacklogStatus(t.Context(), pluginID)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

// TestConversationReplyReplayedAcrossCrashBoundaries covers both sides of the
// send/acknowledge boundary, and states the guarantee: a reply recorded before
// a crash is delivered afterwards, and a reply whose acknowledgment was lost is
// delivered a second time with the same operation id. Delivery is
// at-least-once; suppressing that repeat is the provider adapter's job.
func TestConversationReplyReplayedAcrossCrashBoundaries(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	// Crash before the send: the completed reply is durable, nothing was posted.
	id := f.appendReply(t, conversationTestThread, "ses-chat:m2", "recorded before the crash")
	if f.replies() != "" {
		t.Fatalf("a recorded reply must not be posted by the append: %q", f.replies())
	}
	// A host with no memory of the reply still delivers it.
	f.pump(t, f.restart(t))
	first := fmt.Sprintf("reply\t%s\t%s\trecorded before the crash\n", conversationTestThread, conversationOutboxOperation(id))
	if f.replies() != first {
		t.Fatalf("replay after a crash: %q, want %q", f.replies(), first)
	}
	if status := f.backlog(t); status.Pending != 0 || status.Dead != 0 {
		t.Fatalf("a delivered reply stayed in the backlog: %+v", status)
	}

	// Crash after the send, before the acknowledgment: the delivery is still
	// pending, so it is replayed.
	lost := f.appendReply(t, conversationTestThread, "ses-chat:m3", "acknowledgment lost")
	if err := f.s.conversations().Reply(t.Context(), conversationPluginDescription().ID,
		conversationOutboxOperation(lost), plugins.ConversationReply{
			AccountID: conversationTestAccount, ThreadID: conversationTestThread, Text: "acknowledgment lost",
		}); err != nil {
		t.Fatal(err)
	}
	f.pump(t, f.s)
	repeated := fmt.Sprintf("reply\t%s\t%s\tacknowledgment lost\n", conversationTestThread, conversationOutboxOperation(lost))
	if got := f.replies(); got != first+repeated+repeated {
		t.Fatalf("an unacknowledged reply must be replayed with a stable identity: %q", got)
	}
	if status := f.backlog(t); status.Pending != 0 {
		t.Fatalf("the replay did not settle the delivery: %+v", status)
	}
}

// TestConversationDeliveryIsolatesFailingConversation is the ordering contract
// under failure: a conversation whose reply cannot be delivered keeps its own
// later replies waiting, in order, and holds up nobody else's.
func TestConversationDeliveryIsolatesFailingConversation(t *testing.T) {
	f := newConversationFixture(t)
	f.failThread = conversationTestThread
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	const healthy = "slackC2:1700000000.000200"

	f.appendReply(t, conversationTestThread, "ses-chat:m2", "first of the stuck thread")
	f.appendReply(t, conversationTestThread, "ses-chat:m3", "second of the stuck thread")
	f.appendReply(t, healthy, "ses-other:m1", "unrelated thread")
	f.pump(t, f.s)

	replies := f.replies()
	if !strings.Contains(replies, "reply\t"+healthy+"\t") {
		t.Fatalf("an unrelated conversation was blocked by a failing one: %q", replies)
	}
	if !strings.Contains(replies, "fail\t"+conversationTestThread+"\t") {
		t.Fatalf("the failing conversation was never attempted: %q", replies)
	}
	if strings.Contains(replies, "second of the stuck thread") {
		t.Fatalf("a reply overtook an undelivered earlier one: %q", replies)
	}
	status := f.backlog(t)
	if status.Pending != 2 || status.Retrying != 1 || status.Dead != 0 {
		t.Fatalf("backlog after one failure: %+v", status)
	}

	// The retry is scheduled, not immediate: a second pass does not hammer the
	// provider while the backoff is outstanding.
	before := f.replies()
	f.pump(t, f.s)
	if f.replies() != before {
		t.Fatalf("a delivery was retried before its backoff elapsed: %q", f.replies())
	}
}

// TestConversationDeadLetterControls covers the visible terminal state and its
// two explicit decisions, over the real HTTP surface.
func TestConversationDeadLetterControls(t *testing.T) {
	f := newConversationFixture(t)
	f.failThread = conversationTestThread
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	id := conversationPluginDescription().ID

	poison := f.appendReply(t, conversationTestThread, "ses-chat:m2", "poison")
	f.appendReply(t, conversationTestThread, "ses-chat:m3", "held behind the poison")
	// Bounded retries: every attempt is immediately due, so the cap is reached
	// without waiting out a real backoff.
	for range conversationMaxAttempts {
		f.pump(t, f.s)
		if _, err := f.s.stateDB.FailPluginConversationReply(t.Context(), poison, "unavailable", 0, conversationMaxAttempts); err != nil {
			t.Fatal(err)
		}
	}
	var status state.PluginConversationBacklog
	if err := json.Unmarshal([]byte(f.call(t, "GET", "/"+id+"/conversations", "", 200)), &status); err != nil {
		t.Fatal(err)
	}
	if status.Dead != 1 || len(status.DeadLetters) != 1 || status.DeadLetters[0].ID != poison {
		t.Fatalf("the dead letter is not actionable from the UI: %+v", status)
	}
	if status.DeadLetters[0].LastError == "" || status.DeadLetters[0].Attempts < conversationMaxAttempts {
		t.Fatalf("dead letter %+v", status.DeadLetters[0])
	}

	// A control needs a delivery to act on.
	f.call(t, "POST", "/"+id+"/conversations/discard", `{}`, 400)
	f.call(t, "POST", "/"+id+"/conversations/retry", fmt.Sprintf(`{"deliveryId":%d}`, poison+1000), 404)

	// Retry returns it to the head of its conversation, where it fails again.
	f.call(t, "POST", "/"+id+"/conversations/retry", fmt.Sprintf(`{"deliveryId":%d}`, poison), 200)
	if got := f.backlog(t); got.Dead != 0 || got.Pending != 2 {
		t.Fatalf("retry did not requeue the dead letter: %+v", got)
	}
	f.pump(t, f.s)
	if _, err := f.s.stateDB.FailPluginConversationReply(t.Context(), poison, "unavailable", 0, 1); err != nil {
		t.Fatal(err)
	}

	// Discarding it unblocks the conversation's next reply.
	body := f.call(t, "POST", "/"+id+"/conversations/discard", fmt.Sprintf(`{"deliveryId":%d}`, poison), 200)
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(err)
	}
	if status.Dead != 0 || status.Pending != 1 {
		t.Fatalf("discard left the conversation blocked: %+v", status)
	}
	f.pump(t, f.s)
	if !strings.Contains(f.replies(), "held behind the poison") {
		t.Fatalf("the next reply never went out: %q", f.replies())
	}
}

// TestConversationDeliveryControlsRequireLocalhost keeps the retry and discard
// decisions on the same privileged footing as every other plugin control: a
// remote client can neither read the backlog nor act on it.
func TestConversationDeliveryControlsRequireLocalhost(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	id := conversationPluginDescription().ID
	poison := f.appendReply(t, conversationTestThread, "ses-chat:m2", "poison")
	if _, err := f.s.stateDB.FailPluginConversationReply(t.Context(), poison, "unavailable", 0, 1); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/conversations/retry", "/conversations/discard"} {
		r := httptest.NewRequest(http.MethodPost, "http://localhost:8228/api/plugins/"+id+path,
			strings.NewReader(fmt.Sprintf(`{"deliveryId":%d}`, poison)))
		r.RemoteAddr = "203.0.113.7:4321"
		r.AddCookie(f.cookie)
		w := httptest.NewRecorder()
		f.mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s from a non-loopback client: %d", path, w.Code)
		}
	}
	if got := f.backlog(t); got.Dead != 1 {
		t.Fatalf("a refused control changed the backlog: %+v", got)
	}
	// A GET is unauthenticated-safe but still requires the session cookie.
	r := httptest.NewRequest(http.MethodGet, "http://localhost:8228/api/plugins/"+id+"/conversations", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("backlog status without a session: %d", w.Code)
	}
}

// TestConversationIntakePausesWhenBacklogIsFull covers the pressure rule: at
// the limit the host stops admitting new conversation work instead of dropping
// replies it already owes, and the paused message is not consumed, so the
// provider's redelivery is accepted once there is room.
func TestConversationIntakePausesWhenBacklogIsFull(t *testing.T) {
	// The backlog is filled with replies for a conversation whose provider
	// rejects them, so the delivery pump cannot drain it while the test runs.
	const stuck = "slackC9:1700000000.000900"
	f := newConversationFixture(t)
	f.failThread = stuck
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	f.settle(t)

	var first int64
	for i := range state.PluginConversationOutboxMaxRows {
		id, err := f.s.stateDB.AppendPluginConversationReply(t.Context(),
			conversationOutboxKey(stuck), fmt.Sprintf("ses-stuck:m%d", i), "backlog")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		}
	}
	if status := f.backlog(t); !status.Paused {
		t.Fatalf("the backlog is not reporting the pause: %+v", status)
	}

	paused := conversationMessage("while paused")
	err := f.deliver(t, paused)
	if !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("a paused intake must refuse the message: %v", err)
	}
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 1 {
		t.Fatalf("a message was admitted while paused: %v", prompts)
	}

	// Free one slot: the same message, redelivered by the provider, is accepted
	// rather than treated as a duplicate of the refused one.
	if err := f.s.stateDB.AckPluginConversationReply(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := f.deliver(t, paused); err != nil {
		t.Fatalf("redelivery after the pause: %v", err)
	}
	if prompts := f.awaitPrompts(t, 2); prompts[1] != "while paused" {
		t.Fatalf("prompts %v", prompts)
	}
}

func TestConversationRetryDelayIsBounded(t *testing.T) {
	if got := conversationRetryDelay(0); got != conversationRetryBase {
		t.Fatalf("first retry waits %s", got)
	}
	previous := time.Duration(0)
	for attempts := range 20 {
		got := conversationRetryDelay(attempts)
		if got < previous || got > conversationRetryMax {
			t.Fatalf("attempt %d: %s after %s, ceiling %s", attempts, got, previous, conversationRetryMax)
		}
		previous = got
	}
	if conversationRetryDelay(19) != conversationRetryMax {
		t.Fatal("the backoff never reaches its ceiling")
	}
}

func TestConversationFailureReasonStaysOpaque(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{err: &plugins.WireError{Category: plugins.ErrorUnavailable}, want: "unavailable"},
		{err: context.DeadlineExceeded, want: "deadline_exceeded"},
		{err: context.Canceled, want: "cancelled"},
		{err: plugins.ErrUnavailable, want: "unavailable"},
		{err: state.ErrPluginState, want: "host state error"},
		{err: errors.New("provider said: token xoxb-secret is bad"), want: "internal"},
	} {
		if got := conversationFailureReason(tc.err); got != tc.want {
			t.Fatalf("%v: %q, want %q", tc.err, got, tc.want)
		}
	}
}
