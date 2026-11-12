package server

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func repairCatalog(kind, model string) *platformtest.Fake {
	return &platformtest.Fake{PlatformID: kind, DirectoryCatalogFn: func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		return &platforms.DirectoryCatalog{Models: &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{{Provider: "p", Model: model, IsAvailable: true}}}}, nil
	}}
}

func TestRepairLegacyModelConfigurations(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	reg := platforms.NewRegistry()
	for _, entry := range []struct{ key, kind, model string }{
		{"r-other:cnx_remote", "opencode", "match"},
		{"cnx_wrong_kind", "claudecode", "match"},
		{"cnx_missing_model", "opencode", "different"},
		{"cnx_first", "opencode", "match"},
		{"cnx_second", "opencode", "match"},
	} {
		if err := reg.RegisterConnection(platforms.ID(entry.key), repairCatalog(entry.kind, entry.model)); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []state.Routine{
		{ID: "repair", Name: "Repair", Platform: "opencode", Model: "p/match"},
		{ID: "keep", Name: "Named", Platform: "cnx_removed", Model: "p/match"},
		{ID: "bound", Name: "Existing", Platform: "opencode", Model: "p/match", SessionID: "existing"},
		{ID: "offline", Name: "Offline owner", RemoteID: "offline", Platform: "opencode", Model: "p/match"},
		{ID: "unknown", Name: "Unknown model", Platform: "opencode", Model: "p/unknown"},
	} {
		r.SessionMode, r.ScheduleKind, r.PermissionRulesJSON, r.ScheduleConfigJSON = "new", "none", "[]", "{}"
		if err := core.CreateRoutine(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.SetSetting(t.Context(), `project-defaults:["local","/repo"]`, `{"model":"p/match","agent":"review","extra":true}`); err != nil {
		t.Fatal(err)
	}
	if err := core.SetSetting(t.Context(), "project:/repo", `{"models":["p/match","p/unknown"],"off":true}`); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", reg, nil)
	if err := s.repairModelConfigurations(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := core.GetRoutine(t.Context(), "repair")
	if err != nil || got.Platform != "cnx_first" || got.Model != "cnx_first:p/match" {
		t.Fatalf("repair = %+v, %v", got, err)
	}
	for _, id := range []string{"keep", "bound", "offline", "unknown"} {
		r, err := core.GetRoutine(t.Context(), id)
		if err != nil || r.Model != map[string]string{"unknown": "p/unknown", "keep": "p/match", "bound": "p/match", "offline": "p/match"}[id] {
			t.Fatalf("changed %s: %+v, %v", id, r, err)
		}
	}
	if err := s.repairModelConfigurations(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, _ := core.GetRoutine(t.Context(), "repair")
	if again.UpdatedAt != got.UpdatedAt {
		t.Fatal("repair is not idempotent")
	}
	settings, _ := core.GetProjectSettings(t.Context(), "/repo")
	if len(settings.Models) != 2 || settings.Models[0] != "cnx_first:p/match" || settings.Models[1] != "p/unknown" || !settings.Off {
		t.Fatalf("project settings = %+v", settings)
	}
}

func TestRepairKeepsWorkingLegacyPlatform(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	reg := platforms.NewRegistry()
	reg.Register(repairCatalog("opencode", "match"))
	if err := reg.RegisterConnection("cnx_work", repairCatalog("opencode", "match")); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", reg, nil)
	ref, key := s.repairSavedModel(t.Context(), "local", "/repo", "opencode", "p/match", map[string]*platforms.DirectoryCatalog{})
	if ref != "p/match" || key != "" {
		t.Fatalf("changed working legacy configuration: %s %s", ref, key)
	}
}
