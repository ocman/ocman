package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type emptyProjectRuntime struct {
	root string
}

func (rt *emptyProjectRuntime) Launch(_ context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	rt.root = spec.RepoRoot
	return &ocruntime.Instance{Endpoint: "http://127.0.0.1:7788", Kind: ocruntime.KindNativeTmux, ID: "new-project"}, nil
}

func (*emptyProjectRuntime) Probe(context.Context, *ocruntime.Instance) error { return nil }
func (*emptyProjectRuntime) Stop(context.Context, *ocruntime.Instance) error  { return nil }

func TestStartSessionEmptyProjectLaunchesInstance(t *testing.T) {
	t.Cleanup(ocv2.SetInstalledV2(false))
	dir := t.TempDir()
	rt := &emptyProjectRuntime{}
	srv, reg := newSessionsTestServer(t)
	srv.hostRouter = hostsvc.NewRouter(local.New(local.Deps{Runtime: rt}))
	reg.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			if req.Port == "" {
				return nil, platforms.ErrPlatformUnreachable
			}
			if req.Port != "7788" || req.Directory != dir {
				t.Fatalf("create = %+v", req)
			}
			return &platforms.CreateSessionResponse{ID: "new-session"}, nil
		},
	})
	body, err := json.Marshal(map[string]any{"directory": dir, "platform": "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(string(body))))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if rt.root == "" {
		t.Fatal("empty project did not launch an instance")
	}
}
