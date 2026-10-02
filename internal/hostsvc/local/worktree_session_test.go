package local

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

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
					_, _ = w.Write([]byte(`{"parts":[{"type":"text","text":"fix-login"}]}`))
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
					if req.Title == "" || !strings.HasPrefix(req.Title, provisionalPrefix) {
						t.Fatalf("title not provisional: %q", req.Title)
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
			if namingAvailable && !strings.Contains(titles["one"], want) {
				t.Fatalf("title not renamed: %v", titles)
			}
			if !namingAvailable && len(titles) != 0 {
				t.Fatalf("title renamed without a model name: %v", titles)
			}
		})
	}
}
