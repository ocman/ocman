package server

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func namedMCPSessionFixture(t *testing.T) (*Server, *fakePlatform, string) {
	t.Helper()
	s, native, id := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	history := statev2test.Open(t)
	instance := statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if err := history.RegisterInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	key := statev2.Key{InstanceID: instance.ID, SessionID: id}
	batch, _, err := conversationingest.Normalize(key, &db.HistorySnapshot{Session: db.Session{ID: id, Directory: "/repo", TimeCreated: 1, TimeUpdated: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	adapter, err := owned.New(t.Context(), &historyTestPlatform{fakePlatform: native}, catalogue, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	s.registry.Unregister("test")
	if err := s.registry.RegisterConnection("cnx_work", adapter); err != nil {
		t.Fatal(err)
	}
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	s.registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		if owner == "owner" && kind == "test" && source == "source" {
			return "cnx_work", nil
		}
		return "", platforms.ErrNotFound
	})
	return s, native, id
}

func TestMCPSendSessionPreservesNamedConnection(t *testing.T) {
	for _, scope := range []string{"cnx_work", "", "cnx_missing", "r-disconnected:test"} {
		t.Run(scope, func(t *testing.T) {
			s, native, id := namedMCPSessionFixture(t)
			var sent []platforms.SendMessageRequest
			native.SendMessageFn = func(req platforms.SendMessageRequest) error {
				sent = append(sent, req)
				return nil
			}
			result, err := (sessionMCPService{s}).SendSessionMessage(t.Context(), scope, id, "Stage complete")
			if scope == "cnx_missing" || scope == "r-disconnected:test" {
				if err == nil || result.Sent || len(sent) != 0 {
					t.Fatalf("explicit missing owner fell back: result=%+v err=%v sent=%+v", result, err, sent)
				}
				return
			}
			if err != nil || !result.Sent || result.Platform != "cnx_work" || result.SessionID != id {
				t.Fatalf("send lost connection: result=%+v err=%v", result, err)
			}
			if len(sent) != 1 || sent[0].SessionID != "native" || sent[0].Message != wantMCPAgentMessage("Stage complete") {
				t.Fatalf("wrong native delivery: %+v", sent)
			}
		})
	}
}

func TestMCPArchiveSessionPreservesNamedConnection(t *testing.T) {
	for _, scope := range []string{"cnx_work", ""} {
		t.Run(scope, func(t *testing.T) {
			s, _, id := namedMCPSessionFixture(t)
			for _, archived := range []bool{true, false} {
				result, err := (sessionMCPService{s}).SetSessionArchived(t.Context(), scope, id, archived)
				if err != nil || result.Platform != "cnx_work" || result.SessionID != id || result.Archived != archived || result.Deferred {
					t.Fatalf("archive lost connection: result=%+v err=%v", result, err)
				}
			}
		})
	}
}
