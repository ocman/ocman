package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
)

func seedPRWatch(t *testing.T, s *Server, platform, id string, watching bool) {
	t.Helper()
	payload, err := json.Marshal(prworkflow.Link{Platform: platform, SessionID: id, Watching: watching})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.stateDB.SQL().ExecContext(t.Context(), `INSERT OR REPLACE INTO session_pull_request VALUES(?,?,?,?,?,?)`, platform, id, "github.com", "team/repo", 42, string(payload))
	if err != nil {
		t.Fatal(err)
	}
}

func TestPRWatchCountsAsUnfinishedBackgroundWork(t *testing.T) {
	for _, path := range []string{"list", "read-only", "notify", "sidebar", "remote-sidebar"} {
		t.Run(path, func(t *testing.T) {
			s, reg := newSessionsTestServer(t)
			platform := "fake"
			if path == "remote-sidebar" {
				platform = "r-box:fake"
			}
			seedPRWatch(t, s, platform, "s1", true)
			seedPRWatch(t, s, "other", "s1", true)
			if err := s.stateDB.ArchiveSession(t.Context(), platform, "s1", 100); err != nil {
				t.Fatal(err)
			}
			for _, watching := range []bool{true, false} {
				seedPRWatch(t, s, platform, "s1", watching)
				rows := []db.Session{{ID: "s1", Platform: platform, Status: db.StatusDone, TimeUpdated: 101, BackgroundTaskCount: 2, BackgroundServiceCount: 2}}
				var err error
				switch path {
				case "sidebar", "remote-sidebar":
					if path == "sidebar" {
						reg.Register(&compactTestPlatform{fakePlatform: &fakePlatform{id: platform}, rows: rows})
					} else {
						reg.Register(&remoteCompactTestPlatform{fakePlatform: &fakePlatform{id: platform}, rows: rows})
					}
					rows, err = s.sidebarSessionRows(t.Context(), platforms.SidebarQuery{}, false)
				default:
					apply := map[string]func(context.Context, []db.Session) error{"list": s.applySessionState, "read-only": s.applySessionStateReadOnly, "notify": s.applyNotifySessionState}[path]
					err = apply(t.Context(), rows)
				}
				if err != nil || len(rows) != 1 {
					t.Fatalf("rows=%+v err=%v", rows, err)
				}
				want := 2
				if watching {
					want++
				}
				if rows[0].BackgroundTaskCount != want || rows[0].BackgroundServiceCount != 2 || rows[0].Archived != watching || rows[0].Status != db.StatusDone {
					t.Fatalf("watching=%v row=%+v", watching, rows[0])
				}
				// Notification reads may reuse compact sidebar rows.
				if err := s.applyNotifySessionState(t.Context(), rows); err != nil {
					t.Fatal(err)
				}
				if rows[0].BackgroundTaskCount != want {
					t.Fatalf("watch counted twice: %+v", rows[0])
				}
			}
		})
	}
}
