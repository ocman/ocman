package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

func TestSessionContentSearchRoute(t *testing.T) {
	srv := testServer(t)
	request := func(handler http.Handler, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/sessions/search", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	mux := mustRoutes(t, srv)
	srv.historyAnalytics = nil
	if w := request(mux, http.MethodPost, `{"query":"need le"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing history: %d %s", w.Code, w.Body)
	}
	history := storedProjectFixture(t, srv)
	if _, err := history.Commit(t.Context(), statev2.Batch{
		Session:  statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: "ocm_older"}, Title: "Older work", Directory: "/repo", UpdatedAt: 1},
		Messages: []statev2.Message{{ID: "m", Role: "assistant", ConversationVisible: true}},
		Parts:    []statev2.Part{{ID: "p", MessageID: "m", Kind: "text", Text: "An old Needle phrase"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, body string
		want         int
	}{
		{http.MethodPost, `{"query":" needle "}`, http.StatusOK},
		{http.MethodPost, `{"query":" "}`, http.StatusBadRequest},
		{http.MethodPost, `{"query":"` + strings.Repeat("a", 1025) + `"}`, http.StatusBadRequest},
		{http.MethodPost, `{`, http.StatusBadRequest},
		{http.MethodGet, "", http.StatusMethodNotAllowed},
	} {
		w := request(mux, tc.method, tc.body)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d want %d: %s", tc.method, w.Code, tc.want, w.Body)
		}
		if w.Code == http.StatusOK {
			var result struct {
				Matches  []statev2.SessionTextMatch
				Indexing bool
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Indexing || len(result.Matches) != 1 || result.Matches[0].ID != "ocm_older" || result.Matches[0].Snippet != "An old Needle phrase" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected search results: %s (%v)", w.Body, err)
			}
		}
	}
	srv.auth = newTestAuth(t, "password")
	if w := request(mux, http.MethodPost, `{"query":"needle"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated search: %d %s", w.Code, w.Body)
	}
}
