package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

func TestStopScanAdmissionWindow(t *testing.T) {
	for _, newSession := range []bool{false, true} {
		for _, manual := range []bool{false, true} {
			t.Run(fmt.Sprintf("new-session=%t/manual=%t", newSession, manual), func(t *testing.T) {
				srv, raw := newInterruptionRawServer(t)
				defer raw.Close()
				if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('a','/repo'),('z','/repo'); INSERT INTO message(id,session_id,time_created,data) VALUES ('a-old','a',1,'{"role":"assistant","finish":"stop"}'),('z-old','z',1,'{"role":"assistant","finish":"stop"}')`); err != nil {
					t.Fatal(err)
				}
				scanning := false
				var admittedAt int64
				id := "a"
				if newSession {
					id = "new"
				}
				srv.registry.Register(&interruptionLifecyclePlatform{fakePlatform: &fakePlatform{id: "opencode"}, lifecycle: func(session string) (*platforms.SessionLifecycle, error) {
					if scanning && session == "z" {
						admittedAt = time.Now().UnixMilli()
						if newSession {
							if _, err := raw.Exec(`INSERT INTO session(id,directory) VALUES ('new','/repo')`); err != nil {
								return nil, err
							}
						}
						data := `{"role":"assistant","finish":"tool-calls"}`
						if manual {
							data = fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, admittedAt)
						}
						if _, err := raw.Exec(`INSERT INTO message(id,session_id,time_created,data) VALUES ('admitted',?,?,?)`, id, admittedAt, data); err != nil {
							return nil, err
						}
						// Hold the scan until its entry timestamp is demonstrably later
						// than admission. This tests the ordering, not an arbitrary sleep.
						testutil.WaitFor(t, time.Second, "the stop scan to outlast admission", func() bool { return time.Now().UnixMilli() > admittedAt })
					}
					return &platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageID: session + "-old", LatestMessageCreated: 1}, nil
				}})
				if err := srv.recordOpencodeReplacement(t.Context(), "/repo", "restart"); err != nil {
					t.Fatal(err)
				}
				scanning = true
				if err := srv.beginOpencodeReplacementStop(t.Context(), "/repo"); err != nil {
					t.Fatal(err)
				}
				stopAt, _, err := srv.stateDB.SessionReplacementStopEvidence(t.Context(), "opencode", "/repo")
				if err != nil || admittedAt >= stopAt {
					t.Fatalf("bad test ordering: admitted=%d stop=%d err=%v", admittedAt, stopAt, err)
				}
				if !manual {
					if _, err := raw.Exec(`UPDATE message SET data=? WHERE id='admitted'`, fmt.Sprintf(`{"role":"assistant","error":{"name":"AbortError"},"time":{"completed":%d}}`, stopAt)); err != nil {
						t.Fatal(err)
					}
				}
				if err := srv.confirmOpencodeReplacement(t.Context(), "/repo"); err != nil {
					t.Fatal(err)
				}
				got, err := srv.stateDB.SessionInterruptions(t.Context(), "opencode", id)
				want := 1
				if manual {
					want = 0
				}
				if err != nil || len(got) != want {
					t.Fatalf("scan admission notices=%v want=%d err=%v", got, want, err)
				}
			})
		}
	}
}
