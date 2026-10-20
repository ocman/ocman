package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/google/uuid"
)

func TestSessionReferenceErrorsKeepHTTPStatusAtQueryAndBodyBoundaries(t *testing.T) {
	s, _, _ := identityHTTPFixture(t)
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	pending, _, err := catalogue.Reserve(t.Context(), sessioncatalog.Reservation{RequestKey: "pending-error", PayloadHash: strings.Repeat("a", 64), OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		code     int
		cancel   bool
	}{
		{"missing", "ocm_" + uuid.NewString(), 404, false},
		{"creating", pending.ID, 409, false},
		{"cancelled", pending.ID, 499, true},
	} {
		for _, body := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: " query", true: " body"}[body], func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/api/artifacts?sessionId="+tc.id, nil)
				if body {
					r = httptest.NewRequest(http.MethodPost, "/api/routines", strings.NewReader(`{"sessionId":"`+tc.id+`"}`))
				}
				if tc.cancel {
					ctx, cancel := context.WithCancel(r.Context())
					cancel()
					r = r.WithContext(ctx)
				}
				w := httptest.NewRecorder()
				s.withSessionViews(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						SessionID string `json:"sessionId"`
					}
					if !body || httpx.ReadJSON(w, r, 4096, &req) {
						t.Error("failed identity reached feature handler")
					}
				})(w, r)
				if w.Code != tc.code || w.Header().Get("X-Ocman-Conversation-Error") != "" {
					t.Fatalf("identity error misclassified: %d %s", w.Code, w.Body.String())
				}
			})
		}
	}
}
