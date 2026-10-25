package ocv2

import (
	"net/http"
	"testing"
)

func TestForegroundCompletionIgnoresBackgroundWork(t *testing.T) {
	defer SetInstalledV2(true)()
	for _, terminal := range []string{"succeeded", "failed", "interrupted"} {
		t.Run(terminal, func(t *testing.T) {
			f := agentFamily(t)
			f.events = []string{
				fakeEv("session.execution.started", 1, "/repo", `{"sessionID":"parent"}`),
				fakeEv("shell.created", 2, "/repo", `{"info":{"id":"vite","command":"pnpm dev","status":"running","metadata":{"sessionID":"parent"}}}`),
				fakeEv("session.execution.started", 3, "/repo", `{"sessionID":"child"}`),
				fakeEv("session.execution."+terminal, 4, "/repo", `{"sessionID":"parent"}`),
			}
			events := cmpStream(t, f.URL+"/event")
			statuses, counts := parentActivity(events)
			cmpJSON(t, "foreground statuses", statuses, `["busy","idle"]`)
			cmpJSON(t, "background work remains", counts, `[1,2]`)
			if len(f.find("DELETE /api/shell/vite")) != 0 {
				t.Fatal("foreground completion stopped the preview server")
			}
		})
	}
}

func TestForegroundStatusSnapshotIgnoresBackgroundWork(t *testing.T) {
	defer SetInstalledV2(true)()
	f := agentFamily(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":{"type":"running"}}}`)
	f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"}]`)
	f.json("GET /api/shell", http.StatusOK, `{"data":[{"id":"vite","status":"running","metadata":{"sessionID":"parent"}}]}`)
	cmpJSON(t, "only foreground sessions are busy", cmpOK(t, http.MethodGet, f.URL+"/session/status", ""), `{"child":{"type":"busy"}}`)
}
