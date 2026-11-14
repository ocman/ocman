package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestSessionTasksPreserveNamedConnection(t *testing.T) {
	for _, scope := range []string{"cnx_work", "", "cnx_missing", "r-disconnected:test"} {
		t.Run(scope, func(t *testing.T) {
			s, _, id := namedMCPSessionFixture(t)
			r := httptest.NewRequest(http.MethodGet, "/api/session/"+id+"/tasks?ids="+id+"&platform="+scope, nil)
			w := httptest.NewRecorder()
			serveRoutes(t, s, w, r)
			if scope == "cnx_missing" || scope == "r-disconnected:test" {
				if w.Code < 400 {
					t.Fatalf("missing explicit owner fell back: HTTP %d: %s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("task preview: HTTP %d: %s", w.Code, w.Body.String())
			}
			var result struct {
				Tasks map[string]struct {
					Platform string          `json:"platform"`
					Messages json.RawMessage `json:"messages"`
					Parts    json.RawMessage `json:"parts"`
				} `json:"tasks"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if task := result.Tasks[id]; task.Platform != "cnx_work" || string(task.Messages) != "[]" || string(task.Parts) != "[]" {
				t.Fatalf("task lost connection or empty conversation: %s", w.Body.String())
			}
		})
	}
}

func TestSessionDetailPreservesNamedConnectionCreator(t *testing.T) {
	for _, scope := range []string{"cnx_work", ""} {
		t.Run(scope, func(t *testing.T) {
			s, _, id := namedMCPSessionFixture(t)
			for _, creator := range []state.SessionCreator{
				{Platform: "cnx_work", SessionID: id, CreatorPlatform: "cnx_launcher", CreatorSessionID: "parent", CreatedAt: 42},
				{Platform: "test", SessionID: id, CreatorPlatform: "wrong-account", CreatorSessionID: "wrong-parent", CreatedAt: 1},
			} {
				if err := s.stateDB.RecordSessionCreator(t.Context(), creator); err != nil {
					t.Fatal(err)
				}
			}
			r := httptest.NewRequest(http.MethodGet, "/api/session/"+id+"?platform="+scope, nil)
			w := httptest.NewRecorder()
			serveRoutes(t, s, w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("session detail: HTTP %d: %s", w.Code, w.Body.String())
			}
			var result platforms.SessionDetail
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			for _, creator := range []*platforms.SessionCreator{result.Creator, result.Session.Creator} {
				if creator == nil || creator.Platform != "cnx_launcher" || creator.SessionID != "parent" || creator.CreatedAt != 42 {
					t.Fatalf("creator read from wrong account: %+v", creator)
				}
			}
			if result.Session.Platform != "cnx_work" {
				t.Fatalf("session lost connection: %+v", result.Session)
			}
		})
	}
}
