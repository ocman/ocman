package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreateShareReportsRelayFailure covers the reason this exists: a
// failed publish used to surface as a bare 500 "internal server error",
// which tells the user nothing about a problem that is almost always
// operational. Each failure mode must name itself.
func TestCreateShareReportsRelayFailure(t *testing.T) {
	tests := []struct {
		name       string
		relay      http.HandlerFunc
		wantStatus int
		wantBody   string
	}{
		{
			name: "size cap is reported as too large",
			relay: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"20260813-AAAAAAAAAAAAAAAAAAAAAA","deleteToken":"tok"}`))
					return
				}
				http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantBody:   "too large for the share relay",
		},
		{
			name: "rate limiting is reported as rate limiting",
			relay: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "too many shares created", http.StatusTooManyRequests)
			},
			wantStatus: http.StatusTooManyRequests,
			wantBody:   "rate limiting",
		},
		{
			name: "an unexpected relay status is passed through",
			relay: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "relay exploded", http.StatusInternalServerError)
			},
			wantStatus: http.StatusBadGateway,
			wantBody:   "relay exploded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			relay := httptest.NewServer(tc.relay)
			defer relay.Close()

			srv, reg := newSessionsTestServer(t)
			registerShareFake(reg, "ses_relay_err")
			srv.WithRelay(relay.URL, "flag")

			rr := httptest.NewRecorder()
			srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/ses_relay_err/share", nil))

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body)
			}
			if !strings.Contains(rr.Body.String(), tc.wantBody) {
				t.Fatalf("body %q does not explain the failure (want %q)", rr.Body.String(), tc.wantBody)
			}
			if strings.Contains(rr.Body.String(), "internal server error") {
				t.Fatalf("failure was hidden behind a generic error: %s", rr.Body)
			}
		})
	}
}

// TestCreateShareReportsUnreachableRelay is the most common case in
// practice: the relay simply is not running.
func TestCreateShareReportsUnreachableRelay(t *testing.T) {
	relay := httptest.NewServer(http.NotFoundHandler())
	url := relay.URL
	relay.Close() // nothing is listening now

	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_down")
	srv.WithRelay(url, "flag")

	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/ses_down/share", nil))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "unreachable") || !strings.Contains(rr.Body.String(), url) {
		t.Fatalf("body %q should name the unreachable relay", rr.Body.String())
	}
}

// TestCreateShareRollsBackOnRelayFailure proves a failed publish leaves
// no orphan link behind, since a local row without its relay copy would
// render as a share that resolves to nothing.
func TestCreateShareRollsBackOnRelayFailure(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer relay.Close()

	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_rollback")
	srv.WithRelay(relay.URL, "flag")

	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/ses_rollback/share", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}

	links, err := srv.stateDB.ListActiveShareLinks(t.Context(), "fake", "ses_rollback")
	if err != nil {
		t.Fatalf("ListActiveShareLinks: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("failed publish left %d active link(s) behind", len(links))
	}
}

// TestCreateShareWithoutRelayExplainsHowToConfigureIt keeps the
// "sharing is off" message actionable rather than a bare refusal.
func TestCreateShareWithoutRelayExplainsHowToConfigureIt(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	registerShareFake(reg, "ses_norelay")

	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, httptest.NewRequest(http.MethodPost, "/api/session/ses_norelay/share", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	for _, want := range []string{"-relay-url", "OCMAN_RELAY_URL"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("body %q should mention %s", rr.Body.String(), want)
		}
	}
}
