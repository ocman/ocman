package autoarchive

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

type initialPlatform struct {
	platformtest.Fake
	live map[string]*platforms.LiveState
}

func (p *initialPlatform) LiveStatus(id string) *platforms.LiveState { return p.live[id] }

func initialRows(n int, updated int64) []statev2.Session {
	rows := make([]statev2.Session, n)
	for i := range rows {
		// Deliberately oldest first, with deterministic ties.
		rows[i] = statev2.Session{Key: statev2.Key{SessionID: fmt.Sprintf("s%03d", i)}, UpdatedAt: updated + int64(i)}
	}
	return rows
}

func TestInitialImportKeepsDayOrTwenty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		n       int
		updated int64
		want    int
	}{
		{"older backlog", 50, daysAgo(3), 30},
		{"small old history", 12, daysAgo(90), 0},
		{"busy day", 50, time.Now().Add(-time.Hour).UnixMilli(), 0},
		{"unknown dates", 50, -100, 0},
		{"empty history", 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := statetest.Open(t)
			p := &initialPlatform{Fake: platformtest.Fake{PlatformID: "opencode"}}
			m := newModule(store, nil, p)
			rows := initialRows(tc.n, tc.updated)
			if err := m.InitialImport(t.Context(), "opencode", rows); err != nil {
				t.Fatal(err)
			}
			archived, err := store.ArchivedSessions(t.Context())
			if err != nil || len(archived) != tc.want {
				t.Fatalf("archives=%v err=%v, want %d", archived, err, tc.want)
			}
			if tc.want > 0 {
				for i, s := range rows {
					_, got := archived[state.Key{Platform: "opencode", SessionID: s.SessionID}]
					if got != (i < tc.n-20) {
						t.Errorf("%s archived=%v", s.SessionID, got)
					}
				}
			}
		})
	}
}

func TestInitialImportMixedHistoryAndProtectedSessions(t *testing.T) {
	store := statetest.Open(t)
	p := &initialPlatform{Fake: platformtest.Fake{PlatformID: "claude"}, live: map[string]*platforms.LiveState{
		"s000": {Status: "busy"}, "s001": {Status: "waiting"},
		"s002": {PendingPermission: true}, "s003": {PendingQuestion: true},
		"s004": {BackgroundTaskCount: 1}, "s005": {BackgroundServiceCount: 1},
	}}
	m := newModule(store, nil, p)
	rows := initialRows(40, daysAgo(4))
	// These very recent hidden rows must not displace visible sessions.
	rows = append(rows, statev2.Session{Key: statev2.Key{SessionID: "child"}, ParentID: "s000", UpdatedAt: time.Now().UnixMilli()},
		statev2.Session{Key: statev2.Key{SessionID: "helper"}, Subagent: true, UpdatedAt: time.Now().UnixMilli()},
		statev2.Session{Key: statev2.Key{SessionID: "deleted"}, SourceDeleted: true, UpdatedAt: time.Now().UnixMilli()})
	rows[19].UpdatedAt = time.Now().Add(-time.Hour).UnixMilli()
	if err := store.PinSession(t.Context(), "claude", "s006"); err != nil {
		t.Fatal(err)
	}
	if err := store.UnarchiveSession(t.Context(), "claude", "s007"); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour).UnixMilli()
	if err := store.SnoozeSession(t.Context(), "claude", "s008", until); err != nil {
		t.Fatal(err)
	}
	if err := m.InitialImport(t.Context(), "claude", rows); err != nil {
		t.Fatal(err)
	}
	archived, err := store.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"s000", "s001", "s002", "s003", "s004", "s005", "s006", "s007", "s019", "child", "helper", "deleted", "s039"} {
		if _, ok := archived[state.Key{Platform: "claude", SessionID: id}]; ok {
			t.Errorf("protected %s archived", id)
		}
	}
	snoozed, err := store.SnoozedSessions(t.Context())
	if err != nil || snoozed[state.Key{Platform: "claude", SessionID: "s008"}] != until {
		t.Fatal("snooze replaced", err)
	}
	if _, ok := archived[state.Key{Platform: "claude", SessionID: "s009"}]; !ok {
		t.Fatal("old session not archived")
	}
	// Reconstruct the module and restore an archive. Neither a new source nor
	// a retry for the same host/platform may undo the user's restore.
	if err := store.UnarchiveSession(t.Context(), "claude", "s009"); err != nil {
		t.Fatal(err)
	}
	m = newModule(store, nil, p)
	if err := m.InitialImport(t.Context(), "claude", rows); err != nil {
		t.Fatal(err)
	}
	archived, _ = store.ArchivedSessions(t.Context())
	if _, ok := archived[state.Key{Platform: "claude", SessionID: "s009"}]; ok {
		t.Fatal("cleanup repeated")
	}
}

func TestInitialImportFailuresAndDisabled(t *testing.T) {
	store := statetest.Open(t)
	p := &initialPlatform{Fake: platformtest.Fake{PlatformID: "codex"}}
	m := newModule(store, nil, p)
	rows := initialRows(30, daysAgo(3))
	if err := m.InitialImport(t.Context(), "missing", rows); err == nil {
		t.Fatal("missing platform accepted")
	}
	if err := m.SetSettings(t.Context(), Settings{Enabled: false, TTLDays: 7}); err != nil {
		t.Fatal(err)
	}
	if err := m.InitialImport(t.Context(), "codex", rows); err != nil {
		t.Fatal(err)
	}
	if err := m.SetSettings(t.Context(), Settings{Enabled: true, TTLDays: 7}); err != nil {
		t.Fatal(err)
	}
	if err := m.InitialImport(t.Context(), "codex", rows); err != nil {
		t.Fatal(err)
	}
	archives, _ := store.ArchivedSessions(t.Context())
	if len(archives) != 0 {
		t.Fatal("disabled cleanup ran later")
	}
	if err := NewModule(Deps{}).InitialImport(t.Context(), "codex", rows); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), settingKey, "invalid"); err != nil {
		t.Fatal(err)
	}
	if err := m.InitialImport(t.Context(), "codex", rows); err == nil {
		t.Fatal("bad settings ignored")
	}
}

func TestDailyArchiveWaitsForImportAndPreservesFirstDay(t *testing.T) {
	store := statetest.Open(t)
	scanned := false
	p := &archivePlatform{id: "opencode", inactive: func(context.Context) ([]db.SessionArchiveCandidate, error) {
		scanned = true
		return nil, errors.New("should not read")
	}}
	m := newModule(store, nil, p)
	m.d.Local = func() hostsvc.Host { t.Fatal("project sweep hid initial selection"); return nil }
	m.d.SessionReady = func(string) bool { return false }
	m.ArchiveSessions(t.Context())
	m.ArchiveProjects(t.Context())
	if scanned {
		t.Fatal("scanned partial import")
	}
	m.d.SessionReady = func(string) bool { return true }
	if err := store.ArchiveInitialSessions(t.Context(), "opencode", nil); err != nil {
		t.Fatal(err)
	}
	m.ArchiveSessions(t.Context())
	m.ArchiveProjects(t.Context())
	if scanned {
		t.Fatal("daily policy undid first import")
	}
	if err := store.SetSetting(t.Context(), "initial_import_archive/opencode", "bad"); err != nil {
		t.Fatal(err)
	}
	m.ArchiveSessions(t.Context())
	if scanned {
		t.Fatal("invalid marker did not fail closed")
	}
}
