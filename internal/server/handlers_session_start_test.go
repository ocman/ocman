package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func startTestServer(t *testing.T, ensure *ensureHost) (*Server, *platforms.Registry) {
	t.Helper()
	srv, reg := newSessionsTestServer(t)
	if ensure == nil {
		ensure = &ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
			return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:7788", RepoRoot: req.ProjectDir}, nil
		}}
	}
	srv.hostRouter = hostsvc.NewRouter(ensure)
	return srv, reg
}

func TestPrepareSessionEnsuresAndReturnsCatalog(t *testing.T) {
	var ensured string
	srv, reg := startTestServer(t, &ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:7788", RepoRoot: req.ProjectDir}, nil
	}})
	var asked platforms.DirectoryCatalogRequest
	reg.Register(&fakePlatform{id: "opencode", directoryCatalogFn: func(req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		asked = req
		return &platforms.DirectoryCatalog{
			Agents:       []platforms.AgentCatalogEntry{{Name: "build"}},
			DefaultAgent: "plan", DefaultModel: "anthropic/claude", LiveConnection: true,
		}, nil
	}})
	// A worktree path prepares on the project's single instance.
	w := httptest.NewRecorder()
	srv.handlePrepareSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/prepare",
		strings.NewReader(`{"platform":"opencode","directory":"/src/.worktrees/repo/feat"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	// The catalog is read from the instance the ensure just reported.
	if ensured != "/src/repo" || asked.Directory != "/src/.worktrees/repo/feat" || asked.Port != "7788" {
		t.Fatalf("ensured %q, catalog for %+v", ensured, asked)
	}
	var resp struct {
		Platform     string                          `json:"platform"`
		Agents       []platforms.AgentCatalogEntry   `json:"agents"`
		Commands     []platforms.SlashCommandEntry   `json:"commands"`
		Models       platforms.SessionModelsResponse `json:"models"`
		DefaultAgent string                          `json:"defaultAgent"`
		Live         bool                            `json:"liveConnection"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// nil catalogs are normalized to empty arrays for the client.
	if resp.Platform != "opencode" || len(resp.Agents) != 1 || resp.Commands == nil || resp.Models.Models == nil || resp.DefaultAgent != "plan" || !resp.Live {
		t.Fatalf("response = %s", w.Body.String())
	}
}

func TestPrepareSessionRejectsBadInput(t *testing.T) {
	srv, reg := startTestServer(t, nil)
	reg.Register(&fakePlatform{id: "opencode"})
	for name, body := range map[string]string{
		"relative":         `{"platform":"opencode","directory":"repo"}`,
		"missing":          `{"platform":"opencode"}`,
		"unknown platform": `{"platform":"nope","directory":"/repo"}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.handlePrepareSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/prepare", strings.NewReader(body)))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// A current-checkout start creates in the directory on the ensured port
// and delivers the first prompt server-side; a send failure is reported,
// never fatal.
func TestStartSessionCurrentCheckoutSendsFirstMessage(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "failed"}[fail], func(t *testing.T) {
			srv, reg := startTestServer(t, nil)
			var created platforms.CreateSessionRequest
			var sent platforms.SendMessageRequest
			reg.Register(&fakePlatform{
				id:       "opencode",
				sessions: []db.Session{mkSession("opencode", "s1", "t", 1)},
				createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					created = req
					return &platforms.CreateSessionResponse{ID: "s1"}, nil
				},
				sendMessageFn: func(req platforms.SendMessageRequest) error {
					sent = req
					if fail {
						return errors.New("boom")
					}
					return nil
				},
			})
			// The platform is auto-picked when omitted, exactly like Create.
			body := `{"directory":"/repo","title":"hello","prompt":"Fix login","send":{"message":"Fix login","agent":"build","model":"m","images":[{"url":"data:x","mime":"image/png"}]}}`
			w := httptest.NewRecorder()
			srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if created.Directory != "/repo" || created.Port != "7788" || created.Title != "hello" {
				t.Fatalf("create = %+v", created)
			}
			if sent.SessionID != "s1" || sent.Message != "Fix login" || sent.Agent != "build" || sent.Model != "m" || len(sent.Images) != 1 {
				t.Fatalf("send = %+v", sent)
			}
			var resp struct {
				SessionID string `json:"sessionId"`
				Platform  string `json:"platform"`
				RemoteID  string `json:"remoteId"`
				Directory string `json:"directory"`
				Sent      bool   `json:"firstMessageSent"`
				Err       string `json:"firstMessageError"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.SessionID != "s1" || resp.Platform != "opencode" || resp.RemoteID != "local" || resp.Directory != "/repo" {
				t.Fatalf("response = %+v", resp)
			}
			if resp.Sent == fail || (resp.Err != "") != fail {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
}

// A worktree start runs on the owner named by the compound platform id,
// names the worktree from the prompt, and sends to the child.
func TestStartSessionWorktreeRoutesToOwner(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	owner := &autoWorktreeOwner{}
	srv.hostRouter = hostsvc.NewRouter(nil)
	srv.hostRouter.RegisterRemote("machine", owner)
	var sent platforms.SendMessageRequest
	reg.Register(&fakePlatform{
		id:            "r-machine:opencode",
		sessions:      []db.Session{mkSession("r-machine:opencode", "child", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil },
	})
	body := `{"platform":"r-machine:opencode","directory":"/remote/repo","worktree":true,"prompt":"/review main","send":{"message":"/review main"}}`
	w := httptest.NewRecorder()
	srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !owner.request.AutoName || owner.request.Prompt != "/review main" || owner.request.ProjectDir != "/remote/repo" {
		t.Fatalf("owner request = %+v", owner.request)
	}
	if sent.SessionID != "child" {
		t.Fatalf("send = %+v", sent)
	}
	var resp struct {
		SessionID string `json:"sessionId"`
		Platform  string `json:"platform"`
		RemoteID  string `json:"remoteId"`
		Directory string `json:"directory"`
		Branch    string `json:"branch"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SessionID != "child" || resp.Platform != "r-machine:opencode" || resp.RemoteID != "machine" || resp.Directory != "/remote/worktree" || resp.Branch != "fix-login-1234" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestStartSessionWorktreeFailsClosedForDisconnectedOwner(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	srv.hostRouter = hostsvc.NewRouter(nil)
	w := httptest.NewRecorder()
	srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start",
		strings.NewReader(`{"platform":"r-gone:opencode","directory":"/remote/repo","worktree":true}`)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestStartSessionWorktreeRejectsUnknownPlatformBeforeCreation(t *testing.T) {
	srv, _ := startTestServer(t, nil)
	w := httptest.NewRecorder()
	srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start",
		strings.NewReader(`{"platform":"unknown","directory":"/repo","worktree":true}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestSessionLaunchRoutesRejectUntrustedClients(t *testing.T) {
	for _, path := range []string{"/api/sessions/prepare", "/api/sessions/start"} {
		for _, peer := range []string{"192.0.2.1:1234", "127.0.0.1:1234"} {
			t.Run(path+peer, func(t *testing.T) {
				calls := 0
				srv, reg := startTestServer(t, &ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
					calls++
					return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:7788", RepoRoot: req.ProjectDir}, nil
				}})
				reg.Register(&fakePlatform{id: "opencode"})
				mux, err := srv.routes()
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"directory":"/repo","platform":"opencode"}`))
				r.RemoteAddr = peer
				if peer == "127.0.0.1:1234" {
					r.Header.Set("Origin", "https://evil.example")
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden || calls != 0 {
					t.Fatalf("status=%d host calls=%d: %s", w.Code, calls, w.Body.String())
				}
			})
		}
	}
}

func TestLocalSessionLaunchWithConnectedRemote(t *testing.T) {
	for _, prepare := range []bool{true, false} {
		t.Run(map[bool]string{true: "prepare", false: "start"}[prepare], func(t *testing.T) {
			srv, reg := startTestServer(t, nil)
			reg.Register(&fakePlatform{id: "opencode", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
				return &platforms.CreateSessionResponse{ID: "local-created"}, nil
			}})
			reg.Register(&fakePlatform{id: "r-box:opencode", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
				t.Fatal("local draft must never create on the remote")
				return nil, nil
			}})
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"directory":"/repo"}`))
			if prepare {
				srv.handlePrepareSession(w, r)
			} else {
				srv.handleStartSession(w, r)
			}
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"platform":"opencode"`) {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
