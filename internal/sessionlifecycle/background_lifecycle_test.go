package sessionlifecycle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type backgroundReadPlatform struct {
	platforms.Platform
	supported bool
	fail      bool
}

func (p backgroundReadPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{BackgroundTaskLifecycle: p.supported}
}
func (p backgroundReadPlatform) BackgroundTasks(_ context.Context, id string) (*platforms.BackgroundSnapshot, error) {
	if id != "s1" {
		panic("wrong session owner")
	}
	if p.fail {
		return nil, platforms.ErrNotFound
	}
	return &platforms.BackgroundSnapshot{Count: 1, Tasks: []db.BackgroundTask{{ID: "shell", Lifecycle: "service"}}}, nil
}
func (backgroundReadPlatform) SetBackgroundLifecycle(context.Context, platforms.SetBackgroundLifecycleRequest) error {
	return nil
}

func TestBackgroundTasksRead(t *testing.T) {
	for _, tc := range []struct {
		supported, fail bool
		status          int
	}{{true, false, 200}, {false, false, 501}, {true, true, 404}} {
		m := NewModule(Deps{WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, platforms.Platform)) {
			fn(w, r, "s1", backgroundReadPlatform{supported: tc.supported, fail: tc.fail})
		}})
		mux := http.NewServeMux()
		m.Routes(routeTestRouter{mux: mux})
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/session/s1/background", nil))
		if rr.Code != tc.status {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		if tc.status == 200 && !strings.Contains(rr.Body.String(), `"lifecycle":"service"`) {
			t.Fatal(rr.Body.String())
		}
	}
}

func TestBackgroundLifecycleRejectsMalformedJSON(t *testing.T) {
	m := NewModule(Deps{})
	mux := http.NewServeMux()
	m.Routes(routeTestRouter{mux: mux})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/session/s1/background/shell/lifecycle", strings.NewReader(`{`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}
