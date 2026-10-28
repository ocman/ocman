package pluginsmod

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// TestConversationNoticeRendering pins what a notice may contain. A thread is
// usually a wider audience than the session's operator, so a notice carries a
// fixed sentence and a link and nothing else — no permission text, no command,
// no transcript.
func TestConversationNoticeRendering(t *testing.T) {
	s := NewModule(Deps{PublicURL: func(path string) string { return "http://localhost:8228" + path }})
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
			AccountID: "T1", ThreadID: "C1:1", Text: notice,
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

// TestConversationNoticeSurvivesTheFailureItReports covers the context a notice
// runs under. The likeliest reason a message cannot be delivered is that
// reaching the agent ran out of time, so a notice that inherited the caller's
// exhausted context would be dropped in precisely the case it exists for.
func TestConversationNoticeSurvivesTheFailureItReports(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := NewModule(Deps{Store: store, PublicURL: func(path string) string { return path }})
	key := state.PluginConversationKey{PluginID: "org.example.chatops", AccountID: "T1", ThreadID: "C1:1"}
	dead, cancel := context.WithCancel(t.Context())
	cancel()
	m.appendConversationNotice(dead, key, "notice-on-a-dead-context", m.conversationNotice("unavailable", ""))
	// The plugin is not registered, so the pump leaves the row pending.
	if status, err := store.PluginConversationBacklogStatus(t.Context(), key.PluginID); err != nil || status.Pending != 1 {
		t.Fatalf("notice not recorded: %+v %v", status, err)
	}
}
