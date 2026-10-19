package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type storedReadPlatform struct {
	*fakePlatform
	nativeCalls int
}

func (p *storedReadPlatform) ConversationHead(context.Context, string) (*platforms.ConversationHead, error) {
	return &platforms.ConversationHead{Identity: platforms.HistoryIdentity{Platform: "test", SourceKey: "source"}, Version: "v1", Session: &db.Session{ID: "s", Status: db.StatusWaiting}}, nil
}
func (p *storedReadPlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	if d, handled, err := platforms.TryStoredConversation(ctx, p, id, limit, offset); handled || err != nil {
		return d, err
	}
	p.nativeCalls++
	return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusBusy}}, nil
}

func TestConversationBrowsingRoutesSelectHistoryStrictly(t *testing.T) {
	s, registry := newSessionsTestServer(t)
	p := &storedReadPlatform{fakePlatform: &fakePlatform{id: "test"}}
	registry.Register(p)
	request := func() *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/session/s?platform=test", nil)
		r.SetPathValue("id", "s")
		w := httptest.NewRecorder()
		s.handleSession(w, r)
		return w
	}
	if w := request(); w.Code != 503 || p.nativeCalls != 0 {
		t.Fatalf("missing ingestion fell back: %d %s", w.Code, w.Body.String())
	}
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.WithConversationHistory(store, "owner")
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "physical", OwnerID: "owner", Platform: "test", SourceKey: "source"}); err != nil {
		t.Fatal(err)
	}
	batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "physical", SessionID: "s"}, &db.HistorySnapshot{Session: db.Session{ID: "s"}, Messages: []db.Message{{ID: "m", SessionID: "s", Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSourceState(t.Context(), "physical", "read-ready/s", "v1"); err != nil {
		t.Fatal(err)
	}
	w := request()
	if w.Code != 200 || w.Header().Get("X-Ocman-Conversation-Source") != "statev2" || p.nativeCalls != 0 {
		t.Fatalf("stored route: %d %s", w.Code, w.Body.String())
	}
	if d, err := (sessionMCPService{server: s}).GetSession(t.Context(), "test", "s", 30); err != nil || len(d.Messages) != 1 {
		t.Fatal("MCP did not read history", d, err)
	}
	owner := remote.NewServer(registry, nil, "owner", "test").UseConversationContext(s.ConversationReadContext)
	if _, err := owner.Session(t.Context(), &pb.SessionReq{Platform: "test", SessionId: "s", Limit: 30, Browsing: true}); err != nil {
		t.Fatal("owner read failed", err)
	}
	if err := store.SetSourceState(t.Context(), "physical", "read-ready/s", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Session(t.Context(), &pb.SessionReq{Platform: "test", SessionId: "s", Limit: 30, Browsing: true}); status.Code(err) != codes.Unavailable {
		t.Fatal("owner did not fail closed", err)
	}
	if w := request(); w.Code != 503 || p.nativeCalls != 0 {
		t.Fatalf("missing validation selected a platform transcript: %d", w.Code)
	}
}
