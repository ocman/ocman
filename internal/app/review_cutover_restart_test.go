package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/pluginsmod"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type restartCutoverNative struct {
	appCanonicalNative
	rows []db.Session
}

func (p *restartCutoverNative) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return p.rows, nil
}
func (p *restartCutoverNative) CreateSession(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.rows = append(p.rows, db.Session{ID: "created-native", Directory: "/repo"})
	return &platforms.CreateSessionResponse{ID: "created-native"}, nil
}

func TestPostCutoverCompletionReceiptDoesNotChangeAfterRestart(t *testing.T) {
	core, err := state.Open(statetest.Path(t, server.ModuleSchemas()...), server.ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	native := &restartCutoverNative{rows: []db.Session{{ID: "native", Directory: "/repo"}}}
	prepare := func() platforms.Platform {
		registry := platforms.NewRegistry()
		if err := prepareCanonicalSessionCore(t.Context(), core, history, registry, []platforms.Integration{native}, "owner"); err != nil {
			t.Fatal(err)
		}
		p, _ := registry.Get("test")
		return p
	}
	p := prepare()
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.(platforms.SessionCreationCommitter).CommitCreation(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	wire, err := p.(platforms.PluginSessionCompatibility).PluginSessionReference(t.Context(), created.ID)
	if err != nil || wire != created.ID {
		t.Fatalf("new wire identity: %q %v", wire, err)
	}
	store := pluginsmod.NewStore(core)
	id, err := store.AppendPluginSessionCompletion(t.Context(), "plugin", "test", created.ID, wire+":message", "answer")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AckPluginSessionCompletion(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p = prepare()
		after, err := p.(platforms.PluginSessionCompatibility).PluginSessionReference(t.Context(), created.ID)
		if err != nil || after != wire {
			t.Fatalf("restart changed modern wire identity: before=%q after=%q %v", wire, after, err)
		}
		if duplicate, err := store.AppendPluginSessionCompletion(t.Context(), "plugin", "test", created.ID, after+":message", "answer"); err != nil || duplicate != 0 {
			t.Fatalf("restart replayed delivered completion: %d %v", duplicate, err)
		}
	}
}
