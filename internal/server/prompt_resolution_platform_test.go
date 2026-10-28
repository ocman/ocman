package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
)

// openWaits reports the platform's user waits still open at until.
func openWaits(t *testing.T, s *Server, platform string, since, until int64) int {
	t.Helper()
	waits, err := s.stateDB.AgentUserWaits(t.Context(), platform, since, until)
	if err != nil {
		t.Fatal(err)
	}
	open := 0
	for _, wait := range waits {
		if wait.End >= until {
			open++
		}
	}
	return open
}

// A Claude prompt's wait is recorded under claudecode, so the reply that
// answers it must resolve it under claudecode too, not under opencode.
func TestProviderReplyResolvesClaudeAgentWaits(t *testing.T) {
	s := testServer(t)
	adapter := &fakePlatform{id: "claudecode"}
	s.registry.Register(adapter)
	start := time.Now().UnixMilli() - 1000
	s.onPromptNeedsUser("claudecode", "s", "permission", "p")
	s.onPromptNeedsUser("claudecode", "s", "question", "q")
	s.onPromptNeedsUser("claudecode", "s", "question", "r")

	handle := s.providerPromptHandler(t.Context(), adapter)
	for _, event := range []struct {
		typ   string
		props map[string]any
	}{
		{"permission.replied", map[string]any{"sessionID": "s", "requestID": "p", "reply": "once"}},
		{"question.replied", map[string]any{"sessionID": "s", "requestID": "q"}},
		{"question.rejected", map[string]any{"sessionID": "s", "requestID": "r"}},
	} {
		props, _ := json.Marshal(event.props)
		handle(providerprotocol.Event{Type: event.typ, Properties: props})
	}

	until := time.Now().UnixMilli() + 10000
	if n := openWaits(t, s, "claudecode", start, until); n != 0 {
		t.Fatalf("%d claudecode waits still open after their replies", n)
	}
	if waits, _ := s.stateDB.AgentUserWaits(t.Context(), "opencode", start, until); len(waits) != 0 {
		t.Fatalf("a Claude reply touched opencode waits: %+v", waits)
	}
}

// An interrupt cancels a Claude prompt; it is not a reply and leaves the
// wait for the session's idle edge.
func TestProviderCancelledPermissionIsNotAReply(t *testing.T) {
	s := testServer(t)
	adapter := &fakePlatform{id: "claudecode"}
	s.registry.Register(adapter)
	start := time.Now().UnixMilli() - 1000
	s.onPromptNeedsUser("claudecode", "s", "permission", "p")
	props, _ := json.Marshal(map[string]any{"sessionID": "s", "requestID": "p", "reply": "reject", "reason": "cancelled"})
	s.providerPromptHandler(t.Context(), adapter)(providerprotocol.Event{Type: "permission.replied", Properties: props})
	if n := openWaits(t, s, "claudecode", start, time.Now().UnixMilli()+10000); n != 1 {
		t.Fatalf("open waits = %d, want the cancelled prompt's wait kept open", n)
	}
}

// Session rules approving a Claude permission resolve its Inbox row and
// user wait under claudecode.
func TestRulesApprovalResolvesClaudeInboxAndWait(t *testing.T) {
	adapter := &fakePlatform{
		id:   "claudecode",
		caps: platforms.Capabilities{PermissionRules: true},
		permissionRulesFn: func(string) ([]platforms.PermissionRule, error) {
			return []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}, nil
		},
		listPermissionsFn: func(string) ([]platforms.LivePrompt, error) {
			return []platforms.LivePrompt{{"id": "p1", "sessionID": "sess-1", "permission": "bash", "patterns": []any{"ls"}}}, nil
		},
		respondPermissionFn: func(platforms.RespondPermissionRequest) error { return nil },
		// Session rules read the session's ancestry before any rule applies.
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id}}, nil
		},
	}
	s := testServer(t)
	s.registry.Register(adapter)
	start := time.Now().UnixMilli() - 1000
	s.notifyPermissionInbox(state.InboxPermission{Platform: "claudecode", SessionID: "sess-1", PermissionID: "p1", Permission: "bash"})
	s.onPromptNeedsUser("claudecode", "sess-1", "permission", "p1")

	s.autoapproveModule().ApplyRulesToPending(t.Context(), adapter, "sess-1")

	if items, _ := s.stateDB.ListInboxItems(t.Context()); len(items) != 0 {
		t.Fatalf("auto-approved Claude permission stayed in the Inbox: %+v", items)
	}
	if n := openWaits(t, s, "claudecode", start, time.Now().UnixMilli()+10000); n != 0 {
		t.Fatalf("%d claudecode waits still open after auto-approval", n)
	}
}

// Resolution broadcasts name the prompt's owner, so clients never have to
// guess it.
func TestPromptResolvedBroadcastsCarryPlatform(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	srv.broadcastPermissionResolved("claudecode", "s", "p", "replied")
	srv.broadcastResolved("ocman.question.resolved", "requestId", "claudecode", "s", "q", "rejected")
	for _, want := range []string{
		`{"permissionId":"p","platform":"claudecode","reason":"replied","sessionID":"s"}`,
		`{"platform":"claudecode","reason":"rejected","requestId":"q","sessionID":"s"}`,
	} {
		if ev := <-sub.ch; string(ev.data) != want {
			t.Fatalf("%s payload = %s, want %s", ev.event, ev.data, want)
		}
	}
}
