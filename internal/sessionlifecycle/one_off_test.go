package sessionlifecycle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
)

type oneOffHost struct {
	hostsvc.Host
	calls int
	err   error
}

func (h *oneOffHost) CreateOneOffDirectory(context.Context) (string, error) {
	h.calls++
	return "/owner/tmp/one-off", h.err
}

func TestOneOffDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		offline, fail bool
		status, calls int
	}{
		{"owner", `{"remoteId":"box"}`, false, false, 200, 1},
		{"local", `{"remoteId":"local"}`, false, false, 200, 1},
		{"missing owner", `{}`, false, false, 400, 0},
		{"invalid json", `{`, false, false, 400, 0},
		{"offline", `{"remoteId":"box"}`, true, false, 503, 0},
		{"allocation error", `{"remoteId":"box"}`, false, true, 502, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &oneOffHost{}
			if tc.fail {
				host.err = errors.New("no space")
			}
			m := NewModule(Deps{Owner: func(w http.ResponseWriter, dir, owner string) (hostsvc.Host, bool) {
				if dir != "" || (owner != "box" && owner != "local") {
					t.Fatalf("wrong target: %q %q", dir, owner)
				}
				if tc.offline {
					http.Error(w, "offline", http.StatusServiceUnavailable)
					return nil, false
				}
				return host, true
			}})
			w := httptest.NewRecorder()
			m.HandleOneOffDirectory(w, httptest.NewRequest(http.MethodPost, "/api/sessions/one-off", strings.NewReader(tc.body)))
			if w.Code != tc.status || host.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, host.calls, w.Body.String())
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), `"directory":"/owner/tmp/one-off"`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestOneOffRouteIsLocal(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(Deps{}).Routes(routeTestRouter{mux: mux})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sessions/one-off", strings.NewReader(`{"remoteId":"local"}`)))
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions/one-off", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}
