package ocv2

import (
	"context"
	"net/http"
	"testing"
)

func TestBackgroundTaskCounts(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("shell.created", 1, "/repo", `{"info":{"id":"one","status":"running","metadata":{"sessionID":"s1"}}}`),
		fakeEv("shell.created", 2, "/repo", `{"info":{"id":"two","status":"running","metadata":{"sessionID":"s1"}}}`),
		fakeEv("shell.created", 3, "/repo", `{"info":{"id":"two","status":"running","metadata":{"sessionID":"s1"}}}`),
		fakeEv("shell.exited", 4, "/repo", `{"id":"one"}`),
		fakeEv("shell.deleted", 5, "/repo", `{"id":"two"}`),
	}
	counts := []any{}
	for _, event := range cmpStream(t, f.URL+"/event") {
		if str(event, "type") == "ocman.session.background" {
			props := obj(event, "properties")
			if str(props, "sessionID") != "s1" {
				t.Fatal("lost session ownership")
			}
			counts = append(counts, props["count"])
		}
	}
	cmpJSON(t, "counts", counts, `[1,2,2,1,0]`)
}

func TestBackgroundTaskCountWriteFailure(t *testing.T) {
	for _, typ := range []string{"shell.created", "shell.exited"} {
		t.Run(typ, func(t *testing.T) {
			tr := &translator{ctx: context.Background(), out: activityErrorWriter{}, activity: &activity{
				shells: map[string]string{"one": "s1"},
			}}
			data := map[string]any{"id": "one"}
			if typ == "shell.created" {
				data["info"] = map[string]any{"id": "two", "status": "running", "metadata": map[string]any{"sessionID": "s1"}}
			}
			if err := tr.handle(v2Event{Type: typ, Data: data}); err == nil {
				t.Fatal("background count write failure was lost")
			}
		})
	}
}

func TestBackgroundTaskReconnectSnapshot(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/debug/location", http.StatusOK, `[{"directory":"/repo"}]`)
	f.json("GET /api/shell", http.StatusOK, `{"data":[{"id":"one","status":"running","metadata":{"sessionID":"s1"}},{"id":"two","status":"running","metadata":{"sessionID":"s1"}},{"id":"unowned","status":"running"}]}`)
	f.events = []string{fakeEv("server.connected", 1, "", `{}`)}
	events := cmpStream(t, f.URL+"/event")
	cmpJSON(t, "snapshot", obj(events[0], "properties"), `{"backgroundTasks":{"s1":2}}`)
}
