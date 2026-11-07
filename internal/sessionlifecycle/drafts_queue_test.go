package sessionlifecycle

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestDraftQueueAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, phase, request string
		code                 int
	}{
		{"missing", "", "", 404},
		{"unsubmitted", "draft", "", 409},
		{"discarded", "discarded", `{"platform":"opencode"}`, 409},
		{"missing payload", "accepted", "", 409},
		{"invalid payload", "starting", `{`, 503},
		{"missing owner", "starting", `{}`, 503},
		{"accepted", "accepted", `{"platform":"r-box:opencode"}`, 204},
		{"starting", "starting", `{"platform":"r-box:opencode"}`, 204},
		{"failed", "failed", `{"platform":"r-box:opencode"}`, 204},
		{"interrupted", "interrupted", `{"platform":"r-box:opencode"}`, 204},
		{"delivering", "delivering", `{"platform":"r-box:opencode"}`, 204},
		{"ready", "ready", `{"platform":"r-box:opencode"}`, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := statetest.Open(t, Schema)
			if tc.phase != "" {
				_, err := store.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,request,created_at) VALUES(?, '{}', ?, ?, 1)`, draftID, tc.phase, tc.request)
				if err != nil {
					t.Fatal(err)
				}
			}
			called := false
			m := NewModule(Deps{DB: store.SQL(), QueueList: func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.PathValue("id") != draftID || r.URL.Query().Get("platform") != "r-box:opencode" {
					t.Errorf("lost admitted identity: %s %s", r.PathValue("id"), r.URL)
				}
				w.WriteHeader(http.StatusNoContent)
			}})
			w := httptest.NewRecorder()
			moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/session-drafts/"+draftID+"/queue?platform=opencode", nil))
			if w.Code != tc.code || called != (tc.code == 204) {
				t.Fatalf("status=%d called=%v body=%s", w.Code, called, w.Body.String())
			}
		})
	}
}

func TestDraftQueueUnavailable(t *testing.T) {
	store := statetest.Open(t, Schema)
	if _, err := store.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,request,created_at) VALUES(?, '{}', 'starting', '{"platform":"opencode"}', 1)`, draftID); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Module{NewModule(Deps{}), NewModule(Deps{DB: store.SQL()})} {
		w := httptest.NewRecorder()
		moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/session-drafts/"+draftID+"/queue", nil))
		if w.Code != 503 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if err := store.SQL().Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	moduletest.Mount(NewModule(Deps{DB: store.SQL()})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/session-drafts/"+draftID+"/queue", nil))
	if w.Code != 500 {
		t.Fatalf("closed storage: %d %s", w.Code, w.Body.String())
	}
}
