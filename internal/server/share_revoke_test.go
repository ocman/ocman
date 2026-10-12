package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRevokeShareRouteScopesTokenAndDeletesRelayCopy(t *testing.T) {
	for _, tc := range []struct {
		name, session, platform, token string
		want                           int
	}{
		{name: "valid", want: http.StatusNoContent},
		{name: "malformed", token: "bad.token", want: http.StatusBadRequest},
		{name: "overlength", token: strings.Repeat("a", 257), want: http.StatusBadRequest},
		{name: "unknown", token: "unknown-token", want: http.StatusNotFound},
		{name: "other session", session: "other-session", want: http.StatusNotFound},
		{name: "other platform", platform: "other", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay := newTestRelay(t)
			srv, registry := newSessionsTestServer(t)
			registerShareFake(registry, "session")
			registry.Register(&fakePlatform{id: "other"})
			srv.WithRelay(relay.URL, "flag")
			mux, err := srv.routes()
			if err != nil {
				t.Fatal(err)
			}
			created := httptest.NewRecorder()
			mux.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/session/session/share?platform=fake", nil))
			var view sharedLink
			if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &view) != nil {
				t.Fatalf("create = %d: %s", created.Code, created.Body.String())
			}
			original, active, err := srv.stateDB.GetActiveShareLink(t.Context(), view.Token)
			if err != nil || !active {
				t.Fatalf("created share missing: active=%v err=%v", active, err)
			}
			session, platform, token := tc.session, tc.platform, tc.token
			if session == "" {
				session = "session"
			}
			if platform == "" {
				platform = "fake"
			}
			if token == "" {
				token = view.Token
			}
			revoked := httptest.NewRecorder()
			mux.ServeHTTP(revoked, httptest.NewRequest(http.MethodDelete, "/api/session/"+session+"/share/"+token+"?platform="+platform, nil))
			if revoked.Code != tc.want {
				t.Fatalf("revoke = %d: %s; want %d", revoked.Code, revoked.Body.String(), tc.want)
			}
			remaining, active, err := srv.stateDB.GetActiveShareLink(t.Context(), view.Token)
			if err != nil || active != (tc.want != http.StatusNoContent) {
				t.Fatalf("original share active=%v err=%v after status %d", active, err, tc.want)
			}
			if active && remaining != original {
				t.Fatal("failed revocation changed the original share")
			}
			resp, err := http.Get(relay.URL + "/s/" + original.RelayID + "?from=0")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			wantRelay := http.StatusOK
			if tc.want == http.StatusNoContent {
				wantRelay = http.StatusNotFound
			}
			if resp.StatusCode != wantRelay {
				t.Fatalf("relay copy = %d, want %d", resp.StatusCode, wantRelay)
			}
		})
	}
}
