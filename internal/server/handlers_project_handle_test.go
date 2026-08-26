package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type projectHandleRemoteHost struct {
	hostsvc.Host
	worktree    *hostsvc.WorktreeSessionResult
	upstreams   *hostsvc.ProjectUpstreams
	fetch       hostsvc.FetchPRHeadRequest
	deadline    time.Time
	hasDeadline bool
}

func (h *projectHandleRemoteHost) RemoteID() string { return "rem1" }

func (h *projectHandleRemoteHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:4321"}, nil
}

func (h *projectHandleRemoteHost) CreateWorktreeSession(context.Context, hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	return h.worktree, nil
}

func (h *projectHandleRemoteHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	return h.upstreams, nil
}

func (h *projectHandleRemoteHost) FetchPRHead(ctx context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
	h.fetch = req
	h.deadline, h.hasDeadline = ctx.Deadline()
	return "ocman/pr-42", nil
}

// TestHandleProjectHandle_SessionMode verifies the happy path:
//
//   - Frontend POSTs {dir, remote, type:"issue", number, mode:"session"}.
//   - Backend resolves the upstream, fetches the issue, renders the
//     template, creates a session, and returns its ID.
//
// fakePlatform's createSessionFn / sendMessageFn capture the call so
// the test can verify the prompt was rendered against the issue body.
func TestHandleProjectHandle_SessionMode(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	var capturedPrompt string
	fp := &fakePlatform{
		id: "opencode",
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "child-123"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			capturedPrompt = req.Message
			return nil
		},
	}
	// Override the real OpenCode adapter with our fake.
	srv.registry.Register(fp)

	body := `{
		"dir": "` + dir + `",
		"remote": "origin",
		"type": "issue",
		"number": 7,
		"mode": "session"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		ChildSessionID string `json:"childSessionId"`
		Mode           string `json:"mode"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ChildSessionID != "child-123" || resp.Mode != "session" {
		t.Errorf("unexpected response: %+v", resp)
	}
	// The captured prompt should reference the issue's title.
	if capturedPrompt == "" {
		t.Errorf("expected SendMessage to be called with a prompt")
	}
	expectedTitle := "Bug report"
	if !bytes.Contains([]byte(capturedPrompt), []byte(expectedTitle)) {
		t.Errorf("expected prompt to contain %q, got: %s", expectedTitle, capturedPrompt)
	}
}

func TestHandleProjectHandle_SessionModeUsesOwningRemotePlatform(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/only/repo"
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	var created platforms.CreateSessionRequest
	var sent platforms.SendMessageRequest
	srv.registry.Register(&fakePlatform{
		id: "r-rem1:opencode",
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			created = req
			return &platforms.CreateSessionResponse{ID: "remote-session"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil },
	})
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams(dir)}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	body := `{"dir":"` + dir + `","remote":"origin","remoteId":"rem1","type":"issue","number":7,"mode":"session"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if created.Directory != dir || created.Port != "4321" {
		t.Fatalf("remote create = %+v", created)
	}
	if sent.SessionID != "remote-session" || sent.Message == "" {
		t.Fatalf("remote send = %+v", sent)
	}
	var response struct {
		Platform string `json:"platform"`
		RemoteID string `json:"remoteId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Platform != "r-rem1:opencode" || response.RemoteID != "rem1" {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleProjectHandle_SendFailureReturnsBadGateway(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc
	srv.registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "child"}, nil
		},
		sendMessageFn: func(platforms.SendMessageRequest) error { return errors.New("send failed") },
	})

	body := `{"dir":"` + dir + `","remote":"origin","type":"issue","number":7,"mode":"session"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectHandle_CrossForkFetchRunsOnOwner(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/only/repo"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":42,"title":"Forked PR","state":"open","user":{"login":"carol"},"head":{"ref":"wip","repo":{"full_name":"carol/fork"}},"base":{"repo":{"full_name":"alice/myproj"}}}]`))
	}))
	defer gh.Close()
	srv.integrations.GitHub = newGitHubTestClient(gh)
	srv.registry.Register(&fakePlatform{id: "r-rem1:opencode", sendMessageFn: func(platforms.SendMessageRequest) error { return nil }})
	host := &projectHandleRemoteHost{
		upstreams: githubProjectUpstreams(dir),
		worktree:  &hostsvc.WorktreeSessionResult{SessionID: "child", WorktreePath: "/remote/wt", Branch: "ocman/pr-42"},
	}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	body := `{"dir":"` + dir + `","remote":"origin","remoteId":"rem1","type":"pr","number":42,"mode":"worktree","fetchHead":true}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if host.fetch != (hostsvc.FetchPRHeadRequest{RepoRoot: dir, Remote: "origin", Number: 42}) {
		t.Fatalf("owner fetch = %+v", host.fetch)
	}
	if !host.hasDeadline || time.Until(host.deadline) <= 0 || time.Until(host.deadline) > prHeadFetchTimeout {
		t.Fatalf("owner fetch deadline = %v", host.deadline)
	}
}

func githubProjectUpstreams(root string) *hostsvc.ProjectUpstreams {
	return &hostsvc.ProjectUpstreams{RepoRoot: root, Remotes: []forge.Remote{{Name: "origin", Type: forge.RemoteTypeGitHub, Host: "github.com", Repo: "alice/myproj"}}}
}

func TestHandleProjectHandle_WorktreeModeUsesOwningRemotePlatform(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	var sent platforms.SendMessageRequest
	srv.registry.Register(&fakePlatform{
		id:            "r-rem1:opencode",
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil },
	})
	host := &projectHandleRemoteHost{worktree: &hostsvc.WorktreeSessionResult{
		SessionID: "remote-worktree-session", WorktreePath: "/remote/worktree", Branch: "issue/7-bug-report",
	}, upstreams: githubProjectUpstreams(dir)}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	body := `{"dir":"` + dir + `","remote":"origin","remoteId":"rem1","type":"issue","number":7,"mode":"worktree"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if sent.SessionID != "remote-worktree-session" || sent.Message == "" {
		t.Fatalf("remote send = %+v", sent)
	}
	var response struct {
		SessionID    string `json:"childSessionId"`
		WorktreePath string `json:"worktreePath"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID != "remote-worktree-session" || response.WorktreePath != "/remote/worktree" {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleProjectHandle_RejectsInvalidBody(t *testing.T) {
	srv := testServer(t)

	cases := map[string]string{
		"missing-dir":    `{"remote":"o","type":"pr","number":1,"mode":"session"}`,
		"missing-remote": `{"dir":"/x","type":"pr","number":1,"mode":"session"}`,
		"bad-type":       `{"dir":"/x","remote":"o","type":"weird","number":1,"mode":"session"}`,
		"bad-number":     `{"dir":"/x","remote":"o","type":"pr","number":0,"mode":"session"}`,
		"bad-mode":       `{"dir":"/x","remote":"o","type":"pr","number":1,"mode":"weird"}`,
		"bad-action":     `{"dir":"/x","remote":"o","type":"pr","number":1,"mode":"session","action":"weird"}`,
		"review-issue":   `{"dir":"/x","remote":"o","type":"issue","number":1,"mode":"session","action":"review"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.handleProjectHandle(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleProjectHandle_CrossForkRequiresConfirmation(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)

	// Build a fake GitHub server whose only PR (#42) is cross-fork.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{
			"number": 42, "title": "Forked PR", "body": "",
			"state": "open", "draft": false,
			"updated_at": "2026-05-21T14:03:11Z",
			"html_url": "",
			"user": {"login": "carol"},
			"head": {"ref": "wip", "repo": {"full_name": "carol/myproj-fork"}},
			"base": {"repo": {"full_name": "alice/myproj"}}
		}]`))
	}))
	defer gh.Close()
	srv.integrations.GitHub = newGitHubTestClient(gh)

	// Even with a fake platform, the request should fail BEFORE the
	// launch step with the 409 requires_fetch.
	srv.registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			t.Fatal("CreateSession should not be called on cross-fork without fetchHead")
			return nil, nil
		},
	})

	body := `{
		"dir": "` + dir + `",
		"remote": "origin",
		"type": "pr",
		"number": 42,
		"mode": "worktree",
		"fetchHead": false
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 (requires_fetch), got %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Error struct {
			Code        string `json:"code"`
			FetchTarget string `json:"fetchTarget"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error.Code != "requires_fetch" {
		t.Errorf("expected code=requires_fetch, got %q", envelope.Error.Code)
	}
	if envelope.Error.FetchTarget != "ocman/pr-42" {
		t.Errorf("expected fetchTarget=ocman/pr-42, got %q", envelope.Error.FetchTarget)
	}
}

func TestIssueBranchName_VariousTitles(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"", "issue/7"},
		{"Simple title", "issue/7-simple-title"},
		{"  Padded ", "issue/7-padded"},
		{"!!!", "issue/7"},
		{"Make the foo do the bar", "issue/7-make-the-foo-do-the-bar"},
		// Slug is capped at 40 chars (not counting the "issue/<n>-" prefix);
		// trailing partial words become a partial slug + trim.
		{"Long title with many many many many many many many words to truncate", "issue/7-long-title-with-many-many-many-many-many"},
	}
	for _, tc := range cases {
		got := issueBranchName(7, tc.title)
		if got != tc.want {
			t.Errorf("issueBranchName(7, %q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

// newGitHubTestClient is a tiny helper for tests that already have an
// httptest.Server in hand.
func newGitHubTestClient(srv *httptest.Server) *github.Client {
	return github.NewForTest(srv.URL, "test-token", srv.Client())
}
