package server

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestRuntimeReplacementPersistsOnlyCanonicalRecoveryCandidates(t *testing.T) {
	s, raw := newInterruptionRawServer(t)
	if _, err := raw.Exec(`INSERT INTO session(id,project_id,title,directory,time_created,time_updated) VALUES('runtime-native','project','Runtime','/repo',1,1)`); err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	native := opencode.New(s.db, s.stateDB, nil, ocapi.Auth{})
	canonical, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	s.registry = platforms.NewRegistry()
	s.registry.Register(canonical)
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	if err := s.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.stateDB.PreparedSessionInterruptions(t.Context(), "opencode", "/repo")
	if err != nil || len(baseline) != 1 {
		t.Fatalf("runtime baseline: %+v %v", baseline, err)
	}
	for id := range baseline {
		if !identity.IsSessionID(id) {
			t.Fatalf("runtime replacement persisted native key: %q", id)
		}
	}
}

func TestRuntimeRecoveryAdoptsUncataloguedHiddenOpenCodeHelper(t *testing.T) {
	s, raw := newInterruptionRawServer(t)
	if _, err := raw.Exec(`INSERT INTO session(id,project_id,title,directory,time_created,time_updated) VALUES('hidden-runtime','project','(title subagent)','/repo',1,1)`); err != nil {
		t.Fatal(err)
	}
	catalogue, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	native := opencode.New(s.db, s.stateDB, nil, ocapi.Auth{})
	canonical, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := canonical.GetSessionIdentities(t.Context())
	if err != nil || len(rows) != 1 || !identity.IsSessionID(rows[0].ID) {
		t.Fatalf("hidden runtime helper blocked inventory: %+v %v", rows, err)
	}
	bound, err := catalogue.Get(t.Context(), rows[0].ID)
	if err != nil || bound.Title != "(title subagent)" || bound.NativeID != "hidden-runtime" {
		t.Fatalf("hidden metadata was lost: %+v %v", bound, err)
	}
}
