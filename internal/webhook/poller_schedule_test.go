package webhook

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type webhookScheduleFunc func(context.Context, module.Schedule, func(context.Context) error) error

func (f webhookScheduleFunc) ObserveSchedule(ctx context.Context, spec module.Schedule, work func(context.Context) error) error {
	return f(ctx, spec, work)
}

func TestPollerLogsObservationFailureAndRetries(t *testing.T) {
	f := newPollingFixture(t)
	f.deliver()
	previous := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(previous) })
	hook := logtest.NewGlobal()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		observer := webhookScheduleFunc(func(ctx context.Context, _ module.Schedule, work func(context.Context) error) error {
			calls++
			if calls == 1 {
				return errors.New("SECRET observation failure")
			}
			err := work(ctx)
			cancel()
			return err
		})
		f.poller.Run(module.WithScheduleObserver(ctx, observer))
		if calls != 2 || f.pending || f.acks != 1 {
			t.Fatalf("poller did not recover: observations=%d pending=%v acks=%d", calls, f.pending, f.acks)
		}
	})
	entries := hook.AllEntries()
	if len(entries) != 1 || entries[0].Message != "webhook inbox polling iteration failed" || entries[0].Data["job"] != "webhook-poll.inbox" {
		t.Fatalf("missing sanitized observation warning: %+v", entries)
	}
	text, err := entries[0].String()
	if err != nil || strings.Contains(text, "SECRET") {
		t.Fatalf("warning leaked observation input: %q %v", text, err)
	}
}
