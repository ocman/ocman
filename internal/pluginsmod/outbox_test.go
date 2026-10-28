package pluginsmod

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestOutboxRetryDelayIsBounded(t *testing.T) {
	if got := outboxRetryDelay(0); got != outboxRetryBase {
		t.Fatalf("first retry waits %s", got)
	}
	previous := time.Duration(0)
	for attempts := range 20 {
		got := outboxRetryDelay(attempts)
		if got < previous || got > outboxRetryMax {
			t.Fatalf("attempt %d: %s after %s, ceiling %s", attempts, got, previous, outboxRetryMax)
		}
		previous = got
	}
	if outboxRetryDelay(19) != outboxRetryMax {
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

// TestConversationStaleClaimIsNotResent covers a pump whose claim was read
// before a concurrent delivery settled the row: the stale snapshot must not
// be attempted again. Here the concurrent attempt is a recorded failure; a
// resend would fail too (the plugin is not installed) and add an attempt.
func TestConversationStaleClaimIsNotResent(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := NewModule(Deps{Store: store})
	key := state.PluginConversationKey{PluginID: "org.example.chatops", AccountID: "T1", ThreadID: "C1:1"}
	id, err := store.AppendPluginConversationReply(t.Context(), key, "ses:m1", "only once")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.ClaimPluginConversationReplies(t.Context(), 10)
	if err != nil || len(stale) != 1 || stale[0].ID != id {
		t.Fatalf("claim: %v %v", stale, err)
	}
	if _, err := store.FailPluginConversationReply(t.Context(), id, "unavailable", 0, outboxMaxAttempts); err != nil {
		t.Fatal(err)
	}
	m.deliverConversationReply(stale[0])
	current, err := store.ClaimPluginConversationReplies(t.Context(), 10)
	if err != nil || len(current) != 1 || current[0].Attempts != 1 {
		t.Fatalf("stale claim was attempted again: %+v %v", current, err)
	}
}
