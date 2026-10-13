package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/state"
)

// claudeTestPlatform mirrors main's claudePlatform: the generic adapter plus
// the in-process runtime ensure and the `claude -p` worktree namer.
type claudeTestPlatform struct {
	*provider.Adapter
	cli string
}

func (claudeTestPlatform) EnsureRuntime(_ context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return &hostsvc.EnsureRuntimeResult{RepoRoot: dir}, nil
}

func (p claudeTestPlatform) WorktreeName(ctx context.Context, dir, prompt string) (string, error) {
	return claudeprovider.WorktreeName(ctx, "", p.cli, dir, prompt)
}

// newClaudeServer serves the Claude provider with the fake CLI replaying
// plain.jsonl and wires it into a server whose local host is the real one.
func newClaudeServer(t *testing.T) (*Server, *provider.Adapter, string) {
	t.Helper()
	bin := claudetest.Binary(t)
	t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("plain.jsonl"))
	t.Setenv("FAKE_CLAUDE_PRINT", "Fix the login redirect\n")
	dir, err := os.MkdirTemp("", "ocl-") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	config := filepath.Join(dir, "config")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	sup, err := claudeprovider.NewSupervisor(t.Context(), claudeprovider.Options{
		IndexPath: filepath.Join(dir, "claude-index.db"), ConfigDir: config, CLI: bin, Sweep: time.Hour,
		MaxProcs: 4, IdleTimeout: time.Minute,
	}, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")
	go func() { _ = sup.Serve(t.Context(), socket) }()
	var adapter *provider.Adapter
	for deadline := time.Now().Add(5 * time.Second); adapter == nil; time.Sleep(10 * time.Millisecond) {
		if adapter, err = provider.New(t.Context(), socket); err != nil && time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	t.Cleanup(adapter.Close)
	rules, err := provider.OpenRules("")
	if err != nil {
		t.Fatal(err)
	}
	adapter.SetRules(rules)
	srv, reg := newSessionsTestServer(t)
	reg.Register(claudeTestPlatform{adapter, bin})
	srv.hostRouter = hostsvc.NewRouter(srv.newLocalHost())
	srv.watchProviderPrompts(t.Context(), new(sync.WaitGroup))
	return srv, adapter, bin
}

func claudeRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitForServerTest(t, repo)
	repo, _ = filepath.EvalSymlinks(repo)
	return repo
}

func waitClaude(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func claudeSessionDir(t *testing.T, adapter *provider.Adapter, id string) string {
	t.Helper()
	detail, err := adapter.Session(t.Context(), id, 0, 0)
	if err != nil || detail == nil || detail.Session == nil {
		t.Fatalf("session %s: %+v, %v", id, detail, err)
	}
	return detail.Session.Directory
}

func gitBranches(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "branch", "--format=%(refname:short)").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// /wt with an automatic name creates the Claude session in the new worktree,
// sends the first message there, and names the branch with `claude -p`.
func TestClaudeWorktreeCreateAndLaunch(t *testing.T) {
	srv, adapter, _ := newClaudeServer(t)
	repo := claudeRepo(t)
	body, _ := json.Marshal(map[string]any{"projectDir": repo, "autoName": true, "prompt": "fix the login redirect",
		"platform": "claudecode", "remoteId": "local", "send": map[string]any{"message": "Reply with exactly SPIKE_OK."}})
	w := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(w, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(string(body))))
	var res struct {
		SessionID, Platform, WorktreePath, Branch, FirstMessageError string
		FirstMessageSent                                             bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if res.Platform != "claudecode" || !res.FirstMessageSent || !strings.HasPrefix(res.Branch, "session-") {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.WorktreePath, filepath.Join(".worktrees", "repo")) {
		t.Fatalf("worktree path = %q", res.WorktreePath)
	}
	if dir := claudeSessionDir(t, adapter, res.SessionID); dir != res.WorktreePath {
		t.Fatalf("session directory = %q, want %q", dir, res.WorktreePath)
	}
	suffix := strings.TrimPrefix(res.Branch, "session-")
	waitClaude(t, "claude-named branch", func() bool {
		return strings.Contains(gitBranches(t, repo), "fix-the-login-redirect-"+suffix)
	})
}

// /wt from a Claude session splits onto Claude even when the default
// platform is another one.
func TestClaudeWorktreeFromParentSession(t *testing.T) {
	srv, adapter, _ := newClaudeServer(t)
	repo := claudeRepo(t)
	parent, err := adapter.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.SetSetting(t.Context(), state.DefaultPlatformSetting, "opencode"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(w, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch",
		strings.NewReader(`{"projectDir":"`+repo+`","branch":"split","newBranch":true,"baseRef":"main","parentSessionId":"`+parent.ID+`"}`)))
	var res struct{ SessionID, Platform, WorktreePath string }
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if res.Platform != "claudecode" || claudeSessionDir(t, adapter, res.SessionID) != res.WorktreePath {
		t.Fatalf("result = %+v", res)
	}
}

// MCP sessions create with platform claudecode returns a Claude session.
func TestClaudeMCPCreate(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		t.Run(fmt.Sprintf("worktree=%t", worktree), func(t *testing.T) {
			srv, adapter, _ := newClaudeServer(t)
			repo := claudeRepo(t)
			created, err := (sessionMCPService{server: srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{
				Platform: "claudecode", Directory: repo, Worktree: &worktree, Prompt: "Reply with exactly SPIKE_OK."})
			if err != nil || created.Platform != "claudecode" || created.SessionID == "" {
				t.Fatalf("created = %+v, %v", created, err)
			}
			if dir := claudeSessionDir(t, adapter, created.SessionID); dir != created.Directory || (!worktree && dir != repo) || (worktree && !strings.Contains(dir, "/.worktrees/repo/session-")) {
				t.Fatalf("session directory = %q, result = %+v", dir, created)
			}
			if worktree {
				waitClaude(t, "background worktree naming", func() bool {
					trees, err := srv.router().Local().ListWorktrees(t.Context(), repo)
					if err != nil {
						t.Fatal(err)
					}
					for _, tree := range trees {
						if tree.Path == created.Directory && !strings.HasPrefix(tree.Branch, "session-") {
							return true
						}
					}
					return false
				})
			}
		})
	}
}

// A routine targeting claudecode runs and settles once the session idles,
// in each session mode.
func TestClaudeRoutineSettlesOnIdle(t *testing.T) {
	for _, mode := range []string{routines.SessionNew, routines.SessionReuse, routines.SessionExisting} {
		t.Run(mode, func(t *testing.T) {
			srv, adapter, _ := newClaudeServer(t)
			repo := claudeRepo(t)
			svc := srv.routineService()
			input := routines.Input{Platform: "claudecode", Name: "check", Prompt: "Reply with exactly SPIKE_OK.", Directory: repo,
				SessionMode: mode, Schedule: routines.Schedule{Kind: routines.ScheduleNone}, Enabled: true}
			if mode == routines.SessionExisting {
				existing, err := adapter.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: repo})
				if err != nil {
					t.Fatal(err)
				}
				// Continuing a session runs on its platform, not the default.
				input.SessionID, input.Platform = existing.ID, ""
			}
			routine, err := svc.Create(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			run, err := svc.RunNow(t.Context(), routine.ID)
			if err != nil || run.State != routines.RunRunning || run.Platform != "claudecode" {
				t.Fatalf("run = %+v, %v", run, err)
			}
			if input.SessionID != "" && run.SessionID != input.SessionID {
				t.Fatalf("run session = %q, want %q", run.SessionID, input.SessionID)
			}
			waitClaude(t, "routine settled", func() bool {
				if err := svc.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
				run, err = srv.stateDB.GetRoutineRun(t.Context(), run.ID)
				return err == nil && run.State != routines.RunRunning
			})
			if run.State != routines.RunSuccess {
				t.Fatalf("run = %+v", run)
			}
			lifecycle, err := adapter.SessionLifecycle(t.Context(), run.SessionID)
			if err != nil || lifecycle.Status.String() != "done" {
				t.Fatalf("lifecycle = %+v, %v", lifecycle, err)
			}
		})
	}
}

// A provider runtime enables the managed-launch UI (/wt, worktrees view)
// without opencode on PATH.
func TestHostCapsProviderRuntime(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	t.Setenv("PATH", "")
	if caps := srv.hostCaps(); caps.OpencodeLaunch || caps.Worktrees {
		t.Fatalf("caps without a runtime = %+v", caps)
	}
	reg.Register(runtimeAdapter{&fakePlatform{id: "fake-provider"}})
	if !srv.hostCaps().OpencodeLaunch {
		t.Fatal("provider runtime did not enable managed launch")
	}
}
