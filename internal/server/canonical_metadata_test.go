package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/convstream"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

type canonicalMetadataNative struct{ *fakePlatform }

func (*canonicalMetadataNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: "source"}, nil
}
func (p *canonicalMetadataNative) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	out := make([]db.HistoryEntry, len(p.sessions))
	for i, row := range p.sessions {
		out[i] = db.HistoryEntry{ID: row.ID, Version: "v1"}
	}
	return out, nil
}
func (p *canonicalMetadataNative) HistorySnapshot(_ context.Context, id string) (*db.HistorySnapshot, error) {
	for _, row := range p.sessions {
		if row.ID == id {
			return &db.HistorySnapshot{Session: row, Version: "v1", Messages: []db.Message{}, Parts: []db.Part{}}, nil
		}
	}
	return nil, platforms.ErrNotFound
}
func (p *canonicalMetadataNative) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	snapshot, err := p.HistorySnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	return &snapshot.Session, nil
}

// Parent, directory and descendant lookups read the ingested read model
// through the canonical adapter, never the native database or a transcript.
func TestCanonicalMetadataUsesIngestedReadModel(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	transcriptReads := 0
	native := &canonicalMetadataNative{fakePlatform: &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "root", Directory: "/repo", Title: "root title"}, {ID: "child", ParentID: "root", Directory: "/repo/sub", Title: "child title"}}}}
	native.sessionDetailFn = func(string) (*platforms.SessionDetail, error) {
		transcriptReads++
		return nil, platforms.ErrNotFound
	}
	history := statev2test.Open(t)
	adapter, err := owned.New(t.Context(), native, catalogue, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(adapter)
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", registry, nil).WithConversationHistory(history, "owner")
	ingestHistory(t, s, history, 2)
	root, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if parent, ok := s.autoapproveEngineDeps().ParentSessionID(t.Context(), child.ID); !ok || parent != root.ID {
		t.Errorf("parent lookup bypassed canonical adapter: %q %v", parent, ok)
	}
	if directory, err := s.autoApproveSessionDir(child.ID); err != nil || directory != "/repo/sub" {
		t.Errorf("directory lookup failed: %q %v", directory, err)
	}
	if !s.isDescendantSession(t.Context(), convstream.Key{Platform: "opencode", SessionID: root.ID}, child.ID) {
		t.Error("descendant lookup bypassed canonical adapter")
	}
	if transcriptReads != 0 {
		t.Errorf("metadata lookup read transcript %d times", transcriptReads)
	}
}
