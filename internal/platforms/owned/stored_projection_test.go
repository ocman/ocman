package owned

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type storedProjector struct {
	*nativePlatform
	t        *testing.T
	observed bool
}

func (p *storedProjector) EnrichStoredSessions(_ context.Context, rows []db.Session, parents map[string]string) error {
	for i := range rows {
		if rows[i].ID != "native" || rows[i].Directory != "/stored" {
			p.t.Fatalf("incorrect source-local overlay metadata: %+v", rows[i])
		}
		rows[i].Status = db.StatusWaiting
		rows[i].PendingPermission = true
		rows[i].Title = "must not overwrite stored title"
		rows[i].TotalCost = 100
	}
	p.observed = true
	return nil
}
func (p *storedProjector) ChangesFromStored(_ context.Context, detail *platforms.SessionDetail) (*platforms.SessionChanges, error) {
	if detail.Session.ID != "native" || len(detail.Messages) != 2 || detail.Parts[0].ID != "part" {
		p.t.Fatal("projector did not receive ingested content")
	}
	return &platforms.SessionChanges{SessionID: "native", Supported: true, FilesChanged: 1}, nil
}
func (p *storedProjector) InfoFromStored(_ context.Context, detail *platforms.SessionDetail) (*platforms.SessionInfo, error) {
	return &platforms.SessionInfo{SessionID: detail.Session.ID, Tokens: platforms.TokenTotals{Input: 7}}, nil
}
func (p *storedProjector) DirectoryCatalogFromStored(_ context.Context, req platforms.DirectoryCatalogRequest, recents []db.RecentModel, defaults db.SessionDefaults, _ bool) (*platforms.DirectoryCatalog, error) {
	if req.Directory != "/stored" || len(recents) != 1 || recents[0].Provider != "p" || recents[0].Model != "model" {
		p.t.Fatalf("catalogue used native history: %+v %v", req, recents)
	}
	return &platforms.DirectoryCatalog{DefaultModel: defaults.Model, Models: &platforms.SessionModelsResponse{}, Agents: []platforms.AgentCatalogEntry{{Name: "agent"}}, Commands: []platforms.SlashCommandEntry{{Name: "command"}}}, nil
}

func TestPlatformProjectionReceivesStoredFactsAndCanOnlyOverlayLiveFields(t *testing.T) {
	a, p, id := storedFixture(t)
	source := &storedProjector{nativePlatform: p, t: t}
	a.native = source
	row, err := a.SessionSummary(t.Context(), id)
	if err != nil || !source.observed || row.Title != "stored" || row.TotalCost != 0.5 || !row.PendingPermission || row.Status != db.StatusWaiting {
		t.Fatalf("overlay replaced persisted facts: %+v %v", row, err)
	}
	if changes, err := a.SessionChanges(t.Context(), id); err != nil || changes.SessionID != id || changes.FilesChanged != 1 {
		t.Fatalf("changes: %+v %v", changes, err)
	}
	if info, err := a.SessionInfo(t.Context(), id); err != nil || info.SessionID != id || info.Tokens.Input != 7 {
		t.Fatalf("info: %+v %v", info, err)
	}
	if catalog, err := a.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"}); err != nil || catalog.DefaultModel != "p/model" {
		t.Fatalf("catalogue: %+v %v", catalog, err)
	}
	if agents, err := a.AgentCatalog(t.Context(), id); err != nil || len(agents) != 1 {
		t.Fatalf("agents: %v %v", agents, err)
	}
	if commands, err := a.SlashCommands(t.Context(), id); err != nil || len(commands) != 1 {
		t.Fatalf("commands: %v %v", commands, err)
	}
	if models, err := a.SessionModels(t.Context(), id); err != nil || models == nil {
		t.Fatalf("models: %+v %v", models, err)
	}
}
