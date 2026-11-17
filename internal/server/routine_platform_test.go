package server

import (
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/routines"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestRoutineUsesNamedConnectionDefault(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "opencode", Name: "Personal", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}, DefaultKind: "opencode"}); err != nil {
		t.Fatal(err)
	}
	work, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: "Work", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault(t.Context(), work.ID); err != nil {
		t.Fatal(err)
	}
	reg := platforms.NewRegistry()
	if err := reg.RegisterConnection(platforms.ID(work.ID), &platformtest.Fake{PlatformID: "opencode"}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", reg, nil)
	s.connections, s.connectionStore = platformconnections.NewRouter(store), store
	s.hostRouter = hostsvc.NewRouter(&createPathHost{})
	for _, model := range []string{"", "anthropic/opus", work.ID + ":anthropic/opus"} {
		routine, err := s.routineService().Create(t.Context(), routines.Input{Name: "check" + model, Prompt: "inspect", Directory: "/repo", Model: model, Schedule: routines.Schedule{Kind: routines.ScheduleNone}})
		if err != nil || routine.Platform != work.ID {
			t.Fatalf("model %q selected disabled legacy platform: %+v, %v", model, routine, err)
		}
	}
	if _, err := s.routinePlatform(t.Context(), "offline", "", ""); err == nil {
		t.Fatal("disconnected owner fell back to local default")
	}
	for _, selection := range []struct{ platform, model string }{
		{"opencode", ""}, // The legacy adapter is disabled, only Work is registered.
		{"cnx_missing", ""},
		{work.ID, "codex:openai/model"},
	} {
		if _, err := s.routinePlatform(t.Context(), "local", selection.platform, selection.model); err == nil {
			t.Fatalf("invalid routine selection accepted: %+v", selection)
		}
	}
}
