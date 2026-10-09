package autoapprove

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func captureSessionRefreshLogs(t *testing.T) *logtest.Hook {
	t.Helper()
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	oldLevel := logger.GetLevel()
	logger.SetLevel(log.DebugLevel)
	t.Cleanup(func() {
		logger.ReplaceHooks(oldHooks)
		logger.SetLevel(oldLevel)
	})
	return logtest.NewGlobal()
}

// ocman creates and deletes short-lived helper sessions (the auto-approve
// judge, worktree naming) whose rows GetSessionSummary reports as
// db.ErrSessionNotFound — hidden "… subagent)" rows or deleted rows. Those
// must finalize the first sighting rather than be retried as failures:
// retrying busts the sessions snapshot on every event and makes the next
// list read pay for a full scan.

func TestHandleSessionChangedFinalizesSessionWithNoListRow(t *testing.T) {
	hook := captureSessionRefreshLogs(t)
	refreshCalls := make(chan string, 4)
	release := make(chan struct{})
	broadcasts := make(chan string, 4)
	invalidations := make(chan struct{}, 4)
	svc := &Service{}
	svc.deps.RefreshSession = func(_ context.Context, sessionID string) error {
		refreshCalls <- sessionID
		<-release
		// Wrapped on purpose: the watcher must match through wrapping.
		return fmt.Errorf("refresh %s: %w", sessionID, db.ErrSessionNotFound)
	}
	svc.deps.BroadcastSessionChanged = func(sessionID string) { broadcasts <- sessionID }

	w := newAutoApproveWatcher(svc)
	w.markSessionDirty = func(string) {}
	w.invalidateSessionsCache = func() { invalidations <- struct{}{} }

	w.handleSessionChanged(t.Context(), "ses-sub")
	<-refreshCalls
	close(release)

	select {
	case <-invalidations:
		t.Fatal("a session with no list row invalidated the sessions cache")
	case <-time.After(100 * time.Millisecond):
	}

	w.seenMu.Lock()
	_, seen := w.seenSessions["ses-sub"]
	w.seenMu.Unlock()
	if !seen {
		t.Fatal("a session with no list row was forgotten, so later events repeat the refresh")
	}

	w.handleSessionChanged(t.Context(), "ses-sub")
	select {
	case <-refreshCalls:
		t.Fatal("a session with no list row was refreshed again on a later event")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case id := <-broadcasts:
		t.Fatalf("session %q with no list row was broadcast", id)
	default:
	}
	if entries := hook.AllEntries(); len(entries) != 0 {
		t.Fatalf("expected missing list row to stay silent, got %v", entries)
	}
}

func TestHandleSessionChangedStillInvalidatesOnGenuineError(t *testing.T) {
	hook := captureSessionRefreshLogs(t)
	refreshCalls := make(chan string, 4)
	broadcasts := make(chan string, 4)
	invalidations := make(chan struct{}, 4)
	svc := &Service{}
	svc.deps.RefreshSession = func(_ context.Context, sessionID string) error {
		refreshCalls <- sessionID
		return errors.New("db is busy")
	}
	svc.deps.BroadcastSessionChanged = func(sessionID string) { broadcasts <- sessionID }

	w := newAutoApproveWatcher(svc)
	w.markSessionDirty = func(string) {}
	w.invalidateSessionsCache = func() { invalidations <- struct{}{} }

	w.handleSessionChanged(t.Context(), "ses-1")
	<-refreshCalls
	select {
	case <-invalidations:
	case <-time.After(time.Second):
		t.Fatal("a genuine refresh error did not invalidate the sessions cache")
	}
	select {
	case id := <-broadcasts:
		if id != "ses-1" {
			t.Fatalf("broadcast session = %q, want ses-1", id)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the fallback broadcast")
	}
	if entry := hook.LastEntry(); entry == nil || entry.Level != log.WarnLevel || entry.Message != "failed to refresh new session" {
		t.Fatalf("genuine refresh error lost its warning: %v", entry)
	}

	w.handleSessionChanged(t.Context(), "ses-1")
	select {
	case <-refreshCalls:
	case <-time.After(time.Second):
		t.Fatal("a genuine refresh error was not retried on the next event")
	}
	select {
	case <-broadcasts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the retry broadcast")
	}
}

func TestHandleSessionTitleFinalizesSessionWithNoListRow(t *testing.T) {
	hook := captureSessionRefreshLogs(t)
	published := make(chan string, 4)
	svc := &Service{}
	svc.deps.RefreshSession = func(context.Context, string) error {
		return fmt.Errorf("refresh: %w", db.ErrSessionNotFound)
	}
	svc.deps.BroadcastSessionTitle = func(sessionID, title string) { published <- sessionID + "=" + title }
	w := newAutoApproveWatcher(svc)

	w.handleSessionTitle(t.Context(), "ses-sub", "(auto-approve subagent)")
	select {
	case got := <-published:
		t.Fatalf("title %q of a session with no list row was broadcast", got)
	case <-time.After(100 * time.Millisecond):
	}
	if entries := hook.AllEntries(); len(entries) != 0 {
		t.Fatalf("expected missing list row to stay silent, got %v", entries)
	}
}

func TestHandleSessionTitleStillBroadcastsOnGenuineError(t *testing.T) {
	hook := captureSessionRefreshLogs(t)
	published := make(chan string, 4)
	svc := &Service{}
	svc.deps.RefreshSession = func(context.Context, string) error { return errors.New("db is busy") }
	svc.deps.BroadcastSessionTitle = func(sessionID, title string) { published <- sessionID + "=" + title }
	w := newAutoApproveWatcher(svc)

	w.handleSessionTitle(t.Context(), "ses-1", "Renamed")
	select {
	case got := <-published:
		if got != "ses-1=Renamed" {
			t.Fatalf("broadcast = %q, want ses-1=Renamed", got)
		}
	case <-time.After(time.Second):
		t.Fatal("a genuine refresh error suppressed the title broadcast")
	}
	if entry := hook.LastEntry(); entry == nil || entry.Level != log.WarnLevel || entry.Message != "failed to refresh renamed session" {
		t.Fatalf("genuine refresh error lost its warning: %v", entry)
	}
}
