package projectforge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type worktreeHost struct {
	hostsvc.Host
	got hostsvc.WorktreeSessionRequest
}

func (h *worktreeHost) RemoteID() string { return "" }
func (h *worktreeHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.got = req
	return &hostsvc.WorktreeSessionResult{SessionID: "s1", WorktreePath: "/wt", Branch: req.Branch}, nil
}

type nopSessions struct{}

func (nopSessions) Create(context.Context, string, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return nil, nil
}
func (nopSessions) SendMessage(context.Context, string, platforms.SendMessageRequest) error {
	return nil
}

// A PR launched into a worktree runs on the chosen platform: the owner gets
// the base platform, the response the compound one.
func TestLaunchWorktreeUsesPlatform(t *testing.T) {
	m := newTestModule()
	m.Sessions = nopSessions{}
	host := &worktreeHost{}
	w := httptest.NewRecorder()
	m.launchWorktree(w, httptest.NewRequest(http.MethodPost, "/", nil), handleRequest{Type: "pr", Number: 7},
		host, "r-x:claudecode", forge.Remote{}, nil, "/repo", "go", map[string]string{}, &forge.PR{Number: 7, Branch: "feature/login"})
	var res map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK {
		t.Fatalf("launch = %d %s", w.Code, w.Body.String())
	}
	if host.got.Platform != "claudecode" || host.got.Branch != "feature/login" || res["platform"] != "r-x:claudecode" {
		t.Fatalf("request = %+v, response = %+v", host.got, res)
	}
}

func (h *worktreeHost) EnsureRuntime(context.Context, string, string) (*hostsvc.EnsureRuntimeResult, error) {
	return nil, errors.New("provider launch failed")
}

type countingSessions struct {
	nopSessions
	creates int
}

func (s *countingSessions) Create(context.Context, string, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	s.creates++
	return &platforms.CreateSessionResponse{ID: "child"}, nil
}

// A provider whose runtime fails to launch gets no session; OpenCode keeps
// its old best-effort behaviour (the adapter finds an instance itself).
func TestLaunchSessionStopsOnProviderRuntimeFailure(t *testing.T) {
	for platform, wantCreates := range map[string]int{"claudecode": 0, "opencode": 1} {
		m := newTestModule()
		sessions := &countingSessions{}
		m.Sessions = sessions
		w := httptest.NewRecorder()
		m.launchSession(w, httptest.NewRequest(http.MethodPost, "/", nil), handleRequest{Dir: "/repo"}, &worktreeHost{}, platform, "go")
		if sessions.creates != wantCreates || (wantCreates == 0) != (w.Code != http.StatusOK) {
			t.Fatalf("%s: creates = %d, status %d", platform, sessions.creates, w.Code)
		}
	}
}
