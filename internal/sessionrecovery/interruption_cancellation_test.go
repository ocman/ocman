package sessionrecovery

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestInterruptionEnrichmentCancellationLogging(t *testing.T) {
	s := New(Deps{State: statetest.Open(t)})
	old := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(old) })
	hook := logtest.NewGlobal()
	for _, deadline := range []bool{false, true} {
		hook.Reset()
		ctx, cancel := context.WithCancel(t.Context())
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		} else {
			cancel()
		}
		s.EnrichInterruptionHistory(ctx, "opencode", "s", &platforms.SessionDetail{})
		cancel()
		warned := false
		for _, entry := range hook.AllEntries() {
			warned = warned || entry.Level <= log.WarnLevel
		}
		if warned != deadline {
			t.Errorf("deadline=%v warning=%v", deadline, warned)
		}
	}
}
