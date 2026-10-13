package routines

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type sleepingHost struct {
	*testHost
	awake bool
}

func (h *sleepingHost) EnsureRuntime(ctx context.Context, platform, directory string) (*hostsvc.EnsureRuntimeResult, error) {
	h.awake = true
	return h.testHost.EnsureRuntime(ctx, platform, directory)
}

func TestScheduledRoutineWakesCulledProjectBeforeSending(t *testing.T) {
	for _, mode := range []string{SessionNew, SessionReuse, SessionExisting} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			host := &sleepingHost{testHost: h.host}
			h.svc.router = hostsvc.NewRouter(host)
			input := validInput()
			input.SessionMode = mode
			input.Schedule = Schedule{Kind: ScheduleTimeout, Timeout: time.Minute}
			if mode == SessionExisting {
				input.SessionID = "saved"
			}
			routine, err := h.svc.Create(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if mode == SessionReuse {
				if _, err := h.svc.RunNow(t.Context(), routine.ID); err != nil {
					t.Fatal(err)
				}
				h.platform.setStatus(db.StatusDone)
				if err := h.svc.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
				h.platform.sent = nil
				host.awake = false
			}
			h.platform.onSend = func() {
				if !host.awake {
					t.Error("sent before waking project")
				}
			}
			h.now.Add((2 * time.Minute).Milliseconds())
			if err := h.svc.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			created, sent := h.platform.counts()
			if !host.awake || sent != 1 {
				t.Fatalf("awake=%v sent=%d", host.awake, sent)
			}
			if mode == SessionExisting && (created != 0 || h.platform.sent[0].SessionID != "saved") {
				t.Fatal("did not reuse saved session")
			}
			if mode == SessionReuse && (created != 1 || h.platform.sent[0].SessionID != "session-1") {
				t.Fatal("did not reuse routine session")
			}
		})
	}
}
