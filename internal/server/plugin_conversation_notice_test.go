package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

// awaitThread waits for want to appear in the thread's delivered messages.
func (f *conversationFixture) awaitThread(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(f.replies(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("thread never saw %q, saw: %q", want, f.replies())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// countThread counts delivered messages containing want.
func (f *conversationFixture) countThread(want string) int {
	return strings.Count(f.replies(), want)
}

// TestConversationAttentionNoticeReachesThread covers the prompt lifecycle a
// thread can see: a permission and a question each produce one notice with a
// link, a re-observed prompt produces nothing more, and answering it resolves
// the notice by the reply that follows rather than by a retraction.
func TestConversationAttentionNoticeReachesThread(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	f.settle(t)

	for _, tc := range []struct{ kind, requestID, want string }{
		{"permission", "perm-1", "needs a permission decision in ocman"},
		{"question", "q-1", "waiting on an answer in ocman"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			f.s.conversationPromptNeedsUser("opencode", "ses-chat", tc.kind, tc.requestID)
			f.awaitThread(t, tc.want)
			if got := f.countThread(tc.want); got != 1 {
				t.Fatalf("%s notices: %d, want 1", tc.kind, got)
			}
			// A stream reconnect, a replayed notification or a restart all
			// re-observe the same prompt. The request id keys the notice, so
			// none of them may post a second time.
			for range 3 {
				f.s.conversationPromptNeedsUser("opencode", "ses-chat", tc.kind, tc.requestID)
			}
			time.Sleep(200 * time.Millisecond)
			if got := f.countThread(tc.want); got != 1 {
				t.Fatalf("re-observed %s prompt posted %d notices", tc.kind, got)
			}
			// The notice carries a link to the session it is about, so the
			// reader has somewhere to go and answer it.
			if !strings.Contains(f.replies(), "/session/ses-chat") {
				t.Fatalf("notice carried no session link: %q", f.replies())
			}
		})
	}

	// Answering resolves the notice the only way a thread can be resolved:
	// the turn continues and its completed answer lands under the notice.
	f.settle(t)
	f.awaitThread(t, "first half")

	// A different prompt in the same session is a new notice, not a duplicate.
	f.s.conversationPromptNeedsUser("opencode", "ses-chat", "permission", "perm-2")
	deadline := time.Now().Add(20 * time.Second)
	for f.countThread("needs a permission decision in ocman") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("second permission prompt produced no notice: %q", f.replies())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestConversationAttentionNoticeIgnoresUnmappedSession keeps the notice path
// scoped to threads: the overwhelming majority of prompts belong to sessions
// nobody is watching from a provider, and they must stay silent.
func TestConversationAttentionNoticeIgnoresUnmappedSession(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	f.s.conversationPromptNeedsUser("opencode", "ses-unmapped", "permission", "perm-1")
	// A prompt on the mapped session id but a different platform is a
	// different session: identity is (platform, session), not the bare id.
	f.s.conversationPromptNeedsUser("r-other:opencode", "ses-chat", "permission", "perm-2")
	// An unknown kind must render nothing rather than a bare link.
	f.s.conversationPromptNeedsUser("opencode", "ses-chat", "elevation", "perm-3")
	// A prompt with no request id has no dedup key, so it is dropped rather
	// than posted under a key that would collide with the next one.
	f.s.conversationPromptNeedsUser("opencode", "ses-chat", "permission", "")

	time.Sleep(200 * time.Millisecond)
	if got := f.countThread("in ocman"); got != 0 {
		t.Fatalf("unrelated prompts posted %d notices: %q", got, f.replies())
	}
}

// TestConversationErrorOutcomeReachesThread covers a turn that ends in failure:
// the thread is told, once per failed turn.
func TestConversationErrorOutcomeReachesThread(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	f.mu.Lock()
	f.errored = true
	f.mu.Unlock()

	f.s.onSessionIdle("opencode", "ses-chat")
	f.awaitThread(t, "last turn ended with an error")

	// A repeated idle edge for the same failed turn must not report twice.
	f.s.onSessionIdle("opencode", "ses-chat")
	time.Sleep(200 * time.Millisecond)
	if got := f.countThread("last turn ended with an error"); got != 1 {
		t.Fatalf("repeated idle edge produced %d error notices", got)
	}
	// The partial answer still reaches the thread: the notice says where the
	// turn stopped, it does not replace what the turn produced.
	if !strings.Contains(f.replies(), "first half") {
		t.Fatalf("errored turn dropped its assistant text: %q", f.replies())
	}
}

// TestConversationUnavailableOutcomeReachesThread covers a message whose
// receipt is consumed but whose session cannot be reached: nothing will
// redeliver it, so the thread must be told rather than left waiting.
func TestConversationUnavailableOutcomeReachesThread(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	f.settle(t)

	f.mu.Lock()
	f.createErr = errors.New("opencode is not reachable")
	f.mu.Unlock()

	// A fresh thread, so the failure is session creation rather than a send to
	// an already-mapped session.
	message := conversationMessage("are you there")
	message.ThreadID = "slackC3:1700000000.000300"
	if err := f.deliver(t, message); err == nil {
		t.Fatal("an unreachable session must still fail the delivery")
	}
	f.awaitThread(t, "could not be delivered")
	if !strings.Contains(f.replies(), "slackC3:1700000000.000300") {
		t.Fatalf("unavailable notice went to the wrong thread: %q", f.replies())
	}

	// The same message redelivered by the provider is a duplicate: its receipt
	// is already consumed, so it must not produce a second notice.
	if err := f.deliver(t, message); err != nil {
		t.Fatalf("a duplicate delivery must be dropped, not failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := f.countThread("could not be delivered"); got != 1 {
		t.Fatalf("redelivery produced %d unavailable notices", got)
	}
}

// TestConversationNoticeRendering pins what a notice may contain. A thread is
// usually a wider audience than the session's operator, so a notice carries a
// fixed sentence and a link and nothing else — no permission text, no command,
// no transcript.
func TestConversationNoticeRendering(t *testing.T) {
	s := &Server{}
	for kind := range conversationNoticeText {
		notice := s.conversationNotice(kind, "ses-1")
		if notice == "" {
			t.Fatalf("%s rendered nothing", kind)
		}
		if !strings.HasSuffix(notice, "http://localhost:8228/session/ses-1") {
			t.Fatalf("%s notice = %q", kind, notice)
		}
		// Host-authored text still has to satisfy the wire contract.
		if err := (plugins.ConversationReply{
			AccountID: conversationTestAccount, ThreadID: conversationTestThread, Text: notice,
		}).Validate(); err != nil {
			t.Fatalf("%s notice fails the wire contract: %v", kind, err)
		}
	}
	if got := s.conversationNotice("no-such-kind", "ses-1"); got != "" {
		t.Fatalf("unknown kind rendered %q", got)
	}

	// A session id is not trusted to be URL-safe.
	if got := s.sessionURL("a b/c?d#e"); got != "http://localhost:8228/session/a%20b%2Fc%3Fd%23e" {
		t.Fatalf("session url = %q", got)
	}
	// With no session to point at, the notice still links somewhere openable.
	if got := s.sessionURL(""); got != "http://localhost:8228/" {
		t.Fatalf("base url = %q", got)
	}
}

// TestConversationNoticeLinkUsesConfiguredBaseURL covers the addressing
// requirement: a reader in a provider thread is not on this machine, so the
// link has to come from the operator's configured public base URL rather than
// from the loopback address ocman happens to listen on.
func TestConversationNoticeLinkUsesConfiguredBaseURL(t *testing.T) {
	for _, tc := range []struct{ name, base, addr, want string }{
		{"configured base wins", "https://ocman.example.com", "127.0.0.1:9999", "https://ocman.example.com/session/ses-1"},
		{"trailing slash is not doubled", "https://ocman.example.com/", "", "https://ocman.example.com/session/ses-1"},
		{"subpath is preserved", "https://example.com/ocman", "", "https://example.com/ocman/session/ses-1"},
		{"falls back to the listen address", "", "127.0.0.1:9999", "http://127.0.0.1:9999/session/ses-1"},
		{"bare port becomes localhost", "", ":9999", "http://localhost:9999/session/ses-1"},
		{"unconfigured is loopback", "", "", "http://localhost:8228/session/ses-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{publicBaseURL: tc.base, addr: tc.addr}
			if got := s.sessionURL("ses-1"); got != tc.want {
				t.Fatalf("session url = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConversationNoticeSurvivesTheFailureItReports covers the context a notice
// runs under. The likeliest reason a message cannot be delivered is that
// reaching the agent ran out of time, so a notice that inherited the caller's
// exhausted context would be dropped in precisely the case it exists for.
func TestConversationNoticeSurvivesTheFailureItReports(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	key := state.PluginConversationKey{
		PluginID: conversationPluginDescription().ID, AccountID: conversationTestAccount,
		ThreadID: conversationTestThread,
	}
	dead, cancel := context.WithCancel(t.Context())
	cancel()
	f.s.appendConversationNotice(dead, key, "notice-on-a-dead-context", f.s.conversationNotice("unavailable", ""))
	f.awaitThread(t, "could not be delivered")
}
