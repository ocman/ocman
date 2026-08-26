package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type projectHandleRemoteHost struct {
	hostsvc.Host
	worktree      *hostsvc.WorktreeSessionResult
	worktreeErr   error
	upstreams     *hostsvc.ProjectUpstreams
	fetch         hostsvc.FetchPRHeadRequest
	fetchBranch   string
	fetchErr      error
	deadline      time.Time
	hasDeadline   bool
	upstreamErr   error
	upstreamCalls int
	onUpstream    func()
	remoteID      string
}

func (h *projectHandleRemoteHost) RemoteID() string {
	if h.remoteID != "" {
		return h.remoteID
	}
	return "rem1"
}

func (h *projectHandleRemoteHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:4321"}, nil
}

func (h *projectHandleRemoteHost) CreateWorktreeSession(context.Context, hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	return h.worktree, h.worktreeErr
}

func (h *projectHandleRemoteHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	h.upstreamCalls++
	if h.onUpstream != nil {
		h.onUpstream()
	}
	return h.upstreams, h.upstreamErr
}

func TestHandleProjectHandleRequiresExplicitOwnerAndAbsoluteDir(t *testing.T) {
	srv := testServer(t)
	for _, body := range []string{
		`{"dir":"/repo","remote":"origin","type":"issue","number":7,"mode":"session"}`,
		`{"dir":"relative","remote":"origin","remoteId":"local","type":"issue","number":7,"mode":"session"}`,
	} {
		rr := httptest.NewRecorder()
		srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body = %s: status = %d, response = %s", body, rr.Code, rr.Body.String())
		}
	}
}

func TestProjectRoutes_ListAndLaunchThroughPublicMux(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc
	var sent platforms.SendMessageRequest
	srv.registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "child"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil },
	})
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/project/issues?dir="+dir+"&remoteId=local&remote=origin", nil)
	listReq.RemoteAddr, listReq.Host = "127.0.0.1:1234", "localhost"
	listReq.Header.Set("Origin", "http://localhost")
	listRR := httptest.NewRecorder()
	mux.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK || !strings.Contains(listRR.Body.String(), `"number":7`) {
		t.Fatalf("list status = %d, body = %s", listRR.Code, listRR.Body.String())
	}

	body := `{"dir":"` + dir + `","remoteId":"local","remote":"origin","type":"issue","number":7,"mode":"session"}`
	launchReq := httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body))
	launchReq.RemoteAddr, launchReq.Host = "127.0.0.1:1234", "localhost"
	launchReq.Header.Set("Origin", "http://localhost")
	launchRR := httptest.NewRecorder()
	mux.ServeHTTP(launchRR, launchReq)
	if launchRR.Code != http.StatusOK || sent.SessionID != "child" {
		t.Fatalf("launch status = %d, sent = %+v, body = %s", launchRR.Code, sent, launchRR.Body.String())
	}
}

func TestProjectForgeRoutesRejectNonLocalPeersThroughPublicMux(t *testing.T) {
	srv := testServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/project/prs",
		"/api/project/issues",
		"/api/project/pr-checks",
		"/api/project/forge-user",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "192.0.2.1:1234"
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
			}
		})
	}
}

func TestHandleProjectHandle_WorktreePRFetchesMetadataOnce(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/only/repo"
	requests := 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"number":42,"title":"Patch","state":"open","user":{"login":"alice"},"head":{"ref":"patch","repo":{"full_name":"alice/myproj"}},"base":{"repo":{"full_name":"alice/myproj"}}}`))
	}))
	defer gh.Close()
	srv.integrations.GitHub = newGitHubTestClient(gh)
	srv.registry.Register(&fakePlatform{id: "r-rem1:opencode", sendMessageFn: func(platforms.SendMessageRequest) error { return nil }})
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams(dir), worktree: &hostsvc.WorktreeSessionResult{SessionID: "child", WorktreePath: "/remote/wt", Branch: "patch"}}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	body := `{"dir":"` + dir + `","remote":"origin","remoteId":"rem1","type":"pr","number":42,"mode":"worktree"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if requests != 1 {
		t.Fatalf("PR metadata requests = %d, want 1", requests)
	}
}

func (h *projectHandleRemoteHost) FetchPRHead(ctx context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
	h.fetch = req
	h.deadline, h.hasDeadline = ctx.Deadline()
	branch := h.fetchBranch
	if branch == "" {
		branch = "ocman/pr-42"
	}
	return branch, h.fetchErr
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
		"remoteId": "local",
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
	if !strings.Contains(capturedPrompt, "broken") {
		t.Errorf("expected prompt to retain the issue body, got: %s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "untrusted forge content") {
		t.Errorf("expected prompt to mark forge content untrusted, got: %s", capturedPrompt)
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

func TestHandleProjectHandle_SendFailureReturnsCreatedSession(t *testing.T) {
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

	body := `{"dir":"` + dir + `","remoteId":"local","remote":"origin","type":"issue","number":7,"mode":"session"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"childSessionId":"child"`) || !strings.Contains(rr.Body.String(), `"promptError"`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectHandle_FallsBackWhenMetadataIsUnavailable(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer gh.Close()
	srv.integrations.GitHub = newGitHubTestClient(gh)
	var prompt string
	srv.registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "child"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { prompt = req.Message; return nil },
	})
	body := `{"dir":"` + dir + `","remoteId":"local","remote":"origin","type":"issue","number":7,"mode":"session","intent":"check logs"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK || !strings.Contains(prompt, "Please handle issue #7 on github.com/alice/myproj") || !strings.Contains(prompt, "check logs") || !strings.Contains(prompt, "untrusted forge content") {
		t.Fatalf("status = %d, prompt = %q, body = %s", rr.Code, prompt, rr.Body.String())
	}
}

func TestHandleProjectHandle_LooksUpSelectedPRDirectly(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/alice/myproj/pulls/101" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 101, "title": "Later PR", "state": "open", "user": map[string]string{"login": "alice"},
			"head": map[string]any{"ref": "later", "repo": map[string]string{"full_name": "alice/myproj"}},
			"base": map[string]any{"repo": map[string]string{"full_name": "alice/myproj"}},
		})
	}))
	defer gh.Close()
	srv.integrations.GitHub = newGitHubTestClient(gh)
	var prompt string
	srv.registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "child"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { prompt = req.Message; return nil },
	})
	body := `{"dir":"` + dir + `","remoteId":"local","remote":"origin","type":"pr","number":101,"mode":"session"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK || !strings.Contains(prompt, "Later PR") {
		t.Fatalf("status = %d, prompt = %q, body = %s", rr.Code, prompt, rr.Body.String())
	}
}

func TestHandleProjectHandle_ReportsLaunchErrors(t *testing.T) {
	for _, mode := range []string{"session", "worktree"} {
		t.Run(mode, func(t *testing.T) {
			srv := testServer(t)
			dir := "/remote/repo"
			gh, ghc := fakeGitHubServer(t)
			defer gh.Close()
			srv.integrations.GitHub = ghc
			host := &projectHandleRemoteHost{
				upstreams:   githubProjectUpstreams(dir),
				worktreeErr: errors.New("worktree failed"),
			}
			srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
			srv.hostRouter.RegisterRemote("rem1", host)
			srv.registry.Register(&fakePlatform{
				id: "r-rem1:opencode",
				createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					return nil, errors.New("create failed")
				},
			})
			itemType, number := "issue", 7
			if mode == "worktree" {
				itemType, number = "pr", 42
			}
			body := fmt.Sprintf(`{"dir":%q,"remoteId":"rem1","remote":"origin","type":%q,"number":%d,"mode":%q}`, dir, itemType, number, mode)
			rr := httptest.NewRecorder()
			srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
			if rr.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleProjectHandle_RejectsInvalidWorktreeResult(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/repo"
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams(dir)}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)
	srv.registry.Register(&fakePlatform{id: "r-rem1:opencode"})
	body := `{"dir":"/remote/repo","remoteId":"rem1","remote":"origin","type":"issue","number":7,"mode":"worktree"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectHandle_RequiresRegisteredPlatform(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/repo"
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams(dir)}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)
	body := `{"dir":"/remote/repo","remoteId":"rem1","remote":"origin","type":"issue","number":7,"mode":"session"}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectHandle_RejectsUnavailableDependencies(t *testing.T) {
	body := `{"dir":"/remote/repo","remoteId":"rem1","remote":"origin","type":"issue","number":7,"mode":"session"}`
	t.Run("owner", func(t *testing.T) {
		srv := testServer(t)
		rr := httptest.NewRecorder()
		srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", rr.Code)
		}
	})
	t.Run("upstreams", func(t *testing.T) {
		srv := testServer(t)
		host := &projectHandleRemoteHost{upstreamErr: errors.New("inspection failed")}
		srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
		srv.hostRouter.RegisterRemote("rem1", host)
		rr := httptest.NewRecorder()
		srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
		if rr.Code != http.StatusBadGateway {
			t.Fatalf("status = %d", rr.Code)
		}
	})
	t.Run("forge client", func(t *testing.T) {
		srv := testServer(t)
		srv.integrations.GitHub = nil
		host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams("/remote/repo")}
		srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
		srv.hostRouter.RegisterRemote("rem1", host)
		rr := httptest.NewRecorder()
		srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d", rr.Code)
		}
	})
	t.Run("registry", func(t *testing.T) {
		srv := testServer(t)
		gh, ghc := fakeGitHubServer(t)
		defer gh.Close()
		srv.integrations.GitHub = ghc
		host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams("/remote/repo")}
		srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
		srv.hostRouter.RegisterRemote("rem1", host)
		srv.registry = nil
		rr := httptest.NewRecorder()
		srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d", rr.Code)
		}
	})
}

func TestHandleProjectHandle_CrossForkFetchRunsOnOwner(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/only/repo"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"number":42,"title":"Forked PR","state":"open","user":{"login":"carol"},"head":{"ref":"wip","repo":{"full_name":"carol/fork"}},"base":{"repo":{"full_name":"alice/myproj"}}}`))
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

func TestHandleProjectHandle_CrossForkFetchFailureIsSanitized(t *testing.T) {
	srv := testServer(t)
	dir := "/remote/only/repo"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"number":42,"title":"Forked PR","state":"open","user":{"login":"carol"},"head":{"ref":"wip","repo":{"full_name":"carol/fork"}},"base":{"repo":{"full_name":"alice/myproj"}}}`))
	}))
	defer gh.Close()
	srv.integrations.GitHub = newGitHubTestClient(gh)
	srv.registry.Register(&fakePlatform{id: "r-rem1:opencode"})
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams(dir), fetchErr: errors.New("credential-secret")}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	body := `{"dir":"` + dir + `","remote":"origin","remoteId":"rem1","type":"pr","number":42,"mode":"worktree","fetchHead":true}`
	rr := httptest.NewRecorder()
	srv.handleProjectHandle(rr, httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(body)))
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), "credential-secret") {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
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
		_, _ = w.Write([]byte(`{
			"number": 42, "title": "Forked PR", "body": "",
			"state": "open", "draft": false,
			"updated_at": "2026-05-21T14:03:11Z",
			"html_url": "",
			"user": {"login": "carol"},
			"head": {"ref": "wip", "repo": {"full_name": "carol/myproj-fork"}},
			"base": {"repo": {"full_name": "alice/myproj"}}
		}`))
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
		"remoteId": "local",
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
