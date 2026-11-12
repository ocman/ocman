package gitmod

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorktreeListCancellation(t *testing.T) {
	old := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(old) })
	hook := logtest.NewGlobal()
	for _, tc := range []struct {
		name string
		err  error
		code int
	}{
		{"caller", fmt.Errorf("git rev-parse: %w", context.Canceled), 499},
		{"remote", status.Error(codes.Canceled, "caller left"), 499},
		{"deadline", fmt.Errorf("git worktree list: %w", context.DeadlineExceeded), 502},
		{"remote deadline", status.Error(codes.DeadlineExceeded, "too slow"), 502},
		{"killed without cancellation evidence", errors.New("git worktree list: signal: killed"), 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook.Reset()
			h := newHarness(module.Guards{Auth: pass})
			h.host.err = tc.err
			rr := h.serve(http.MethodGet, "/api/worktree/list?dir=/r", "")
			if rr.Code != tc.code {
				t.Errorf("status=%d want %d", rr.Code, tc.code)
			}
			warned := false
			for _, entry := range hook.AllEntries() {
				warned = warned || entry.Level <= log.WarnLevel
			}
			if warned != (tc.code == 502) {
				t.Errorf("warning=%v for %v", warned, tc.err)
			}
		})
	}
}
