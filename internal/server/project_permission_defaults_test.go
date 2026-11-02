package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectsmod"
)

func TestStartProjectPermissionDefault(t *testing.T) {
	want := []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "ask"}}
	for _, worktree := range []bool{false, true} {
		t.Run(map[bool]string{false: "checkout", true: "remote worktree"}[worktree], func(t *testing.T) {
			srv, reg := startTestServer(t, nil)
			platform, owner := "opencode", "local"
			remoteOwner := &autoWorktreeOwner{}
			if worktree {
				platform, owner = "r-machine:opencode", "machine"
				srv.hostRouter = hostsvc.NewRouter(nil)
				srv.hostRouter.RegisterRemote(owner, remoteOwner)
			}
			if err := srv.stateDB.SetSetting(t.Context(), projectsmod.DefaultsKey("/repo", owner), `{"permissionMode":"auto-edit"}`); err != nil {
				t.Fatal(err)
			}
			var rules []platforms.PermissionRule
			reg.Register(&fakePlatform{PlatformID: platform, SessionList: []db.Session{mkSession(platform, "child", "t", 1)},
				CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				},
				SetPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error { rules = req.Rules; return nil },
				SendMessageFn: func(platforms.SendMessageRequest) error {
					if worktree {
						rules = remoteOwner.request.PermissionRules
					}
					if !reflect.DeepEqual(rules, want) {
						t.Fatalf("rules before first send = %#v", rules)
					}
					return nil
				},
			})
			body := `{"directory":"/repo","remoteId":"` + owner + `","worktree":` + map[bool]string{false: "false", true: "true"}[worktree] + `,"send":{"message":"hello"}}`
			w := httptest.NewRecorder()
			srv.sessionLifecycleModule().HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestStartProjectPermissionUnreadableDefaults(t *testing.T) {
	for _, raw := range []string{`{broken`, `{"permissionMode":"unknown"}`} {
		srv, reg := startTestServer(t, nil)
		reg.Register(&fakePlatform{PlatformID: "opencode", CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			t.Fatal("created a session with unreadable permissions")
			return nil, nil
		}})
		if err := srv.stateDB.SetSetting(t.Context(), projectsmod.DefaultsKey("/repo", "local"), raw); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		srv.sessionLifecycleModule().HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(`{"directory":"/repo"}`)))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}
