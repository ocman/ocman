package worktreecleanup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func cleanupLogHook(t *testing.T) *logtest.Hook {
	t.Helper()
	logger := log.StandardLogger()
	oldHooks, oldLevel := logger.ReplaceHooks(make(log.LevelHooks)), logger.GetLevel()
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks); logger.SetLevel(oldLevel) })
	logger.SetLevel(log.InfoLevel)
	return logtest.NewGlobal()
}

func cleanupLogEntry(t *testing.T, hook *logtest.Hook, message string) *log.Entry {
	t.Helper()
	for _, entry := range hook.AllEntries() {
		if entry.Message == message {
			return entry
		}
	}
	t.Fatalf("missing INFO log %q: %+v", message, hook.AllEntries())
	return nil
}

func TestCleanupLogsOutcomesAtInfo(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, reason string
		setup                 func(*testing.T, *fixture)
	}{
		{"removed", "removed", "", func(*testing.T, *fixture) {}},
		{"already removed", "already-removed", "", func(_ *testing.T, f *fixture) { f.h.entries = nil }},
		{"not archived", "retained", "session_not_archived", func(t *testing.T, f *fixture) {
			if err := f.store.UnarchiveSession(t.Context(), "fake", "s1"); err != nil {
				t.Fatal(err)
			}
		}},
		{"session grace", "retained", "session_grace_period", func(t *testing.T, f *fixture) { f.archive(t, "s1", f.now) }},
		{"directory grace", "retained", "directory_grace_period", func(t *testing.T, f *fixture) {
			_, release, err := f.m.BeginDirectory(t.Context(), testPath)
			if err != nil {
				t.Fatal(err)
			}
			release()
		}},
		{"pending work", "retained", "session_pending_work", func(_ *testing.T, f *fixture) { f.p.SessionList[0].Status = db.StatusBusy }},
		{"list failure", "retained", "worktree_inventory_failed", func(_ *testing.T, f *fixture) { f.h.listErr = errors.New("private-error-canary") }},
		{"locked", "retained", "unsafe_worktree_identity", func(_ *testing.T, f *fixture) { f.h.entries[0].Locked = true }},
		{"owner refusal", "retained", "owner_check_failed", func(_ *testing.T, f *fixture) { f.h.removeErr = errors.New("private-error-canary") }},
		{"persistence failure", "retained", "eligibility_read_failed", func(t *testing.T, f *fixture) { execFixtureSQL(t, f, "DROP TABLE archived_session") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			hook := cleanupLogHook(t)
			if err := f.m.Sweep(t.Context()); err != nil {
				t.Fatal(err)
			}
			checkout := cleanupLogEntry(t, hook, "worktree cleanup checkout")
			if checkout.Level != log.InfoLevel || checkout.Data["outcome"] != tc.outcome || checkout.Data["reason_code"] != tc.reason || checkout.Data["path"] != testPath || checkout.Data["owner"] != "local" {
				t.Fatalf("checkout outcome: %+v", checkout)
			}
			summary := cleanupLogEntry(t, hook, "worktree cleanup sweep")
			if summary.Level != log.InfoLevel || summary.Data["status"] != "completed" || summary.Data["checked"] != int64(1) || summary.Data[tc.outcome] != int64(1) {
				t.Fatalf("sweep summary: %+v", summary)
			}
			if summary.Data["sweep_id"] == "" || summary.Data["sweep_id"] != checkout.Data["sweep_id"] || summary.Data["duration_ms"] == nil {
				t.Fatalf("uncorrelated sweep: %+v %+v", summary.Data, checkout.Data)
			}
			reasons, ok := summary.Data["retained_by_reason"].(map[string]int64)
			if !ok || (tc.reason != "" && reasons[tc.reason] != 1) {
				t.Fatalf("missing reason counts: %+v", summary.Data)
			}
			for _, entry := range hook.AllEntries() {
				text, _ := entry.String()
				if strings.Contains(text, "private-error-canary") {
					t.Fatal("INFO logs leaked raw owner errors")
				}
			}
		})
	}
}

func TestCleanupLogsUnavailableOwnersAndEmptySweeps(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		setup        func(*fixture)
	}{
		{"empty", "", func(f *fixture) { f.p.SessionList = nil }},
		{"inventory", "inventory_unavailable", func(f *fixture) { f.p.SessionsErr = errors.New("private-error-canary") }},
		{"not ready", "inventory_unavailable", func(f *fixture) { f.m.d.Ready = func(string) bool { return false } }},
		{"disconnected", "owner_disconnected", func(f *fixture) {
			f.p.PlatformID = "r-box:fake"
			f.m.d.Router = func() *hostsvc.Router { return hostsvc.NewRouter(nil) }
		}},
		{"unsupported", "cleanup_unsupported", func(f *fixture) {
			router := hostsvc.NewRouter(&legacyCleanupHost{Host: f.h})
			f.m.d.Router = func() *hostsvc.Router { return router }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			hook := cleanupLogHook(t)
			if err := f.m.Sweep(t.Context()); err != nil {
				t.Fatal(err)
			}
			summary := cleanupLogEntry(t, hook, "worktree cleanup sweep")
			if summary.Data["checked"] != int64(0) || summary.Data["targets"] != int64(0) || summary.Data["status"] != "completed" {
				t.Fatalf("empty summary: %+v", summary.Data)
			}
			if tc.reason != "" {
				owner := cleanupLogEntry(t, hook, "worktree cleanup owner unavailable")
				wantOwner := "local"
				if tc.name == "disconnected" {
					wantOwner = "box"
				}
				if owner.Data["reason_code"] != tc.reason || owner.Data["owner"] != wantOwner || owner.Data["sweep_id"] != summary.Data["sweep_id"] || summary.Data["unavailable-owners"] != int64(1) {
					t.Fatalf("owner unavailable: %+v %+v", owner.Data, summary.Data)
				}
			}
		})
	}
}

func TestCleanupLogsCancelledSweep(t *testing.T) {
	f := newFixture(t)
	hook := cleanupLogHook(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.m.Sweep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	summary := cleanupLogEntry(t, hook, "worktree cleanup sweep")
	if summary.Data["status"] != "cancelled" || summary.Data["checked"] != int64(0) {
		t.Fatalf("cancelled sweep claimed completion: %+v", summary.Data)
	}
}

func TestCleanupLogsPartialSweepCancellation(t *testing.T) {
	f := newFixture(t)
	otherPath := "/projects/.worktrees/repo/session-2"
	f.p.SessionList = append(f.p.SessionList, db.Session{ID: "s2", Directory: otherPath, TimeUpdated: 100, Status: db.StatusDone})
	f.archive(t, "s2", f.now.Add(-retention))
	other := f.h.entries[0]
	other.Path = otherPath
	f.h.entries = append(f.h.entries, other)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.h.beforeRemove = func(context.Context, hostsvc.CleanupWorktreeRequest) error { cancel(); return nil }
	hook := cleanupLogHook(t)
	if err := f.m.Sweep(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	summary := cleanupLogEntry(t, hook, "worktree cleanup sweep")
	if summary.Data["status"] != "cancelled" || summary.Data["targets"] != int64(2) || summary.Data["checked"] != int64(1) || summary.Data["removed"] != int64(1) {
		t.Fatalf("partial sweep lost completed removal: %+v", summary.Data)
	}
	checkout := cleanupLogEntry(t, hook, "worktree cleanup checkout")
	if checkout.Data["reason_code"] != "receipt_write_failed" || checkout.Data["outcome"] != "removed" {
		t.Fatalf("failed acknowledgement hid the removal: %+v", checkout.Data)
	}
}

func TestCleanupLoggingPreservesErrorIdentity(t *testing.T) {
	err := cleanupError("owner_check_failed", platforms.ErrUnsupported)
	if !errors.Is(err, platforms.ErrUnsupported) || err.Error() != platforms.ErrUnsupported.Error() {
		t.Fatalf("logging changed error semantics: %v", err)
	}
	hook := cleanupLogHook(t)
	report := newSweepReport()
	report.finish(errors.New("private-error-canary"))
	if entry := cleanupLogEntry(t, hook, "worktree cleanup sweep"); entry.Data["status"] != "failed" {
		t.Fatalf("failed summary: %+v", entry.Data)
	}
}
