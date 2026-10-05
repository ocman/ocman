package local

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// A new worktree inherits the main checkout's untracked .opencode
// dependencies before its session exists, so OpenCode skips the npm install
// it would otherwise run (and block plugin loading on) for the new directory.
func TestWorktreeSessionSeedsOpencodeDeps(t *testing.T) {
	repo := initRepo(t)
	write := func(rel, body string) {
		p := filepath.Join(repo, ".opencode", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("opencode.json", "{}")
	write(".gitignore", "node_modules\npackage.json\npackage-lock.json\n")
	git := exec.Command("git", "-C", repo, "add", ".opencode")
	git.Env = gitexec.CleanEnv()
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	git = exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@e", "commit", "-qm", "opencode")
	git.Env = gitexec.CleanEnv()
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	write("node_modules/@opencode-ai/plugin/index.js", "x")
	write("package.json", `{"dependencies":{"@opencode-ai/plugin":"1"}}`)
	write("package-lock.json", "{}")

	h := New(Deps{
		Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
		CreateSession: func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			for _, rel := range []string{"node_modules/@opencode-ai/plugin/index.js", "package.json", "package-lock.json"} {
				if _, err := os.Stat(filepath.Join(req.Directory, ".opencode", rel)); err != nil {
					t.Errorf("%s not seeded before session create: %v", rel, err)
				}
			}
			return &platforms.CreateSessionResponse{ID: "s"}, nil
		},
	})
	if _, err := h.CreateWorktreeSession(context.Background(), hostsvc.WorktreeSessionRequest{
		ProjectDir: repo, Branch: "feature", NewBranch: true, BaseRef: "main",
	}); err != nil {
		t.Fatal(err)
	}
}

// Automatic worktrees return a provisional name at once; the model's name is
// applied afterwards to the branch and the session title, keeping the path.
func TestAutomaticWorktreeNamesAreFresh(t *testing.T) {
	for _, namingAvailable := range []bool{true, false} {
		t.Run(map[bool]string{true: "model", false: "fallback"}[namingAvailable], func(t *testing.T) {
			repo := initRepo(t)
			var mu sync.Mutex
			titles := map[string]string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !namingAvailable {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				switch {
				case r.URL.Path == "/config":
					_, _ = w.Write([]byte(`{"small_model":"test/fast"}`))
				case r.URL.Path == "/session":
					// The naming session must stay hidden from session listings.
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), ` subagent)"`) {
						t.Errorf("naming session title not hidden: %s", body)
					}
					_, _ = w.Write([]byte(`{"id":"name"}`))
				case r.URL.Path == "/session/name/message":
					// The title agent sees only the user's task, never naming instructions.
					body, _ := io.ReadAll(r.Body)
					if strings.Contains(string(body), "branch") || !strings.Contains(string(body), `"text":"Fix login"`) {
						t.Errorf("naming input is not the bare prompt: %s", body)
					}
					_, _ = w.Write([]byte(`{"parts":[{"type":"text","text":"Fix Login\n"}]}`))
				case r.Method == http.MethodPatch:
					body, _ := io.ReadAll(r.Body)
					mu.Lock()
					titles[strings.TrimPrefix(r.URL.Path, "/session/")] = string(body)
					mu.Unlock()
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			ids := []string{"one", "two"}
			h := New(Deps{
				Runtime: &fakeRuntime{endpoint: srv.URL},
				CreateSession: func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					if req.Directory == repo {
						t.Fatal("session remained in current checkout")
					}
					if req.Title != "" {
						t.Fatalf("automatic session got title %q; OpenCode should title it", req.Title)
					}
					id := ids[0]
					ids = ids[1:]
					return &platforms.CreateSessionResponse{ID: id}, nil
				},
			})
			request := hostsvc.WorktreeSessionRequest{ProjectDir: repo, AutoName: true, Prompt: "Fix login"}
			first, err := h.CreateWorktreeSession(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			second, err := h.CreateWorktreeSession(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			h.background.Wait()
			if !strings.HasPrefix(first.Branch, provisionalPrefix) || first.Branch == second.Branch || first.WorktreePath == second.WorktreePath || second.Reused {
				t.Fatalf("worktrees not isolated: %+v / %+v", first, second)
			}
			suffix := strings.TrimPrefix(first.Branch, provisionalPrefix)
			want := first.Branch
			if namingAvailable {
				want = "fix-login-" + suffix
			}
			out, err := exec.Command("git", "-C", first.WorktreePath, "branch", "--show-current").Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(out)); got != want {
				t.Fatalf("worktree branch = %q, want %q", got, want)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(titles) != 0 {
				t.Fatalf("session retitled with the branch name: %v", titles)
			}
		})
	}
}

// Every step of a worktree start is reported on the request's context, so
// the new conversation can show the instance and checkout in parallel.
func TestWorktreeSessionReportsProgress(t *testing.T) {
	repo := initRepo(t)
	var mu sync.Mutex
	got := map[string][]string{}
	ctx := hostsvc.WithProgress(context.Background(), func(step, state string) {
		mu.Lock()
		got[step] = append(got[step], state)
		mu.Unlock()
	})
	h := New(Deps{
		Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
		CreateSession: func(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "s"}, nil
		},
	})
	if _, err := h.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
		ProjectDir: repo, Branch: "feature", NewBranch: true, BaseRef: "main",
	}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{hostsvc.StepOpencode, hostsvc.StepWorktree, hostsvc.StepSession} {
		if strings.Join(got[step], ",") != "active,done" {
			t.Errorf("%s = %v, want active,done", step, got[step])
		}
	}
}
