package sessionlifecycle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestBackgroundTaskStopRoutesToOwner(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		m := NewModule(Deps{
			Sessions: func() Sessions {
				return actionTestSessions{call: func(ctx context.Context, owner string, raw any) error {
					calls++
					req := raw.(platforms.AbortRequest)
					if ctx.Err() != nil || owner != "r-owner:opencode" || req.SessionID != "s1" || req.TaskID != "one" {
						t.Fatalf("%s %+v %v", owner, req, ctx.Err())
					}
					if fail {
						return platforms.ErrUnsupported
					}
					return nil
				}}
			},
		})
		mux := http.NewServeMux()
		m.Routes(routeTestRouter{mux: mux})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/session/s1/background/one/stop?platform=r-owner:opencode", nil).WithContext(ctx))
		want := http.StatusNoContent
		if fail {
			want = http.StatusNotImplemented
		}
		if rr.Code != want || calls != 1 {
			t.Fatalf("status=%d calls=%d", rr.Code, calls)
		}
	}
}

func (s actionTestSessions) SetBackgroundLifecycle(c context.Context, p string, r platforms.SetBackgroundLifecycleRequest) error {
	return s.call(c, p, r)
}

func TestBackgroundLifecycleRoutesToOwner(t *testing.T) {
	for _, fail := range []bool{false, true} {
		calls := 0
		m := NewModule(Deps{Sessions: func() Sessions {
			return actionTestSessions{call: func(_ context.Context, owner string, raw any) error {
				calls++
				req := raw.(platforms.SetBackgroundLifecycleRequest)
				if owner != "r-owner:opencode" || req.SessionID != "s1" || req.TaskID != "shell" || req.Lifecycle != "service" {
					t.Fatalf("%s %+v", owner, req)
				}
				if fail {
					return platforms.ErrRemoteOffline
				}
				return nil
			}}
		}})
		mux := http.NewServeMux()
		m.Routes(routeTestRouter{mux: mux})
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/session/s1/background/shell/lifecycle?platform=r-owner:opencode", strings.NewReader(`{"lifecycle":"service"}`)))
		want := http.StatusNoContent
		if fail {
			want = http.StatusServiceUnavailable
		}
		if rr.Code != want || calls != 1 {
			t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body.String())
		}
	}
}
