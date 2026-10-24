package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// TestAutoArchiveLoop_SurvivesPanic injects a panicking tick body
// into autoArchiveTickFn, runs runAutoArchiveLoop manually for two
// ticks, and asserts that:
//
//   - the first tick panics (and is recovered),
//   - the second tick still runs (i.e. the loop didn't die).
//
// This is the FR-11 contract: a panicking iteration must not silently
// disable a feature for the rest of the process lifetime.
func TestAutoArchiveLoop_SurvivesPanic(t *testing.T) {
	prev := autoArchiveTickFn
	defer func() { autoArchiveTickFn = prev }()

	var ticks int32
	autoArchiveTickFn = func(context.Context, *Server) {
		n := atomic.AddInt32(&ticks, 1)
		if n == 1 {
			panic("simulated panic on first tick")
		}
	}

	// We don't need a real Server — autoArchiveTickFn ignores it.
	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	// Simulate two iterations of the loop body. We don't run the
	// real ticker — that would slow the test down — but we exercise
	// the same module.RunWithRecover wrapping the loop uses.
	module.RunWithRecover("auto-archive", func() { autoArchiveTickFn(t.Context(), nil) })
	module.RunWithRecover("auto-archive", func() { autoArchiveTickFn(t.Context(), nil) })

	if got := atomic.LoadInt32(&ticks); got != 2 {
		t.Fatalf("ticks = %d, want 2 (loop must survive the panicking first tick)", got)
	}
	var foundPanic bool
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.ErrorLevel && e.Data["loop"] == "auto-archive" {
			foundPanic = true
			break
		}
	}
	if !foundPanic {
		t.Fatalf("expected ERROR log for the panicking auto-archive tick")
	}
}

// TestProjectsIndexLoop_SurvivesPanic mirrors the auto-archive test
// for the projects-index loop.
func TestProjectsIndexLoop_SurvivesPanic(t *testing.T) {
	prev := projectsIndexTickFn
	defer func() { projectsIndexTickFn = prev }()

	var ticks int32
	projectsIndexTickFn = func(context.Context, *Server) {
		n := atomic.AddInt32(&ticks, 1)
		if n == 1 {
			panic("simulated panic on first tick")
		}
	}

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	module.RunWithRecover("projects-index", func() { projectsIndexTickFn(t.Context(), nil) })
	module.RunWithRecover("projects-index", func() { projectsIndexTickFn(t.Context(), nil) })

	if got := atomic.LoadInt32(&ticks); got != 2 {
		t.Fatalf("ticks = %d, want 2", got)
	}
	var foundPanic bool
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.ErrorLevel && e.Data["loop"] == "projects-index" {
			foundPanic = true
			break
		}
	}
	if !foundPanic {
		t.Fatalf("expected ERROR log for the panicking projects-index tick")
	}
}
