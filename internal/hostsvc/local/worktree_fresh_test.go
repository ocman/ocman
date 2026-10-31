package local

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func TestAutomaticWorktreeFetchesBeforeSessionCreation(t *testing.T) {
	upstream := initRepo(t)
	repo := filepath.Join(t.TempDir(), "checkout")
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := gitexec.Command(t.Context(), append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(t.TempDir(), "clone", upstream, repo)
	run(upstream, "-c", "user.name=test", "-c", "user.email=test@test", "commit", "--allow-empty", "-m", "upstream change")
	want := run(upstream, "rev-parse", "HEAD")
	created := false
	h := New(Deps{
		Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
		CreateSession: func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			created = true
			if got := run(req.Directory, "rev-parse", "HEAD"); got != want {
				t.Errorf("session started at %s, want fetched commit %s", got, want)
			}
			return &platforms.CreateSessionResponse{ID: "ses_fresh"}, nil
		},
	})
	if _, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{ProjectDir: repo, AutoName: true}); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("session was not created")
	}
}
