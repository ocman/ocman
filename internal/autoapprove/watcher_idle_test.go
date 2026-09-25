package autoapprove

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

func TestWatcherIdlePairReadsStatusOnce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		events      []string
		reads, idle int
	}{
		{"pair", []string{"status", "idle"}, 1, 1},
		{"legacy", []string{"idle", "idle"}, 2, 2},
		{"status only", []string{"status"}, 1, 0},
		{"two turns", []string{"status", "idle", "busy", "status", "idle"}, 2, 2},
		{"busy clears pair", []string{"status", "busy", "idle"}, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			for _, event := range tc.events {
				payload := `{"type":"session.idle","properties":{"sessionID":"ses-1"}}`
				if event != "idle" {
					status := "idle"
					if event == "busy" {
						status = "busy"
					}
					payload = `{"type":"session.status","properties":{"sessionID":"ses-1","status":{"type":"` + status + `"}}}`
				}
				events = append(events, "data: "+payload+"\n\n")
			}
			server := newFakeOpenCodeEventServer(events)
			defer server.close()
			// An empty temporary DB makes every attempted status read fail and
			// broadcast a refresh, letting us count reads at the real stream seam.
			database, err := db.OpenReadWrite(t.TempDir() + "/opencode.db")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			adapter := opencode.New(database, nil)
			reads, idle := 0, 0
			svc := NewService(Deps{
				OpencodePlatform:        func() platforms.Platform { return adapter },
				BroadcastSessionStatus:  func(string, db.SessionStatus) {},
				BroadcastSessionChanged: func(string) { reads++ },
				BroadcastSessionIdle:    func(string, string) { idle++ },
			})
			w := newAutoApproveWatcher(svc)
			if err := w.streamOnce(context.Background(), server.port()); err != nil {
				t.Fatal(err)
			}
			if reads != tc.reads || idle != tc.idle {
				t.Fatalf("status reads = %d, idle callbacks = %d; want %d, %d", reads, idle, tc.reads, tc.idle)
			}
		})
	}
}
