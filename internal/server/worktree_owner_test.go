package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ownerHost records which machine served a worktree / ensure call. Every
// machine in these tests has the *same* directory, so only the owner
// identity can tell them apart.
type ownerHost struct {
	hostsvc.Host
	id string

	mu    sync.Mutex
	calls []string
}

func (h *ownerHost) RemoteID() string { return h.id }

func (h *ownerHost) record(op string) {
	h.mu.Lock()
	h.calls = append(h.calls, op)
	h.mu.Unlock()
}

func (h *ownerHost) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func (h *ownerHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	h.record("list")
	return []git.Worktree{{Path: "/repo", Main: true}}, nil
}

func (h *ownerHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) {
	h.record("base-ref")
	return h.id + "/main", nil
}

func (h *ownerHost) RemoveWorktree(context.Context, hostsvc.RemoveWorktreeRequest) error {
	h.record("remove")
	return nil
}

func (h *ownerHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.record("create")
	return &hostsvc.WorktreeSessionResult{SessionID: "ses_" + h.id, WorktreePath: "/.worktrees/repo/x", Branch: req.Branch}, nil
}

func (h *ownerHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.record("ensure:" + req.ProjectDir)
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir}, nil
}

// ownerRouter wires local + remotes A and B, all sharing /repo. The
// inventory resolver answers `inferred` for every directory, standing in
// for a stale, missing ("") or ambiguous inventory.
func ownerRouter(inferred string) (*hostsvc.Router, map[string]*ownerHost) {
	hosts := map[string]*ownerHost{"local": {id: "local"}, "A": {id: "A"}, "B": {id: "B"}}
	r := hostsvc.NewRouter(hosts["local"])
	r.RegisterRemote("A", hosts["A"])
	r.RegisterRemote("B", hosts["B"])
	r.SetDirResolver(func(string) string { return inferred })
	return r, hosts
}

// served returns which host ids recorded any call.
func served(hosts map[string]*ownerHost) []string {
	var out []string
	for _, id := range []string{"local", "A", "B"} {
		if len(hosts[id].got()) > 0 {
			out = append(out, id)
		}
	}
	return out
}

func TestWorktreeRoutes_ExplicitOwnerBeatsDirectoryInference(t *testing.T) {
	routes := []struct {
		name string
		call func(srv *Server, owner string) *httptest.ResponseRecorder
	}{
		{"list", func(srv *Server, owner string) *httptest.ResponseRecorder {
			rr := httptest.NewRecorder()
			srv.handleWorktreeList(rr, httptest.NewRequest(http.MethodGet, "/api/worktree/list?dir=/repo&remoteId="+owner, nil))
			return rr
		}},
		{"default-base-ref", func(srv *Server, owner string) *httptest.ResponseRecorder {
			rr := httptest.NewRecorder()
			srv.handleWorktreeDefaultBaseRef(rr, httptest.NewRequest(http.MethodGet, "/api/worktree/default-base-ref?dir=/repo&remoteId="+owner, nil))
			return rr
		}},
		{"remove", func(srv *Server, owner string) *httptest.ResponseRecorder {
			rr := httptest.NewRecorder()
			body := `{"projectDir":"/repo","path":"/.worktrees/repo/x","remoteId":"` + owner + `"}`
			srv.handleWorktreeRemove(rr, httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(body)))
			return rr
		}},
		{"create", func(srv *Server, owner string) *httptest.ResponseRecorder {
			rr := httptest.NewRecorder()
			body := `{"projectDir":"/repo","branch":"x","remoteId":"` + owner + `"}`
			srv.handleWorktreeCreateAndLaunch(rr, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
			return rr
		}},
	}
	cases := []struct {
		owner, inferred string
		wantStatus      int
		wantServed      []string
	}{
		// Local stays local even when the inventory claims a remote owns /repo.
		{"local", "A", http.StatusOK, []string{"local"}},
		// A named remote wins over a stale/ambiguous inventory answer.
		{"B", "A", http.StatusOK, []string{"B"}},
		// ...and over a missing one, which would otherwise mean "local".
		{"B", "", http.StatusOK, []string{"B"}},
		// A disconnected owner fails closed; no machine runs the action.
		{"gone", "", http.StatusServiceUnavailable, nil},
	}
	for _, route := range routes {
		for _, tc := range cases {
			t.Run(route.name+"/"+tc.owner+"/inferred="+tc.inferred, func(t *testing.T) {
				srv := &Server{}
				r, hosts := ownerRouter(tc.inferred)
				srv.hostRouter = r
				rr := route.call(srv, tc.owner)
				if rr.Code != tc.wantStatus {
					t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body)
				}
				if got := served(hosts); strings.Join(got, ",") != strings.Join(tc.wantServed, ",") {
					t.Fatalf("served by %v, want %v", got, tc.wantServed)
				}
				if tc.wantStatus == http.StatusServiceUnavailable && !strings.Contains(rr.Body.String(), "remote_not_connected") {
					t.Fatalf("body = %s, want remote_not_connected", rr.Body)
				}
			})
		}
	}
}

// The created session must be addressable on its owner: the response
// names the compound platform so the UI seeds and links it correctly.
func TestWorktreeCreate_ReturnsOwnerIdentity(t *testing.T) {
	for _, tc := range []struct{ owner, platform string }{
		{"local", "opencode"},
		{"B", "r-B:opencode"},
	} {
		srv := &Server{}
		r, _ := ownerRouter("A")
		srv.hostRouter = r
		rr := httptest.NewRecorder()
		body := `{"projectDir":"/repo","branch":"x","remoteId":"` + tc.owner + `"}`
		srv.handleWorktreeCreateAndLaunch(rr, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
		var resp struct{ SessionID, Platform, RemoteID string }
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rr.Body)
		}
		if resp.Platform != tc.platform || resp.RemoteID != tc.owner || resp.SessionID != "ses_"+tc.owner {
			t.Fatalf("owner %s: resp = %+v, want platform %s", tc.owner, resp, tc.platform)
		}
	}
}

// Relaunching an unreachable session's instance must run on the machine
// that owns the session (its adapter), never on whatever the directory
// inventory guesses for an identical path.
func TestRelaunchOpencodeForSession_UsesAdapterOwner(t *testing.T) {
	cases := []struct {
		name, platform, inferred string
		want                     []string
		wantOK                   bool
	}{
		{"remote session, inventory points at another remote", "r-B:opencode", "A", []string{"B"}, true},
		{"remote session, inventory missing", "r-B:opencode", "", []string{"B"}, true},
		{"local session, remote shares the path", "opencode", "A", []string{"local"}, true},
		{"remote disconnected", "r-gone:opencode", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			reg.Register(&fakePlatform{
				id:       tc.platform,
				sessions: []db.Session{mkSession(tc.platform, "s1", "t", 1)},
				sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
					return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/home/u/.worktrees/repo/feat"}}, nil
				},
			})
			r, hosts := ownerRouter(tc.inferred)
			srv.hostRouter = r
			if ok := srv.relaunchOpencodeForSession(t.Context(), tc.platform, "s1"); ok != tc.wantOK {
				t.Fatalf("relaunch ok = %v, want %v", ok, tc.wantOK)
			}
			if got := served(hosts); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("relaunched on %v, want %v", got, tc.want)
			}
			for _, id := range tc.want {
				if calls := hosts[id].got(); len(calls) != 1 || calls[0] != "ensure:/home/u/repo" {
					t.Fatalf("%s calls = %v, want folded ensure:/home/u/repo", id, calls)
				}
			}
		})
	}
}
