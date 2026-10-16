package ocv2

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestBackgroundShellKeepsSessionBusy(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("session.execution.started", 1, "/repo", `{"sessionID":"s1"}`),
		fakeEv("shell.created", 2, "/repo", `{"info":{"id":"sh_1","status":"running","metadata":{"sessionID":"s1"}}}`),
		fakeEv("session.execution.succeeded", 3, "/repo", `{"sessionID":"s1"}`),
		fakeEv("shell.exited", 4, "/repo", `{"id":"sh_1","status":"exited","exit":0}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	statuses := []string{}
	for _, ev := range evs {
		if str(ev, "type") == "session.status" {
			statuses = append(statuses, str(obj(obj(ev, "properties"), "status"), "type"))
		}
	}
	// No idle transition at foreground completion. Only the shell's exit
	// makes this session ready for attention.
	cmpJSON(t, "statuses", statuses, `["busy","busy","busy","idle"]`)
	if str(evs[len(evs)-1], "type") != "session.idle" {
		t.Fatal("last shell exit did not settle the session")
	}
}

func TestBackgroundShellStatusSnapshot(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"foreground":{"type":"running"}}}`)
	f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"},{"directory":"/repo/worktree"}]`)
	f.on("GET /api/shell", func(c fakeCall) (int, any) {
		if c.Query.Get("location[directory]") == "/repo/worktree" {
			return http.StatusOK, map[string]any{"data": []any{
				map[string]any{"id": "sh_1", "status": "running", "metadata": map[string]any{"sessionID": "background"}},
				map[string]any{"id": "sh_2", "status": "running", "metadata": map[string]any{}},
				map[string]any{"id": "sh_3", "status": "exited", "metadata": map[string]any{"sessionID": "finished"}},
			}}
		}
		return http.StatusOK, map[string]any{"data": []any{}}
	})
	cmpJSON(t, "status", cmpOK(t, http.MethodGet, f.URL+"/session/status", ""),
		`{"foreground":{"type":"busy"},"background":{"type":"busy"}}`)
}

func TestBackgroundShellTransitions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
		want   string
	}{
		{"overlapping commands", []string{
			fakeEv("shell.created", 1, "/repo", `{"info":{"id":"sh_1","status":"running","metadata":{"sessionID":"s1"}}}`),
			fakeEv("shell.created", 2, "/repo", `{"info":{"id":"sh_2","status":"running","metadata":{"sessionID":"s1"}}}`),
			fakeEv("shell.exited", 3, "/repo", `{"id":"sh_1","status":"timeout"}`),
			fakeEv("shell.deleted", 4, "/repo", `{"id":"sh_2"}`),
		}, `["busy","busy","busy","idle"]`},
		{"foreground outlasts shell", []string{
			fakeEv("session.execution.started", 1, "/repo", `{"sessionID":"s1"}`),
			fakeEv("shell.created", 2, "/repo", `{"info":{"id":"sh_1","status":"running","metadata":{"sessionID":"s1"}}}`),
			fakeEv("shell.exited", 3, "/repo", `{"id":"sh_1","status":"killed"}`),
			fakeEv("session.execution.failed", 4, "/repo", `{"sessionID":"s1"}`),
		}, `["busy","busy","busy","idle"]`},
		{"retry remains active", []string{
			fakeEv("session.retry.scheduled", 1, "/repo", `{"sessionID":"s1","attempt":2,"error":{"message":"rate limited"},"at":12345}`),
			fakeEv("shell.created", 2, "/repo", `{"info":{"id":"sh_1","status":"running","metadata":{"sessionID":"s1"}}}`),
			fakeEv("shell.exited", 3, "/repo", `{"id":"sh_1","status":"exited"}`),
			fakeEv("session.execution.failed", 4, "/repo", `{"sessionID":"s1"}`),
		}, `["retry","retry","retry","idle"]`},
		{"unowned and malformed shells", []string{
			fakeEv("shell.created", 1, "/repo", `{"info":{"id":"sh_1","status":"running","metadata":{}}}`),
			fakeEv("shell.created", 2, "/repo", `{"info":{"status":"running","metadata":{"sessionID":"s1"}}}`),
			fakeEv("shell.created", 3, "/repo", `{"info":{"id":"sh_2","status":"exited","metadata":{"sessionID":"s1"}}}`),
			fakeEv("shell.deleted", 4, "/repo", `{"id":"unknown"}`),
		}, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer SetInstalledV2(true)()
			f := fakeServer(t)
			f.events = tc.events
			statuses := []string{}
			idle := 0
			for _, ev := range cmpStream(t, f.URL+"/event") {
				if str(ev, "type") == "session.status" {
					statuses = append(statuses, str(obj(obj(ev, "properties"), "status"), "type"))
				}
				if str(ev, "type") == "session.idle" {
					idle++
				}
			}
			cmpJSON(t, "statuses", statuses, tc.want)
			if strings.Contains(tc.want, "idle") && idle != 1 {
				t.Fatalf("got %d idle edges, want one", idle)
			}
		})
	}
}

func TestBackgroundShellReconnect(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"s1":{"type":"running"}}}`)
	f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"},{"directory":"/repo"},{}]`)
	f.json("GET /api/shell", http.StatusOK, `{"data":[{"id":"sh_1","status":"running","metadata":{"sessionID":"s1"}}]}`)
	f.events = []string{
		fakeEv("session.execution.interrupted", 1, "/repo", `{"sessionID":"s1"}`),
		fakeEv("shell.exited", 2, "/repo", `{"id":"sh_1","status":"exited"}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpTypeSeq(t, evs, "session.status", "session.status", "session.idle")
	cmpJSON(t, "still busy", obj(evs[0], "properties"), `{"sessionID":"s1","status":{"type":"busy"}}`)
	f.one("GET /api/shell") // duplicate locations were not fetched twice
}

func TestActivityDirectoryScope(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"},{"directory":"/other"}]`)
	f.on("GET /api/shell", func(c fakeCall) (int, any) {
		if c.Query.Get("location[directory]") != "/repo" {
			t.Errorf("read unrelated location: %v", c.Query)
		}
		return http.StatusOK, map[string]any{"data": []any{
			map[string]any{"id": "sh_1", "status": "running", "metadata": map[string]any{"sessionID": "s1"}},
		}}
	})
	cmpJSON(t, "scoped status", cmpOK(t, http.MethodGet, f.URL+"/session/status?directory=/repo/", ""), `{"s1":{"type":"busy"}}`)
	f.events = []string{
		fakeEv("shell.exited", 1, "/other", `{"id":"sh_other","status":"exited"}`),
		fakeEv("shell.exited", 2, "/repo", `{"id":"sh_1","status":"exited"}`),
	}
	cmpTypeSeq(t, cmpStream(t, f.URL+"/event?directory=/repo/"), "session.status", "session.idle")
}

func TestActivitySnapshotErrors(t *testing.T) {
	for _, path := range []string{"/api/session/active", "/api/debug/location", "/api/shell"} {
		t.Run(path, func(t *testing.T) {
			defer SetInstalledV2(true)()
			f := fakeServer(t)
			f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"}]`)
			f.json("GET "+path, http.StatusBadGateway, `{"error":"unavailable"}`)
			status, _ := cmpDo(t, http.MethodGet, f.URL+"/session/status", nil, "")
			if status != http.StatusBadGateway {
				t.Fatalf("partial activity snapshot returned status %d", status)
			}
			resp, err := (&http.Client{Transport: Wrap(http.DefaultTransport)}).Get(f.URL + "/event")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("stream accepted an incomplete activity snapshot: HTTP %d", resp.StatusCode)
			}
		})
	}
}

func TestActivityEmptyForeground(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":null}`)
	f.events = []string{fakeEv("session.execution.started", 1, "", `{"sessionID":"s1"}`)}
	cmpTypeSeq(t, cmpStream(t, f.URL+"/event"), "session.status")
}

type activityErrorWriter struct{}

func (activityErrorWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestEmitActivityWriteFailure(t *testing.T) {
	tr := &translator{ctx: context.Background(), out: activityErrorWriter{}, activity: &activity{}}
	if err := tr.emitActivity("s1", ""); err == nil {
		t.Fatal("lost stream write error")
	}
	tr.out = &bytes.Buffer{}
	if err := tr.emitActivity("s1", ""); err != nil {
		t.Fatal(err)
	}
}
