package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type draftOwnerHost struct {
	hostsvc.Host
	id      string
	ensures int
}

func (h *draftOwnerHost) RemoteID() string { return h.id }
func (h *draftOwnerHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.ensures++
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:7788", RepoRoot: req.ProjectDir}, nil
}

func TestDraftLaunchExplicitOwner(t *testing.T) {
	for _, endpoint := range []string{"prepare", "start"} {
		for _, tc := range []struct {
			name, owner, platform string
			status                int
		}{
			{"remote without platform", "box", "", http.StatusOK},
			{"remote with platform", "box", "r-box:opencode", http.StatusOK},
			{"local owner remote platform", "local", "r-box:opencode", http.StatusBadRequest},
			{"remote owner local platform", "box", "opencode", http.StatusBadRequest},
			{"different remote", "box", "r-other:opencode", http.StatusBadRequest},
			{"disconnected", "gone", "", http.StatusServiceUnavailable},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				srv, reg := newSessionsTestServer(t)
				local, owner := &draftOwnerHost{id: "local"}, &draftOwnerHost{id: "box"}
				srv.hostRouter = hostsvc.NewRouter(local)
				srv.hostRouter.RegisterRemote("box", owner)
				reg.Register(&fakePlatform{id: "opencode", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					t.Fatal("an explicit remote draft must never create locally")
					return nil, nil
				}})
				reg.Register(&fakePlatform{id: "r-box:opencode", createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					if req.Directory != "/repo" {
						t.Fatalf("wrong checkout: %q", req.Directory)
					}
					return &platforms.CreateSessionResponse{ID: "remote-created"}, nil
				}})
				body, _ := json.Marshal(map[string]string{"directory": "/repo", "remoteId": tc.owner, "platform": tc.platform})
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+endpoint, strings.NewReader(string(body)))
				if endpoint == "prepare" {
					srv.handlePrepareSession(w, r)
				} else {
					srv.handleStartSession(w, r)
				}
				if w.Code != tc.status {
					t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
				}
				if local.ensures != 0 {
					t.Fatalf("hub launched %d times", local.ensures)
				}
				if tc.status == http.StatusOK {
					if owner.ensures != 1 || !strings.Contains(w.Body.String(), `"platform":"r-box:opencode"`) {
						t.Fatalf("owner calls=%d: %s", owner.ensures, w.Body.String())
					}
				} else if owner.ensures != 0 {
					t.Fatalf("rejected request launched owner %d times", owner.ensures)
				}
			})
		}
	}
}
