package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type deadlineForge struct {
	forge.Forge
	deadline time.Time
	has      bool
}

func (f *deadlineForge) FetchPRHead(ctx context.Context, _, _ string, _ int) (string, error) {
	f.deadline, f.has = ctx.Deadline()
	return "ocman/pr-1", nil
}

func TestFetchPRHeadHasDeadline(t *testing.T) {
	f := &deadlineForge{}
	if _, err := fetchPRHead(context.Background(), f, "/repo", "origin", 1); err != nil {
		t.Fatal(err)
	}
	if !f.has {
		t.Fatal("FetchPRHead context has no deadline")
	}
	if remaining := time.Until(f.deadline); remaining <= 0 || remaining > prHeadFetchTimeout {
		t.Fatalf("deadline remaining = %v, want within (0, %v]", remaining, prHeadFetchTimeout)
	}
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
