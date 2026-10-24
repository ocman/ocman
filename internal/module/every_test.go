package module

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestRunWithRecoverLogsPanicAndReturns(t *testing.T) {
	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	RunWithRecover("quiet", func() {})
	if len(hook.AllEntries()) != 0 {
		t.Fatalf("happy path logged %d entries", len(hook.AllEntries()))
	}
	RunWithRecover("test-loop", func() { panic("boom") })
	e := hook.LastEntry()
	if e == nil || e.Level != logrus.ErrorLevel || e.Message != "background loop panicked, continuing on next tick" || e.Data["loop"] != "test-loop" || e.Data["panic"] != "boom" || e.Data["stack"] == "" {
		t.Fatalf("panic log = %+v", e)
	}
}

func TestEveryRunsImmediatelyAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	Every(ctx, "test", time.Hour, func(context.Context) {
		calls++
		cancel()
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want one immediate pass", calls)
	}
}

func TestEverySurvivesPanickingRuns(t *testing.T) {
	logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	ctx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int32
	Every(ctx, "test", time.Millisecond, func(context.Context) {
		if calls.Add(1) == 3 {
			cancel()
			return
		}
		panic("tick failed")
	})
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}
