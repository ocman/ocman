package server

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/testutil"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcptest"
)

func TestMCPSendSessionMessage(t *testing.T) {
	for _, platform := range []string{"opencode", "claudecode", "r-machine:opencode"} {
		t.Run(platform, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			var sent platforms.SendMessageRequest
			sendErr := error(nil)
			reg.Register(&fakePlatform{id: platform, sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
				if id != "recipient" {
					return nil, platforms.ErrNotFound
				}
				return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo", Status: "busy"}, TotalMessages: 1, DefaultModel: "p/current"}, nil
			}, sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return sendErr }})
			svc := sessionMCPService{srv}
			postQueuedMessage(t, srv, platform, "recipient", "held message")
			for _, scope := range []string{platform, ""} {
				got, err := svc.SendSessionMessage(t.Context(), scope, "recipient", "Continue the review")
				if err != nil || got != (internalmcp.SentSession{Platform: platform, SessionID: "recipient", Sent: true}) {
					t.Fatalf("send = %#v, %v", got, err)
				}
				if sent.SessionID != "recipient" || sent.Message != "Continue the review" || sent.Model != "" || sent.Delivery != "" || sent.Agent != "" {
					t.Fatalf("sent = %#v", sent)
				}
			}
			queued, err := srv.stateDB.ListQueuedMessages(t.Context(), platform, "recipient")
			if err != nil || len(queued) != 1 || queued[0].Text != "held message" {
				t.Fatalf("held queue changed: %#v, %v", queued, err)
			}
			for _, scope := range []string{platform, "", "r-disconnected:opencode"} {
				sent = platforms.SendMessageRequest{}
				if _, err := svc.SendSessionMessage(t.Context(), scope, "missing", "go"); !errors.Is(err, platforms.ErrNotFound) || sent.SessionID != "" {
					t.Fatalf("scope %q: err = %v, sent = %#v", scope, err, sent)
				}
			}
			if _, err := svc.SendSessionMessage(t.Context(), "r-disconnected:opencode", "recipient", "go"); !errors.Is(err, platforms.ErrNotFound) || sent.SessionID != "" {
				t.Fatalf("disconnected owner fell back locally: err = %v, sent = %#v", err, sent)
			}
			sendErr = errors.New("delivery failed")
			if got, err := svc.SendSessionMessage(t.Context(), platform, "recipient", "go"); !errors.Is(err, sendErr) || got.Sent {
				t.Fatalf("failed send = %#v, %v", got, err)
			}
		})
	}
}

func TestMCPSendSessionToolDelivery(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var sent platforms.SendMessageRequest
	reg.Register(&fakePlatform{id: "test", sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id}, TotalMessages: 1}, nil
	}, sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil }})
	mcpServer, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: sessionMCPService{srv}})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mcpServer.Close)
	result := testutil.CallTool(t, mcpServer, "sessions", map[string]any{"action": "send", "session_id": "recipient", "platform": "test", "prompt": "Review this"})
	if result.IsError || sent.SessionID != "recipient" || sent.Message != "Review this" {
		t.Fatalf("tool result = %q, sent = %#v", testutil.ResultText(result), sent)
	}
}

func TestMCPSendSessionCanonicalReference(t *testing.T) {
	srv, local, _ := identityHTTPFixture(t)
	local.sendMessageFn = func(platforms.SendMessageRequest) error {
		t.Fatal("canonical remote send reached local adapter")
		return nil
	}
	id := "ocm_" + uuid.NewString()
	native := &taskIdentityPlatform{fakePlatform: &fakePlatform{id: "r-remote:test"}, info: platforms.SessionIdentity{ID: id, OwnerID: "remote", Platform: "test", SourceKey: "source", Origin: "external", CreationState: "ready"}}
	var sent platforms.SendMessageRequest
	native.sessionDetailFn = func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo"}, TotalMessages: 1}, nil
	}
	native.sendMessageFn = func(req platforms.SendMessageRequest) error { sent = req; return nil }
	srv.registry.Register(native)
	srv.registry.RememberSessions("r-remote:test", []db.Session{{ID: id}})
	got, err := (sessionMCPService{srv}).SendSessionMessage(t.Context(), "", id, "go")
	if err != nil || !got.Sent || got.Platform != "r-remote:test" || got.SessionID != id || sent.SessionID != id {
		t.Fatalf("canonical reference = %#v, %v, sent = %#v", got, err, sent)
	}
	sent = platforms.SendMessageRequest{}
	if _, err := (sessionMCPService{srv}).SendSessionMessage(t.Context(), "wrong-platform", id, "go"); err == nil || sent.SessionID != "" {
		t.Fatalf("mismatched canonical scope = %v, sent = %#v", err, sent)
	}
}

func TestMCPSendSessionAmbiguous(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	for _, id := range []string{"a", "b"} {
		reg.Register(&fakePlatform{id: id, sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id}}, nil
		}, sendMessageFn: func(platforms.SendMessageRequest) error {
			t.Fatal("ambiguous send reached an adapter")
			return nil
		}})
	}
	var ambiguous internalmcp.AmbiguousSessionError
	if _, err := (sessionMCPService{srv}).SendSessionMessage(t.Context(), "", "duplicate", "go"); !errors.As(err, &ambiguous) {
		t.Fatalf("ambiguous send = %v", err)
	}
}

func TestMCPSendSessionLookupFailure(t *testing.T) {
	for _, detail := range []*platforms.SessionDetail{nil, {}} {
		srv, reg := newSessionsTestServer(t)
		lookupErr := error(nil)
		reg.Register(&fakePlatform{id: "p", sessionDetailFn: func(string) (*platforms.SessionDetail, error) { return detail, lookupErr }})
		svc := sessionMCPService{srv}
		if _, err := svc.SendSessionMessage(t.Context(), "p", "x", "go"); !errors.Is(err, platforms.ErrNotFound) {
			t.Fatalf("nil session = %v", err)
		}
		lookupErr = platforms.ErrPlatformUnreachable
		if _, err := svc.SendSessionMessage(t.Context(), "p", "x", "go"); !errors.Is(err, lookupErr) {
			t.Fatalf("lookup failure = %v", err)
		}
	}
}
