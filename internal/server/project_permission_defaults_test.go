package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
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
			if err := srv.stateDB.SetSetting(t.Context(), projectDefaultsKey("/repo", owner), `{"permissionMode":"auto-edit"}`); err != nil {
				t.Fatal(err)
			}
			var rules []platforms.PermissionRule
			reg.Register(&fakePlatform{id: platform, sessions: []db.Session{mkSession(platform, "child", "t", 1)},
				createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				},
				setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error { rules = req.Rules; return nil },
				sendMessageFn: func(platforms.SendMessageRequest) error {
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
			srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestProjectPermissionPresets(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want []platforms.PermissionRule
	}{
		{"", nil},
		{"default", nil},
		{"plan", []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}, {Permission: "bash", Pattern: "*", Action: "deny"}}},
		{"auto-edit", []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "ask"}}},
		{"yolo", []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			d := projectDefaults{PermissionMode: tc.mode}
			got, err := d.permissionRules()
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rules = %#v, err = %v", got, err)
			}
			if err := d.validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStartProjectPermissionUnreadableDefaults(t *testing.T) {
	for _, raw := range []string{`{broken`, `{"permissionMode":"unknown"}`} {
		srv, reg := startTestServer(t, nil)
		reg.Register(&fakePlatform{id: "opencode", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			t.Fatal("created a session with unreadable permissions")
			return nil, nil
		}})
		if err := srv.stateDB.SetSetting(t.Context(), projectDefaultsKey("/repo", "local"), raw); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(`{"directory":"/repo"}`)))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}
