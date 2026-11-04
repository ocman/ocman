package autoarchive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// Like OpenCode, this platform supplies cached observations, not LiveStatus.
type snapshotPlatform struct {
	platformtest.Fake
	observations map[string]platforms.LiveState
	err          error
}

func (p *snapshotPlatform) SessionObservations(context.Context, []string) (map[string]platforms.LiveState, error) {
	return p.observations, p.err
}

func TestInitialImportUsesOwnerObservationSnapshot(t *testing.T) {
	store := statetest.Open(t)
	p := &snapshotPlatform{Fake: platformtest.Fake{PlatformID: "opencode"}, observations: map[string]platforms.LiveState{
		"s000": {Status: "busy"}, "s001": {PendingPermission: true}, "s002": {PendingQuestion: true},
		"s003": {BackgroundTaskCount: 1}, "s004": {BackgroundServiceCount: 1},
	}}
	m := newModule(store, nil, p)
	if err := m.InitialImport(t.Context(), "opencode", initialRows(30, daysAgo(3))); err != nil {
		t.Fatal(err)
	}
	archived, err := store.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for id := range p.observations {
		if _, ok := archived[state.Key{Platform: "opencode", SessionID: id}]; ok {
			t.Errorf("live session %s was archived", id)
		}
	}
	if len(archived) != 5 {
		t.Fatalf("archived %d, want 5", len(archived))
	}
}

func TestInitialImportWaitsForObservations(t *testing.T) {
	store := statetest.Open(t)
	p := &snapshotPlatform{Fake: platformtest.Fake{PlatformID: "opencode"}, err: errors.New("bootstrap incomplete")}
	m := newModule(store, nil, p)
	rows := initialRows(30, daysAgo(3))
	if err := m.InitialImport(t.Context(), "opencode", rows); err == nil {
		t.Fatal("incomplete observations accepted")
	}
	archived, _ := store.ArchivedSessions(t.Context())
	if len(archived) != 0 {
		t.Fatal("archived before live bootstrap")
	}
	p.err = nil
	if err := m.InitialImport(t.Context(), "opencode", rows); err != nil {
		t.Fatal(err)
	}
	archived, _ = store.ArchivedSessions(t.Context())
	if len(archived) != 10 {
		t.Fatal("failed snapshot consumed the one-time receipt")
	}
}

func TestRunRetriesReadinessDeferredMaintenance(t *testing.T) {
	store := statetest.Open(t)
	scanned := make(chan struct{}, 1)
	p := &archivePlatform{id: "opencode", inactive: func(context.Context) ([]db.SessionArchiveCandidate, error) {
		scanned <- struct{}{}
		return nil, nil
	}}
	m := newModule(store, &projectHost{}, p)
	m.readinessRetry = time.Millisecond
	calls := 0
	m.d.SessionReady = func(string) bool { calls++; return calls > 2 }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-scanned:
	case <-time.After(time.Second):
		t.Fatal("startup maintenance was deferred for a full day")
	}
}
