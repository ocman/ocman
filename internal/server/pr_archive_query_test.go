package server

import (
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/testutil"
)

func TestPRWatchArchiveReadIsScoped(t *testing.T) {
	exporter := testutil.RecordSQL(t)
	s := testServer(t)
	s.registry.Register(prLifecyclePlatform{
		fakePlatform: &fakePlatform{PlatformID: "fake"},
		life:         &platforms.SessionLifecycle{Status: db.StatusDone},
	})
	exporter.Reset()
	if !s.prWatchReady(t.Context(), prworkflow.Target{Platform: "fake", SessionID: "s1", RemoteID: "local"}) {
		t.Fatal("idle session not ready")
	}
	found := false
	for _, span := range exporter.GetSpans() {
		for _, attr := range span.Attributes {
			if attr.Key == "db.query.summary" && strings.Contains(attr.Value.AsString(), "from archived_session") {
				found = true
				if !strings.Contains(attr.Value.AsString(), "where platform") {
					t.Errorf("read all archives for one watch: %s", attr.Value.AsString())
				}
			}
		}
	}
	if !found {
		t.Fatal("archive lookup not observed")
	}
}

func TestPRWatchArchiveFailureBlocksAdmission(t *testing.T) {
	s := testServer(t)
	s.registry.Register(prLifecyclePlatform{
		fakePlatform: &fakePlatform{PlatformID: "fake"},
		life:         &platforms.SessionLifecycle{Status: db.StatusDone},
	})
	if err := s.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	if s.prWatchReady(t.Context(), prworkflow.Target{Platform: "fake", SessionID: "s1", RemoteID: "local"}) {
		t.Fatal("archive read failure admitted PR notification")
	}
}
