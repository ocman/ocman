package ocv2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func agentFamily(t *testing.T) *fakeV2 {
	t.Helper()
	f := fakeServer(t)
	for id, parent := range map[string]string{"parent": "", "child": "parent", "second": "parent", "nested": "child", "unrelated": ""} {
		f.json("GET /api/session/"+id, http.StatusOK, fmt.Sprintf(`{"data":{"id":%q,"parentID":%q,"title":%q,"location":{"directory":"/repo"}}}`, id, parent, id))
	}
	return f
}

func parentActivity(events []map[string]any) ([]string, []any) {
	statuses, counts := []string{}, []any{}
	for _, ev := range events {
		p := obj(ev, "properties")
		if str(p, "sessionID") != "parent" {
			continue
		}
		switch str(ev, "type") {
		case "session.status":
			statuses = append(statuses, str(obj(p, "status"), "type"))
		case "ocman.session.background":
			counts = append(counts, p["count"])
		}
	}
	return statuses, counts
}

func TestAsyncSubagentKeepsParentWorking(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/child", http.StatusOK, `{"data":{"id":"child","parentID":"parent","title":"Review changes","location":{"directory":"/repo"}}}`)
	f.json("GET /api/session/parent", http.StatusOK, `{"data":{"id":"parent","location":{"directory":"/repo"}}}`)
	f.events = []string{
		fakeEv("session.execution.started", 1, "/repo", `{"sessionID":"parent"}`),
		fakeEv("session.execution.started", 2, "/repo", `{"sessionID":"child"}`),
		fakeEv("session.execution.succeeded", 3, "/repo", `{"sessionID":"parent"}`),
		fakeEv("session.execution.succeeded", 4, "/repo", `{"sessionID":"child"}`),
	}
	statuses, counts := parentActivity(cmpStream(t, f.URL+"/event"))
	cmpJSON(t, "parent statuses", statuses, `["busy","busy","busy","idle"]`)
	cmpJSON(t, "background agents", counts, `[1,0]`)
}

func TestAsyncSubagentTerminalTransitions(t *testing.T) {
	defer SetInstalledV2(true)()
	for _, terminal := range []string{"succeeded", "failed", "interrupted", "deleted"} {
		t.Run(terminal, func(t *testing.T) {
			f := agentFamily(t)
			f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":true,"second":true}}`)
			typ := "session.execution." + terminal
			if terminal == "deleted" {
				typ = "session.deleted"
			}
			f.events = []string{
				fakeEv(typ, 1, "/repo", `{"sessionID":"child"}`),
				fakeEv("session.execution.succeeded", 2, "/repo", `{"sessionID":"second"}`),
			}
			statuses, counts := parentActivity(cmpStream(t, f.URL+"/event"))
			cmpJSON(t, "parent statuses", statuses, `["busy","idle"]`)
			cmpJSON(t, "remaining agents", counts, `[1,0]`)
		})
	}
}

func TestAsyncSubagentNestedAndUnrelated(t *testing.T) {
	defer SetInstalledV2(true)()
	f := agentFamily(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":true,"nested":true,"unrelated":true}}`)
	f.events = []string{
		fakeEv("session.execution.succeeded", 1, "/repo", `{"sessionID":"child"}`),
		fakeEv("session.execution.succeeded", 2, "/repo", `{"sessionID":"nested"}`),
	}
	statuses, counts := parentActivity(cmpStream(t, f.URL+"/event"))
	cmpJSON(t, "parent statuses", statuses, `["busy","idle"]`)
	cmpJSON(t, "nested agents count once", counts, `[1,0]`)
}

func TestAsyncSubagentParentResumesBeforeChildExits(t *testing.T) {
	defer SetInstalledV2(true)()
	f := agentFamily(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":true}}`)
	f.events = []string{
		fakeEv("session.execution.started", 1, "/repo", `{"sessionID":"parent"}`),
		fakeEv("session.execution.succeeded", 2, "/repo", `{"sessionID":"child"}`),
		fakeEv("session.execution.succeeded", 3, "/repo", `{"sessionID":"parent"}`),
	}
	statuses, _ := parentActivity(cmpStream(t, f.URL+"/event"))
	cmpJSON(t, "parent statuses", statuses, `["busy","busy","idle"]`)
}

func TestAsyncSubagentShellOutlivesChildExecution(t *testing.T) {
	defer SetInstalledV2(true)()
	f := agentFamily(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":true}}`)
	f.events = []string{
		fakeEv("shell.created", 1, "/repo", `{"info":{"id":"shell","status":"running","metadata":{"sessionID":"child"}}}`),
		fakeEv("session.execution.succeeded", 2, "/repo", `{"sessionID":"child"}`),
		fakeEv("shell.exited", 3, "/repo", `{"id":"shell"}`),
	}
	statuses, counts := parentActivity(cmpStream(t, f.URL+"/event"))
	cmpJSON(t, "parent statuses", statuses, `["busy","busy","idle"]`)
	cmpJSON(t, "child counted once", counts, `[1,1,0]`)
}

func TestAsyncSubagentOwnershipReadFailure(t *testing.T) {
	defer SetInstalledV2(true)()
	f := agentFamily(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":true}}`)
	f.json("GET /api/session/child", http.StatusServiceUnavailable, `{}`)
	status, _ := cmpDo(t, http.MethodGet, f.URL+"/session/status", nil, "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("partial ownership snapshot returned %d", status)
	}
	tr := &translator{c: &compat{base: f.URL, rt: http.DefaultTransport}, ctx: t.Context(), out: &bytes.Buffer{}, activity: &activity{}}
	for _, typ := range []string{"session.execution.started", "session.retry.scheduled", "shell.created"} {
		d := map[string]any{"sessionID": "child", "info": map[string]any{"id": "shell", "status": "running", "metadata": map[string]any{"sessionID": "child"}}}
		tr.activity = &activity{shells: map[string]string{}}
		if err := tr.handle(v2Event{Type: typ, Data: d}); err == nil {
			t.Fatalf("%s lost ownership read failure", typ)
		}
	}
}

func TestAsyncSubagentCyclesAndWriteErrors(t *testing.T) {
	a := &activity{parents: map[string]string{"child": "parent", "parent": "child"}, foreground: map[string]any{"child": true}, shells: map[string]string{"shell": "child"}}
	if len(a.ancestors("child")) != 1 || !a.busy("parent") || a.busy("other") {
		t.Fatal("cycle handling lost activity or borrowed unrelated activity")
	}
	if _, foreground := a.foreground["parent"]; foreground {
		t.Fatal("child activity manufactured parent foreground execution")
	}
	if err := a.loadParents(t.Context(), nil, "child"); err != nil {
		t.Fatal(err)
	}
	a.rememberSession(map[string]any{})
	a.rememberSession(map[string]any{"id": "parent"})
	if len(a.ancestors("child")) != 1 {
		t.Fatal("lost native parent link")
	}
	tr := &translator{ctx: context.Background(), out: activityErrorWriter{}, activity: a}
	if err := tr.emitActivityFamily("child", "/repo"); err == nil {
		t.Fatal("lost activity write failure")
	}
}

type agentFailAfterWriter struct{ remaining int }

func (w *agentFailAfterWriter) Write(p []byte) (int, error) {
	if w.remaining == 0 {
		return 0, errors.New("stream closed")
	}
	w.remaining--
	return len(p), nil
}

func TestAsyncSubagentAncestorWriteFailure(t *testing.T) {
	for _, remaining := range []int{1, 2} {
		tr := &translator{ctx: t.Context(), out: &agentFailAfterWriter{remaining: remaining}, activity: &activity{
			foreground: map[string]any{"child": true}, parents: map[string]string{"child": "parent"},
		}}
		if err := tr.emitActivityFamily("child", "/repo"); err == nil {
			t.Fatalf("lost ancestor write failure after %d writes", remaining)
		}
	}
}

func TestAsyncSubagentCreationSuppliesOwnership(t *testing.T) {
	defer SetInstalledV2(true)()
	f := agentFamily(t)
	f.events = []string{
		fakeEv("session.created", 1, "/repo", `{"sessionID":"child"}`),
		fakeEv("session.execution.started", 2, "/repo", `{"sessionID":"child"}`),
		fakeEv("session.retry.scheduled", 3, "/repo", `{"sessionID":"child","attempt":1}`),
		fakeEv("session.execution.succeeded", 4, "/repo", `{"sessionID":"child"}`),
	}
	statuses, _ := parentActivity(cmpStream(t, f.URL+"/event"))
	cmpJSON(t, "parent statuses", statuses, `["busy","busy","idle"]`)
	f.one("GET /api/session/child") // No metadata fetch on every execution edge.
}

func TestAsyncSubagentStatusSnapshot(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/active", http.StatusOK, `{"data":{"child":{"type":"running"}}}`)
	f.json("GET /api/session/child", http.StatusOK, `{"data":{"id":"child","parentID":"parent","title":"Review changes","location":{"directory":"/repo"}}}`)
	f.json("GET /api/session/parent", http.StatusOK, `{"data":{"id":"parent","location":{"directory":"/repo"}}}`)
	cmpJSON(t, "status", cmpOK(t, http.MethodGet, f.URL+"/session/status", ""), `{"child":{"type":"busy"},"parent":{"type":"busy"}}`)
	f.events = []string{fakeEv("server.connected", 1, "", `{}`)}
	events := cmpStream(t, f.URL+"/event")
	cmpJSON(t, "background snapshot", obj(events[0], "properties"), `{"backgroundTasks":{"parent":1},"backgroundTaskDetails":{"parent":[{"id":"child","description":"Review changes","kind":"agent"}]}}`)
}
