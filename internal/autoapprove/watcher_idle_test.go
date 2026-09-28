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

// Retry statuses reach SessionRetry with their payload so quota
// detection runs whether or not auto-approve is enabled.
func TestWatcherForwardsRetryStatus(t *testing.T) {
	server := newFakeOpenCodeEventServer([]string{
		`data: {"type":"session.status","properties":{"sessionID":"ses-1","status":{"type":"busy"}}}` + "\n\n",
		`data: {"type":"session.status","properties":{"sessionID":"ses-1","status":{"type":"retry","action":{"reason":"free_tier_limit"},"next":42}}}` + "\n\n",
	})
	defer server.close()
	var got []SessionStatus
	svc := NewService(Deps{SessionRetry: func(id string, st SessionStatus) {
		if id == "ses-1" {
			got = append(got, st)
		}
	}})
	if err := newAutoApproveWatcher(svc).streamOnce(context.Background(), server.port()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ActionReason != "free_tier_limit" || got[0].Next != 42 {
		t.Fatalf("retries = %+v", got)
	}
}
