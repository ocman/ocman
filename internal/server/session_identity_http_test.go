package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/google/uuid"
)

func identityHTTPFixture(t *testing.T) (*Server, *fakePlatform, string) {
	t.Helper()
	db, err := state.Open(statetest.Path(t, ModuleSchemas()...), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	history, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	p := &fakePlatform{id: "test"}
	registry := platforms.NewRegistry()
	registry.Register(&historyTestPlatform{fakePlatform: p})
	s := New(nil, db, "", registry, nil).WithConversationHistory(history, "owner")
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	return s, p, row.ID
}

func TestOwnedSessionStreamProjectsReferencesAndPreservesCursor(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	w := httptest.NewRecorder()
	s.withOwnedSessionPath(s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/native/stream" {
			t.Fatalf("native stream received %s", r.URL)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		frame := `id: epoch:42` + "\n" + `event: oce` + "\n" + `data: {"platform":"test","sessionId":"native","seq":42,"event":{"type":"part.upserted","part":{"id":"part","messageId":"message","sessionId":"native","data":{"type":"text","text":"native","sessionId":"native"}}}}` + "\n\n"
		mid := len(frame) / 2
		_, _ = fmt.Fprint(w, frame[:mid])
		_, _ = fmt.Fprint(w, frame[mid:])
		_, _ = fmt.Fprint(w, ":keepalive\n\n")
	}))(w, httptest.NewRequest(http.MethodGet, "/api/session/"+ownedID+"/stream", nil))
	body := w.Body.String()
	if !strings.Contains(body, "id: epoch:42\nevent: oce\n") || !strings.Contains(body, `"sessionId":"`+ownedID+`"`) || !strings.Contains(body, `"id":"part"`) || !strings.Contains(body, `"messageId":"message"`) || !strings.Contains(body, `"data":{"sessionId":"native","text":"native","type":"text"}`) || !strings.Contains(body, ":keepalive\n\n") {
		t.Fatalf("invalid public stream projection: %s", body)
	}
}

func TestLegacySessionViewUsesResolvedAdapterForProjection(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	s.registry.RememberSessions("test", []db.Session{{ID: "native", Platform: "test"}})
	w := httptest.NewRecorder()
	s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		adapter := s.resolvePlatformForSession(w, r, "native")
		if adapter == nil {
			return
		}
		httpx.WriteJSON(w, struct {
			SessionID string `json:"sessionId"`
		}{SessionID: "native"})
	})(w, httptest.NewRequest(http.MethodGet, "/api/session/native/queue", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), ownedID) {
		t.Fatalf("resolved adapter lost at projection: %d %s", w.Code, w.Body.String())
	}
}

func TestIdentityProjectionDoesNotRequireRunningExecutionInstance(t *testing.T) {
	s, p, ownedID := identityHTTPFixture(t)
	available := false
	p.available = &available
	s.registry.RememberSessions("test", []db.Session{{ID: "native", Platform: "test"}})
	rows, err := s.sessions.LookupSessionIdentities(t.Context(), "", []string{"native"})
	if err != nil || len(rows) != 1 || rows[0].ID != ownedID {
		t.Fatalf("retained identity depended on live execution: %+v %v", rows, err)
	}
}

func TestSessionListKeepsReadyRowsWhenAnotherIdentityIsConfiguring(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := catalogue.Reserve(t.Context(), sessioncatalog.Reservation{RequestKey: "pending", PayloadHash: strings.Repeat("0", 64), OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.Attach(t.Context(), row.ID, sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "pending"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, []db.Session{{ID: "native", Platform: "test"}, {ID: "pending", Platform: "test"}})
	})(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var rows []db.Session
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("invalid list: %s %v", w.Body.String(), err)
	}
	if w.Code != 200 || len(rows) != 1 || rows[0].ID != ownedID {
		t.Fatalf("pending creation blocked unrelated list: %d %s", w.Code, w.Body.String())
	}
}

func TestSessionHTTPViewsUseOwnedIDsAndKeepNativeMutationKeys(t *testing.T) {
	s, p, ownedID := identityHTTPFixture(t)
	p.sessionDetailFn = func(id string) (*platforms.SessionDetail, error) {
		if id != "native" {
			t.Fatalf("native reader received %s", id)
		}
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: "test", Directory: "/repo"}, Messages: []db.Message{{ID: "message", SessionID: id, Data: json.RawMessage(`{"role":"user","sessionID":"native"}`)}}}, nil
	}
	w := httptest.NewRecorder()
	s.withOwnedSessionPath(s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		value, err := p.Session(r.Context(), "native", 30, 0)
		if err != nil {
			t.Fatal(err)
		}
		httpx.WriteJSON(w, value)
	}))(w, httptest.NewRequest(http.MethodGet, "/api/session/"+ownedID, nil))
	var detail platforms.SessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Session.ID != ownedID || detail.Messages[0].SessionID != ownedID || detail.Messages[0].ID != "message" || !strings.Contains(string(detail.Messages[0].Data), `"sessionID":"native"`) {
		t.Fatalf("public/native boundary incorrect: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ParentSessionID string `json:"parentSessionId"`
			Prompt          string `json:"prompt"`
		}
		if !httpx.ReadJSON(w, r, 4096, &req) {
			return
		}
		if req.ParentSessionID != "native" || req.Prompt != ownedID {
			t.Fatalf("request projection touched wrong fields: %+v", req)
		}
		w.WriteHeader(http.StatusNoContent)
	})(w, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(`{"parentSessionId":"`+ownedID+`","prompt":"`+ownedID+`"}`)))
	if w.Code != 204 {
		t.Fatalf("owned parent reference rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestSessionIdentityEndpointOmitsPhysicalSource(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	w := httptest.NewRecorder()
	s.handleSessionIdentity(w, httptest.NewRequest(http.MethodGet, "/api/session-identity?nativeId=native&platform=test", nil))
	if w.Code != 200 {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	var info platforms.SessionIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != ownedID || info.NativeID != "native" || info.OwnerID != "owner" || info.SourceKey != "" || strings.Contains(w.Body.String(), "sourceKey") {
		t.Fatalf("invalid public identity: %s", w.Body.String())
	}
}

func TestOwnedSessionPathsReachNativeModuleHandlers(t *testing.T) {
	s, p, ownedID := identityHTTPFixture(t)
	mux := http.NewServeMux()
	moduleRouter{s: s, mux: mux}.Handle("POST /api/session/{id}/probe", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "native" || r.URL.Path != "/api/session/native/probe" || r.URL.Query().Get("platform") != "test" {
			t.Fatalf("owned ID leaked into native handler: %s %s", r.URL, r.PathValue("id"))
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/session/"+ownedID+"/probe", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("X-Ocman-Session-ID") != ownedID {
		t.Fatalf("alias: %d %s", w.Code, w.Body.String())
	}
	if req.URL.Path != "/api/session/"+ownedID+"/probe" {
		t.Fatal("mutated caller URL")
	}
	p.sessionDetailFn = func(string) (*platforms.SessionDetail, error) {
		t.Fatal("unknown owned ID probed native platform")
		return nil, nil
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/session/ocm_"+uuid.NewString()+"/probe?platform=test", nil))
	if w.Code != 404 {
		t.Fatalf("unknown owned ID fell back: %d %s", w.Code, w.Body.String())
	}
}

func TestOwnedSessionPathRejectsInactivePhysicalSource(t *testing.T) {
	s, _, _ := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	b := sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "old-source", NativeID: "native"}
	if _, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: b.OwnerID, Platform: b.Platform, SourceKey: b.SourceKey}, []sessioncatalog.NativeSession{{ID: b.NativeID}}); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.withOwnedSessionPath(func(http.ResponseWriter, *http.Request) { t.Fatal("inactive source was rebound to current native ID") })(w, httptest.NewRequest(http.MethodGet, "/api/session/"+row.ID, nil))
	if w.Code != 503 {
		t.Fatalf("inactive source alias: %d %s", w.Code, w.Body.String())
	}
}
