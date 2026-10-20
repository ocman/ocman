package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

func TestFeatureResponsesUseOwnedSessionReferences(t *testing.T) {
	for _, tc := range []struct{ name, path, payload string }{
		{"notify", "/api/sessions/notify", `[{"id":"native","platform":"test","permissions":[{"sessionId":"native","requestId":"request","platform":"test"}]}]`},
		{"routine", "/api/routines", `[{"id":"routine","platform":"test","sessionId":"native","prompt":"native"}]`},
		{"routine history", "/api/routines/routine/history", `[{"id":"run","platform":"test","sessionId":"native"}]`},
		{"factory", "/api/factory/epics/epic", `{"id":"epic","attempts":[{"id":"attempt","session":{"id":"native","platform":"test"}}]}`},
		{"inbox", "/api/inbox", `{"items":[{"id":"item","session":{"platform":"test","sessionId":"native"},"permission":{"platform":"test","sessionId":"native","permissionId":"request","metadata":{"sessionId":"native"}}}]}`},
		{"worktree launch", "/api/worktree/create-and-launch", `{"platform":"test","sessionId":"native"}`},
		{"forge launch", "/api/project/handle", `{"platform":"test","sessionId":"native"}`},
		{"webhook dispatch", "/api/webhook-inboxes/inbox/deliveries", `[{"deliveryId":"delivery","body":"native","dispatches":[{"platform":"test","sessionId":"native","routineId":"routine"}]}]`},
		{"shares", "/api/shares", `[{"platform":"test","sessionId":"native","token":"token"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, ownedID := identityHTTPFixture(t)
			var payload any
			if err := json.Unmarshal([]byte(tc.payload), &payload); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.withSessionViews(func(w http.ResponseWriter, r *http.Request) { httpx.WriteJSON(w, payload) })(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), ownedID) {
				t.Fatalf("feature leaked native session reference: %d %s", w.Code, w.Body.String())
			}
			if tc.name == "notify" && strings.Contains(w.Body.String(), `"sessionId":"native"`) {
				t.Fatalf("prompt reference was not canonicalized: %s", w.Body.String())
			}
			if tc.name == "inbox" && !strings.Contains(w.Body.String(), `"metadata":{"sessionId":"native"}`) {
				t.Fatalf("opaque permission metadata changed: %s", w.Body.String())
			}
		})
	}
}

func TestPrivilegedFeatureRoutesInstallIdentityProjection(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	for _, path := range []string{"/api/project/handle", "/api/worktree/create-and-launch"} {
		mux := http.NewServeMux()
		(moduleRouter{s: s, mux: mux}).Handle(path, s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, map[string]string{"platform": "test", "sessionId": "native"})
		}))
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), ownedID) {
			t.Fatalf("privileged feature missed identity boundary: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestLocalModuleRoutesTranslateOwnedRequestReferences(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	mux := http.NewServeMux()
	(moduleRouter{s: s, mux: mux}).Local("/api/routines", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Platform  string `json:"platform"`
			SessionID string `json:"sessionId"`
		}
		if !httpx.ReadJSON(w, r, 4096, &req) {
			return
		}
		if req.Platform != "test" || req.SessionID != "native" {
			t.Errorf("module received public instead of owner-native reference: %+v", req)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r := httptest.NewRequest(http.MethodPost, "/api/routines", strings.NewReader(`{"platform":"test","sessionId":"`+ownedID+`"}`))
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("local module request failed: %d %s", w.Code, w.Body.String())
	}
}

func TestSessionQueryReferencesResolveBeforeFeatureHandlers(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	for _, path := range []string{"/api/artifacts?sessionId=", "/api/plugins/actions?sessionId="} {
		w := httptest.NewRecorder()
		s.withSessionViews(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			key := "sessionId"
			if q.Get(key) != "native" || q.Get("platform") != "test" {
				t.Errorf("query reference not owner-resolved: %s", r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, httptest.NewRequest(http.MethodGet, path+ownedID, nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("query failed: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestModuleRouteGuardsPreserveSessionProjection(t *testing.T) {
	for _, kind := range []string{"auth", "local", "setting", "local setting"} {
		t.Run(kind, func(t *testing.T) {
			s, _, ownedID := identityHTTPFixture(t)
			mux := http.NewServeMux()
			router := moduleRouter{s: s, mux: mux}
			handler := func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Platform  string `json:"platform"`
					SessionID string `json:"sessionId"`
				}
				if !httpx.ReadJSON(w, r, 4096, &req) {
					return
				}
				if req.SessionID != "native" || req.Platform != "test" {
					t.Fatalf("native handler received %+v", req)
				}
				httpx.WriteJSON(w, req)
			}
			switch kind {
			case "auth":
				router.Handle("/api/routines", s.requireAuth(handler))
			case "local":
				router.Local("/api/routines", handler)
			case "setting":
				router.Setting("/api/routines", handler)
			case "local setting":
				router.LocalSetting("/api/routines", handler)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/routines", strings.NewReader(`{"platform":"test","sessionId":"`+ownedID+`"}`))
			r.RemoteAddr = "127.0.0.1:1234"
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), ownedID) {
				t.Fatalf("projection lost through %s guard: %d %s", kind, w.Code, w.Body.String())
			}
		})
	}
}
