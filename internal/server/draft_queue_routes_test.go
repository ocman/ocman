package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestDraftQueueBeforeCanonicalBinding(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	const id = "ocm_01234567-89ab-4cde-8fab-0123456789ab"
	// No catalogue or platform session exists. Only admission is durable.
	_, err := s.stateDB.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,request,created_at)
		VALUES(?, '{}', 'starting', '{"platform":"r-box:opencode"}', 1)`, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []state.QueuedMessage{
		{ID: "q-first", Platform: "r-box:opencode", SessionID: id, Text: "first"},
		{ID: "q-second", Platform: "r-box:opencode", SessionID: id, Text: "second"},
		{ID: "q-other", Platform: "opencode", SessionID: "other", Text: "private"},
		{ID: "q-wrong-owner", Platform: "opencode", SessionID: id, Text: "foreign"},
	} {
		if err := s.stateDB.EnqueueMessage(t.Context(), msg); err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, suffix, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/session-drafts/"+id+"/queue"+suffix, strings.NewReader(body))
		w := httptest.NewRecorder()
		serveModule(s, s.sessionLifecycleModule(), w, r)
		return w
	}
	w := call(http.MethodGet, "?platform=opencode", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "first") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "foreign") {
		t.Fatalf("draft queue unavailable or misrouted: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPost, "/q-second/move", `{"direction":-1}`); w.Code != 204 {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodDelete, "/q-first", ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodDelete, "/q-other", ""); w.Code != 204 {
		t.Fatalf("idempotent foreign delete: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodPost, "/q-wrong-owner/move?platform=opencode", `{"direction":-1}`); w.Code != 204 {
		t.Fatalf("foreign move: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodDelete, "/q-wrong-owner?platform=opencode", ""); w.Code != 204 {
		t.Fatalf("foreign delete: %d %s", w.Code, w.Body.String())
	}
	if rows, err := s.stateDB.ListQueuedMessages(t.Context(), "opencode", id); err != nil || len(rows) != 1 {
		t.Fatalf("foreign owner queue changed: %+v %v", rows, err)
	}
	if rows, err := s.stateDB.ListQueuedMessages(t.Context(), "opencode", "other"); err != nil || len(rows) != 1 {
		t.Fatalf("foreign queue changed: %+v %v", rows, err)
	}
	if w := call(http.MethodGet, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"text":"first"`) || !strings.Contains(w.Body.String(), "second") {
		t.Fatalf("queue mutations not reflected: %d %s", w.Code, w.Body.String())
	}
}

func TestDraftQueueRoutesRetainHostControlGuard(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	mux := http.NewServeMux()
	s.sessionLifecycleModule().Routes(moduleRouter{s: s, mux: mux})
	for _, tc := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodDelete, "/q-held"}, {http.MethodPost, "/q-held/move"},
	} {
		r := httptest.NewRequest(tc.method, "/api/session-drafts/ocm_missing/queue"+tc.suffix, nil)
		r.RemoteAddr = "192.0.2.1:12345"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s exposed draft queue: %d %s", tc.method, w.Code, w.Body.String())
		}
	}
}
