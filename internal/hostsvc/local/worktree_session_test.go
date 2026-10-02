package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestAutomaticWorktreeNamesAreFresh(t *testing.T) {
	for _, namingAvailable := range []bool{true, false} {
		t.Run(map[bool]string{true: "model", false: "fallback"}[namingAvailable], func(t *testing.T) {
			repo := initRepo(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !namingAvailable {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				switch r.URL.Path {
				case "/config":
					_, _ = w.Write([]byte(`{"small_model":"test/fast"}`))
				case "/session":
					_, _ = w.Write([]byte(`{"id":"name"}`))
				case "/session/name/message":
					_, _ = w.Write([]byte(`{"parts":[{"type":"text","text":"fix-login"}]}`))
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			h := New(Deps{
				Runtime: &fakeRuntime{endpoint: srv.URL},
				CreateSession: func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					if req.Directory == repo {
						t.Fatal("session remained in current checkout")
					}
					return &platforms.CreateSessionResponse{ID: "new"}, nil
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
			prefix := "session-"
			if namingAvailable {
				prefix = "fix-login-"
			}
			if !strings.HasPrefix(first.Branch, prefix) || first.Branch == second.Branch || first.WorktreePath == second.WorktreePath || second.Reused {
				t.Fatalf("worktrees not isolated: %+v / %+v", first, second)
			}
		})
	}
}
