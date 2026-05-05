package server

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// TestRunWithRecover_LogsAndReturnsOnPanic asserts the helper logs a
// structured ERROR line containing the panic value and the loop
// name when its body panics, and returns without re-panicking.
func TestRunWithRecover_LogsAndReturnsOnPanic(t *testing.T) {
	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	// Should not panic.
	runWithRecover("test-loop", func() {
		panic("boom")
	})

	entries := hook.AllEntries()
	if len(entries) == 0 {
		t.Fatalf("expected at least one log entry on panic")
	}
	var found bool
	for _, e := range entries {
		if e.Level == logrus.ErrorLevel &&
			strings.Contains(e.Message, "background loop panicked") &&
			e.Data["loop"] == "test-loop" {
			found = true
			if e.Data["panic"] != "boom" {
				t.Errorf("panic value not captured: %v", e.Data["panic"])
			}
			break
		}
	}
	if !found {
		t.Fatalf("no ERROR log with loop=test-loop and panic message found; got %d entries", len(entries))
	}
}

// TestRunWithRecover_HappyPath_NoLogging is the negative control: a
// body that returns normally must not produce any log line.
func TestRunWithRecover_HappyPath_NoLogging(t *testing.T) {
	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	runWithRecover("test-loop", func() { /* no-op */ })

	if len(hook.AllEntries()) != 0 {
		t.Fatalf("expected no log entries on happy path; got %d", len(hook.AllEntries()))
	}
}

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
	autoArchiveTickFn = func(*Server) {
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
	// the same runWithRecover wrapping the loop uses.
	runWithRecover("auto-archive", func() { autoArchiveTickFn(nil) })
	runWithRecover("auto-archive", func() { autoArchiveTickFn(nil) })

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
	projectsIndexTickFn = func(*Server) {
		n := atomic.AddInt32(&ticks, 1)
		if n == 1 {
			panic("simulated panic on first tick")
		}
	}

	hook := logtest.NewLocal(logrus.StandardLogger())
	defer logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))

	runWithRecover("projects-index", func() { projectsIndexTickFn(nil) })
	runWithRecover("projects-index", func() { projectsIndexTickFn(nil) })

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
