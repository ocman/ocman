package server

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestArchivedBackgroundWorkDoesNotResurface(t *testing.T) {
	for _, path := range []string{"list", "notify", "read-only"} {
		t.Run(path, func(t *testing.T) {
			srv := &Server{stateDB: openTestStateDB(t), registry: platforms.NewRegistry()}
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
