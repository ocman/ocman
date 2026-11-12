package server

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestArchivedBackgroundWorkDoesNotResurface(t *testing.T) {
	for _, path := range []string{"list", "notify", "read-only"} {
		t.Run(path, func(t *testing.T) {
			srv := &Server{stateDB: statetest.Open(t, ModuleSchemas()...), registry: platforms.NewRegistry()}
			if err := srv.stateDB.ArchiveSession(t.Context(), "fake", "s1", 100); err != nil {
				t.Fatal(err)
			}
			var row db.Session
			if err := json.Unmarshal([]byte(`{"id":"s1","platform":"fake","timeUpdated":101,"status":"done","backgroundTaskCount":1}`), &row); err != nil {
				t.Fatal(err)
			}
			apply := srv.applySessionState
			switch path {
			case "notify":
				apply = srv.applyNotifySessionState
			case "read-only":
				apply = srv.applySessionStateReadOnly
			}
			rows := []db.Session{row}
			if err := apply(t.Context(), rows); err != nil {
				t.Fatal(err)
			}
			if !rows[0].Archived {
				t.Fatal("foreground completion resurfaced a session with unfinished background work")
			}
		})
	}
}

func TestArchivedSetupDoesNotResurfaceBeforeFirstDelivery(t *testing.T) {
	for _, path := range []string{"list", "notify", "read-only"} {
		t.Run(path, func(t *testing.T) {
			srv := &Server{stateDB: statetest.Open(t, ModuleSchemas()...), registry: platforms.NewRegistry()}
			if _, err := srv.stateDB.SQL().Exec(`INSERT INTO conversation_draft(id,metadata,phase,created_at) VALUES('s1','{}','delivering',1)`); err != nil {
				t.Fatal(err)
			}
			if err := srv.stateDB.ArchiveSession(t.Context(), "fake", "s1", 100); err != nil {
				t.Fatal(err)
			}
			apply := srv.applySessionState
			if path == "notify" {
				apply = srv.applyNotifySessionState
			}
			if path == "read-only" {
				apply = srv.applySessionStateReadOnly
			}
			rows := []db.Session{{ID: "s1", Platform: "fake", TimeUpdated: 101, Status: db.StatusDone}}
			if err := apply(t.Context(), rows); err != nil {
				t.Fatal(err)
			}
			if !rows[0].Archived {
				t.Fatal("native publication resurfaced setup before first delivery")
			}
			for _, prompt := range []db.Session{
				{ID: "s1", TimeUpdated: 101, PendingPermission: true},
				{ID: "s1", TimeUpdated: 101, PendingQuestion: true},
			} {
				if !srv.shouldResurfaceSession(t.Context(), prompt, 100, "halt") {
					t.Fatal("setup hid a pending prompt")
				}
			}
			if _, err := srv.stateDB.SQL().Exec(`UPDATE conversation_draft SET phase='ready' WHERE id='s1'`); err != nil {
				t.Fatal(err)
			}
			rows[0].Archived = false
			if err := apply(t.Context(), rows); err != nil {
				t.Fatal(err)
			}
			if rows[0].Archived {
				t.Fatal("completed conversation did not resurface")
			}
		})
	}
}
