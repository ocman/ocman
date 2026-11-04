package sessionlifecycle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

type cloneHost struct {
	startHost
	cloned hostsvc.ProjectClone
	err    error
}

func (h *cloneHost) CloneProject(_ context.Context, req hostsvc.ProjectClone) error {
	h.cloned = req
	*h.steps = append(*h.steps, "clone")
	return h.err
}
func (h *cloneHost) PlanProjectClone(_ context.Context, req hostsvc.ProjectClone) (*hostsvc.ProjectClone, error) {
	h.cloned = req
	req.Directory = "/owner/repo"
	return &req, h.err
}

func TestStartClonesOnOwnerBeforeLaunching(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "failure", false: "success"}[fail], func(t *testing.T) {
			var steps []string
			host := &cloneHost{startHost: startHost{steps: &steps}}
			if fail {
				host.err = errors.New("check owner credentials")
			}
			svc := &startSessions{create: func(req platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
				steps = append(steps, "create")
				return &platforms.CreateSessionResponse{ID: "child"}, nil
			}}
			m := NewModule(Deps{
				Sessions: func() Sessions { return svc },
				Owner: func(w http.ResponseWriter, dir, owner string) (hostsvc.Host, bool) {
					if owner != "box" || dir != "/repo" {
						t.Fatalf("wrong owner: %q %q", dir, owner)
					}
					return host, true
				},
				CreatePlatform: func(context.Context, hostsvc.Host, string, string) (string, string, error) {
					return "r-box:claudecode", "", nil
				},
				PermissionDefaults: func(context.Context, string, string) ([]platforms.PermissionRule, error) { return nil, nil },
			})
			w := httptest.NewRecorder()
			m.HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(`{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","cloneUrl":"https://example.com/repo"}`)))
			want := []string{"clone", "ensure:claudecode:/repo", "create"}
			status := http.StatusOK
			if fail {
				want = []string{"clone"}
				status = http.StatusBadGateway
			}
			if !reflect.DeepEqual(steps, want) || w.Code != status {
				t.Fatalf("steps=%v, response=%d %s", steps, w.Code, w.Body.String())
			}
			if host.cloned.Directory != "/repo" || host.cloned.URL != "https://example.com/repo" {
				t.Fatalf("clone=%+v", host.cloned)
			}
		})
	}
}

func TestPlanCloneDoesNotCloneAndRequiresOwner(t *testing.T) {
	for _, tc := range []struct {
		body          string
		offline, fail bool
		status        int
	}{
		{`{"remoteId":"box","url":"https://example.com/repo"}`, false, false, 200},
		{`{"url":"https://example.com/repo"}`, false, false, 400},
		{`{`, false, false, 400},
		{`{"remoteId":"gone"}`, true, false, 503},
		{`{"remoteId":"box"}`, false, true, 400},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var steps []string
			host := &cloneHost{startHost: startHost{steps: &steps}}
			if tc.fail {
				host.err = errors.New("invalid clone plan")
			}
			m := NewModule(Deps{Owner: func(w http.ResponseWriter, dir, owner string) (hostsvc.Host, bool) {
				if tc.offline {
					http.Error(w, "offline", http.StatusServiceUnavailable)
					return nil, false
				}
				return host, true
			}})
			w := httptest.NewRecorder()
			m.HandlePlanProjectClone(w, httptest.NewRequest(http.MethodPost, "/api/sessions/plan-clone", strings.NewReader(tc.body)))
			if w.Code != tc.status || len(steps) != 0 {
				t.Fatalf("response=%d %s steps=%v", w.Code, w.Body.String(), steps)
			}
		})
	}
}
