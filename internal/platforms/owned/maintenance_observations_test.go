package owned

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/autoarchive"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
)

func TestMaintenanceObservationsTranslateCachedPromptsAndBackground(t *testing.T) {
	a, p, id := storedFixture(t)
	a.native = &sidebarNative{nativePlatform: p, t: t, observations: map[string]platforms.LiveState{"native": {PendingQuestion: true}}}
	a.observeBackground([]byte(`{"type":"server.connected","properties":{"backgroundTasks":{"native":1}}}`), "")
	reader, ok := any(a).(platforms.SessionObservationReader)
	if !ok {
		t.Fatal("canonical adapter has no maintenance observation snapshot")
	}
	rows, err := reader.SessionObservations(t.Context(), []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if !rows[id].PendingQuestion || rows[id].BackgroundTaskCount != 1 {
		t.Fatalf("lost cached observations: %+v", rows)
	}
	if _, ok := rows["native"]; ok {
		t.Fatal("snapshot leaked native identity")
	}
}

type maintenanceOpenCode struct {
	*nativePlatform
	cache *opencode.Adapter
	ready bool
}

func (p *maintenanceOpenCode) LiveStatus(id string) *platforms.LiveState {
	return p.cache.LiveStatus(id)
}
func (p *maintenanceOpenCode) CachedSessionObservations() map[string]platforms.LiveState {
	return p.cache.CachedSessionObservations()
}
func (p *maintenanceOpenCode) SessionObservationsReady(context.Context) (bool, error) {
	if !p.ready {
		return true, platforms.ErrHistoryUnavailable
	}
	return true, nil
}

func TestInitialArchiveUsesActualOpenCodeCacheWithCanonicalIDs(t *testing.T) {
	a, native, id := storedFixture(t)
	p := &maintenanceOpenCode{nativePlatform: native, cache: opencode.New(nil, nil, nil, ocapi.Auth{})}
	a.native = p
	// Publish enough newer roots to put the old live root outside the twenty.
	for i := range 25 {
		publish(t, a, db.Session{ID: fmt.Sprintf("other-%d", i), TimeUpdated: time.Now().Add(-72 * time.Hour).UnixMilli()})
	}
	p.cache.ObserveSessionStatus("7777", 0, "native", "busy")
	p.cache.ObservePromptAskedFromPort("7777", 0, "/repo", "question", platforms.LivePrompt{"sessionID": "native", "id": "q"})
	if p.LiveStatus("native") != nil {
		t.Fatal("test did not exercise OpenCode's nil LiveStatus")
	}
	path, err := a.store.FilePath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	core, err := state.Open(path, sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	rows, err := a.history.Sessions(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	m := autoarchive.NewModule(autoarchive.Deps{Store: core, Platforms: func() []platforms.Platform { return []platforms.Platform{a} }})
	if err := m.InitialImport(t.Context(), "test", rows); err == nil {
		t.Fatal("unseeded native cache accepted")
	}
	p.ready = true
	if err := m.InitialImport(t.Context(), "test", rows); err == nil {
		t.Fatal("unseeded background cache accepted")
	}
	a.observeBackground([]byte(`{"backgroundTasks":{"native":1}}`), "server.connected")
	if err := m.InitialImport(t.Context(), "test", rows); err != nil {
		t.Fatal(err)
	}
	archived, err := core.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[state.Key{Platform: "test", SessionID: id}]; ok {
		t.Fatal("old live OpenCode root archived")
	}
	if len(archived) != 5 {
		t.Fatalf("archived %d, want 5", len(archived))
	}
}
