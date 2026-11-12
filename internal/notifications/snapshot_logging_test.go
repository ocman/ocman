package notifications

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type snapshotObserver struct {
	mu     sync.Mutex
	err    error
	failed bool
}

func (o *snapshotObserver) Report(module.Progress) error { return nil }
func (o *snapshotObserver) Fail(err error)               { o.err = err }
func (o *snapshotObserver) ObserveSchedule(ctx context.Context, s module.Schedule, work func(context.Context) error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.err = nil
	err := work(module.WithProgressReporter(ctx, o))
	err = errors.Join(err, o.err)
	if s.Name == "web-push" && err != nil {
		o.failed = true
	}
	return err
}

func TestSnapshotFailureLogsOnceAndRetainsScheduledFailure(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(map[bool]string{false: "without observer", true: "with observer"}[observed], func(t *testing.T) {
			m := fixture(t)
			if err := m.store.save(t.Context(), subscription(t)); err != nil {
				t.Fatal(err)
			}
			oldHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
			hook := logtest.NewLocal(log.StandardLogger())
			defer log.StandardLogger().ReplaceHooks(oldHooks)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			observer := &snapshotObserver{}
			if observed {
				ctx = module.WithScheduleObserver(ctx, observer)
			}
			calls := 0
			m.d.Snapshot = func(context.Context) ([]Candidate, error) {
				calls++
				if calls == 1 {
					return nil, errors.New("snapshot failed")
				}
				cancel()
				return nil, nil
			}
			m.run(ctx, time.Millisecond)
			warnings := 0
			for _, entry := range hook.AllEntries() {
				if entry.Level == log.WarnLevel {
					warnings++
				}
			}
			if warnings != 1 {
				t.Errorf("one failed snapshot logged %d warnings, want 1", warnings)
			}
			if observed && !observer.failed {
				t.Error("scheduled failure receipt was lost")
			}
		})
	}
}
