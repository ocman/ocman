package sessionlifecycle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
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
			WithSessionPath: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, string)) {
				fn(w, r, r.PathValue("id"), "")
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
