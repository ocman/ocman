package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestCullClaudePreservesRunningTurnAndWakesOnSend(t *testing.T) {
	e := newRecoveryEnv(t)
	hold := filepath.Join(e.data, "hold")
	t.Setenv("FAKE_CLAUDE_FIXTURE", turnsFixture(t, 1))
	t.Setenv("FAKE_CLAUDE_HOLD", hold)
	p, stop := e.start(t, "127.0.0.1:47311")
	defer stop()
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	if roots, err := p.HistoricalSessionRoots(t.Context()); err != nil || roots[created.ID] != e.repo {
		t.Fatalf("project supervisor did not persist identity: %v %v", roots, err)
	}
	first := e.endpoint(t)
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID, Message: "first"}); err != nil {
		t.Fatal(err)
	}
	always := func(context.Context) (bool, error) { return true, nil }
	stopped, err := p.CullIdleProject(t.Context(), e.repo, always)
	if err != nil || stopped {
		t.Fatalf("culled running Claude: stopped=%v err=%v", stopped, err)
	}
	if err := os.WriteFile(hold, nil, 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "completed turn", func() bool {
		d, err := runtimeDetail(t.Context(), p, created.ID, 0, 0)
		return err == nil && d.Session.Status == db.StatusDone
	})
	roots, err := p.ManagedIdleProjects(t.Context())
	if err != nil || len(roots) != 1 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	stopped, err = p.CullIdleProject(t.Context(), e.repo, always)
	if err != nil || !stopped {
		t.Fatalf("idle Claude retained: stopped=%v err=%v", stopped, err)
	}
	if e.endpoint(t) != "" || p.projects.owner(created.ID) != nil {
		t.Fatal("stopped supervisor still attached")
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID, Message: "wake"}); err != nil {
		t.Fatal(err)
	}
	if endpoint := e.endpoint(t); endpoint == "" || endpoint == first {
		t.Fatal("supervisor did not wake")
	}
	eventually(t, "resumed turn", func() bool {
		d, err := runtimeDetail(t.Context(), p, created.ID, 0, 0)
		return err == nil && d.Session.Status == db.StatusDone
	})
}
