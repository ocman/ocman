package claudeprovider

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestIdleHostExitRetriesChildSettlement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		followup bool
		partial  db.SessionStatus
	}{
		{"idle parent", false, db.StatusBusy},
		{"after followup", true, db.StatusBusy},
		{"partial error", true, db.StatusError},
		{"partial interruption", true, db.StatusInterrupted},
		{"partial completion", true, db.StatusDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastRetries(t)
			gate := filepath.Join(t.TempDir(), "exit")
			rows := `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"system","subtype":"task_started","task_type":"local_agent","task_id":"child","tool_use_id":"spawn","description":"Investigate"}}
{"direction":"stdout","message":{"type":"result","subtype":"success"}}
`
			if tc.followup {
				rows += oneTurn
			}
			rows += fmt.Sprintf("{\"direction\":\"wait\",\"message\":%q}\n", gate)
			h := startLive(t, writeFixture(t, rows), nil)
			id := h.create(t)
			h.send(t, id, "first")
			waitFor(t, "foreground idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
			if tc.followup {
				h.send(t, id, "next")
				waitFor(t, "followup idle", func() bool { _, _, n := h.events.get(id); return n == 2 })
			}
			const child = "agent-child"
			if tc.partial != db.StatusBusy {
				h.live.emit(child, "", "session.status", map[string]any{"sessionID": child, "status": map[string]string{"type": string(tc.partial)}})
			}
			restore := h.spoolDown(t, child)
			h.live.mu.Lock()
			p := h.live.procs[id]
			h.live.mu.Unlock()
			if err := p.cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "host reaped", func() bool { return h.procs() == 0 })
			waitFor(t, "child settlement retry registered", func() bool {
				h.live.mu.Lock()
				defer h.live.mu.Unlock()
				_, ok := h.live.unsettled[child]
				return ok
			})
			if !h.supervisorActive(child) {
				t.Fatal("child released before its idle edge could be saved")
			}
			restore()
			waitFor(t, "child released after recovery", func() bool { return !h.supervisorActive(child) })
			want := tc.partial
			if want == db.StatusBusy {
				want = db.StatusInterrupted
			}
			h.live.mu.Lock()
			got, parent := h.live.status[child].Status, h.live.status[id].Status
			h.live.mu.Unlock()
			if got != want || parent != db.StatusDone || !drainIdle(t, h) {
				t.Fatalf("child=%s want=%s parent=%s, or supervisor cannot drain", got, want, parent)
			}
		})
	}
}
