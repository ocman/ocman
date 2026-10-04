package ocv2

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// fakeEv builds one v2 event frame. dir "" omits the location.
func fakeEv(typ string, created float64, dir, data string) string {
	ev := map[string]any{"id": "evt", "type": typ, "created": created, "data": json.RawMessage(data)}
	if dir != "" {
		ev["location"] = map[string]any{"directory": dir}
	}
	b, _ := json.Marshal(ev)
	return string(b)
}

func cmpTypes(evs []map[string]any) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, str(e, "type"))
	}
	return out
}

func cmpTypeSeq(t *testing.T, evs []map[string]any, want ...string) {
	t.Helper()
	if got := cmpTypes(evs); !reflect.DeepEqual(got, want) {
		t.Fatalf("event types:\n got  %q\n want %q", got, want)
	}
}

func cmpPart(ev map[string]any) map[string]any { return obj(obj(ev, "properties"), "part") }

func TestEventsLifecycle(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("server.connected", 1, "", `{}`),
		": heartbeat",
		fakeEv("session.execution.started", 2, "", `{"sessionID":"s1"}`),
		fakeEv("session.retry.scheduled", 3, "", `{"sessionID":"s1","attempt":2,"error":{"message":"rate limited"},"at":12345}`),
		fakeEv("session.execution.succeeded", 4, "", `{"sessionID":"s1"}`),
		`not json`,
		fakeEv("some.unknown.event", 5, "", `{"sessionID":"s1"}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpJSON(t, "events", evs, `[
		{"type":"server.connected","properties":{}},
		{"type":"server.heartbeat","properties":{}},
		{"type":"session.status","properties":{"sessionID":"s1","status":{"type":"busy"}}},
		{"type":"session.status","properties":{"sessionID":"s1","status":{"type":"retry","attempt":2,"message":"rate limited","next":12345}}},
		{"type":"session.status","properties":{"sessionID":"s1","status":{"type":"idle"}}},
		{"type":"session.idle","properties":{"sessionID":"s1"}}]`)
	if c := f.one("GET /api/event"); c.Path != "/api/event" {
		t.Fatal("no upstream subscription")
	}
}

func TestEventsAssistantFlow(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	const d = `"sessionID":"s1","assistantMessageID":"msg01"`
	f.events = []string{
		fakeEv("session.step.started", 100, "", `{`+d+`,"agent":"build","model":{"providerID":"openai","id":"gpt"},"started":100,"snapshot":"snap1"}`),
		fakeEv("session.text.started", 101, "", `{`+d+`}`),
		fakeEv("session.text.delta", 102, "", `{`+d+`,"delta":"Hel"}`),
		fakeEv("session.text.ended", 103, "", `{`+d+`,"text":"Hello"}`),
		fakeEv("session.reasoning.started", 104, "", `{`+d+`}`),
		fakeEv("session.reasoning.delta", 105, "", `{`+d+`,"delta":"th"}`),
		fakeEv("session.reasoning.ended", 106, "", `{`+d+`,"text":"think"}`),
		fakeEv("session.tool.input.started", 107, "", `{`+d+`,"id":"call1","name":"shell"}`),
		fakeEv("session.tool.called", 108, "", `{`+d+`,"id":"call1","input":{"command":"ls"}}`),
		fakeEv("session.tool.progress", 109, "", `{`+d+`,"id":"call1","metadata":{"pid":7}}`),
		fakeEv("session.tool.success", 110, "", `{`+d+`,"id":"call1","content":[{"type":"text","text":"out"}],"metadata":{"exit":0}}`),
		fakeEv("session.tool.input.started", 111, "", `{`+d+`,"id":"call2","name":"read"}`),
		fakeEv("session.tool.failed", 112, "", `{`+d+`,"id":"call2","error":{"message":"boom"}}`),
		fakeEv("session.tool.success", 113, "", `{`+d+`,"id":"nope"}`), // unknown tool: ignored
		fakeEv("session.step.ended", 200, "", `{`+d+`,"finish":"stop","cost":0.5,"tokens":{"input":1,"output":2}}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpTypeSeq(t, evs,
		"message.updated", "message.part.updated", // step.started
		"message.part.updated",                         // text.started
		"message.part.delta",                           // text.delta
		"message.part.updated",                         // text.ended
		"message.part.updated",                         // reasoning.started
		"message.part.delta",                           // reasoning.delta
		"message.part.updated",                         // reasoning.ended
		"message.part.updated",                         // tool input.started
		"message.part.updated",                         // tool.called
		"message.part.updated",                         // tool.progress
		"message.part.updated",                         // tool.success
		"message.part.updated", "message.part.updated", // second tool started + failed
		"message.updated", "message.part.updated", // step.ended
	)
	info := obj(obj(evs[0], "properties"), "info")
	if info["id"] != "msg01" || info["role"] != "assistant" || info["agent"] != "build" || info["providerID"] != "openai" || info["modelID"] != "gpt" {
		t.Errorf("started info = %v", info)
	}
	if p := cmpPart(evs[1]); p["id"] != PartID("msg01", 0) || p["type"] != "step-start" || p["snapshot"] != "snap1" {
		t.Errorf("step-start = %v", p)
	}
	if p := cmpPart(evs[2]); p["id"] != PartID("msg01", 1) || p["type"] != "text" || p["text"] != "" {
		t.Errorf("text start = %v", p)
	}
	cmpJSON(t, "text delta", evs[3]["properties"],
		`{"sessionID":"s1","messageID":"msg01","partID":"`+PartID("msg01", 1)+`","field":"text","delta":"Hel"}`)
	if p := cmpPart(evs[4]); p["id"] != PartID("msg01", 1) || p["text"] != "Hello" {
		t.Errorf("text end = %v", p)
	}
	if p := cmpPart(evs[5]); p["id"] != PartID("msg01", 2) || p["type"] != "reasoning" {
		t.Errorf("reasoning start = %v", p)
	}
	if pid := obj(evs[6], "properties")["partID"]; pid != PartID("msg01", 2) {
		t.Errorf("reasoning delta partID = %v", pid)
	}
	if p := cmpPart(evs[7]); p["text"] != "think" || obj(p, "time")["end"] != 106.0 {
		t.Errorf("reasoning end = %v", p)
	}
	toolState := func(i int) map[string]any { return obj(cmpPart(evs[i]), "state") }
	if p := cmpPart(evs[8]); p["id"] != PartID("msg01", 3) || p["tool"] != "bash" || p["callID"] != "call1" || str(toolState(8), "status") != "pending" {
		t.Errorf("tool pending = %v", p)
	}
	if s := toolState(9); s["status"] != "running" || obj(s, "input")["command"] != "ls" {
		t.Errorf("tool running = %v", s)
	}
	if s := toolState(10); s["status"] != "running" || obj(s, "metadata")["pid"] != 7.0 {
		t.Errorf("tool progress = %v", s)
	}
	if s := toolState(11); s["status"] != "completed" || s["output"] != "out" || obj(s, "input")["command"] != "ls" {
		t.Errorf("tool completed = %v", s)
	}
	if p := cmpPart(evs[13]); p["id"] != PartID("msg01", 4) || str(obj(p, "state"), "status") != "error" || str(obj(p, "state"), "error") != "boom" {
		t.Errorf("tool error = %v", p)
	}
	info = obj(obj(evs[14], "properties"), "info")
	if info["finish"] != "stop" || info["cost"] != 0.5 || obj(info, "tokens")["output"] != 2.0 || obj(info, "time")["completed"] != 200.0 {
		t.Errorf("ended info = %v", info)
	}
	if p := cmpPart(evs[15]); p["id"] != PartID("msg01", 5) || p["type"] != "step-finish" || p["reason"] != "stop" {
		t.Errorf("step-finish = %v", p)
	}
	// step.started looks the message up once (a retry keeps earlier
	// content); every later event reuses the projection.
	if n := len(f.find("GET /api/session/s1/message/msg01")); n != 1 {
		t.Errorf("a started message was fetched %d times, want 1", n)
	}
}

func TestEventsSeedUnknownAssistant(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s2/message/msg09", http.StatusOK, `{"data":{"id":"msg09","type":"assistant","agent":"build",
		"model":{"providerID":"p","id":"m"},"time":{"created":1},"content":[{"type":"text","text":"Hi"}]}}`)
	f.json("GET /api/session/s2/message/msg10", http.StatusOK, `{"data":{"id":"msg10","type":"assistant","agent":"build",
		"model":{"providerID":"p","id":"m"},"time":{"created":2},"content":[{"type":"text","text":"Done"}]}}`)
	f.events = []string{
		// ephemeral: seeded snapshot, then the delta on top
		fakeEv("session.text.delta", 3, "", `{"sessionID":"s2","assistantMessageID":"msg09","delta":"!"}`),
		// now cached: no refetch
		fakeEv("session.text.delta", 4, "", `{"sessionID":"s2","assistantMessageID":"msg09","delta":"?"}`),
		// durable: already in the snapshot, not re-applied
		fakeEv("session.text.ended", 5, "", `{"sessionID":"s2","assistantMessageID":"msg10","text":"Done"}`),
		// seed fails: dropped
		fakeEv("session.text.delta", 6, "", `{"sessionID":"s2","assistantMessageID":"msg11","delta":"x"}`),
		// no message id: dropped
		fakeEv("session.text.delta", 7, "", `{"sessionID":"s2","delta":"x"}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpTypeSeq(t, evs,
		"message.updated", "message.part.updated", "message.part.updated", "message.part.delta",
		"message.part.delta",
		"message.updated", "message.part.updated", "message.part.updated")
	if p := cmpPart(evs[2]); p["text"] != "Hi" || p["id"] != PartID("msg09", 1) {
		t.Errorf("seeded text = %v", p)
	}
	for i, want := range map[int]string{3: "!", 4: "?"} {
		if pr := obj(evs[i], "properties"); pr["partID"] != PartID("msg09", 1) || pr["delta"] != want {
			t.Errorf("delta %d = %v", i, pr)
		}
	}
	if info := obj(obj(evs[5], "properties"), "info"); info["id"] != "msg10" {
		t.Errorf("msg10 info = %v", info)
	}
	if p := cmpPart(evs[7]); p["text"] != "Done" {
		t.Errorf("msg10 text = %v", p)
	}
	if n := len(f.find("GET /api/session/s2/message/msg09")); n != 1 {
		t.Errorf("msg09 fetched %d times, want 1", n)
	}
	if n := len(f.find("GET /api/session/s2/message/msg11")); n != 1 {
		t.Errorf("msg11 fetched %d times, want 1", n)
	}
}

func TestEventsExecutionEndForgetsMessages(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("session.step.started", 1, "", `{"sessionID":"s1","assistantMessageID":"msg01","started":1}`),
		fakeEv("session.execution.failed", 2, "", `{"sessionID":"s1"}`),
		fakeEv("session.text.started", 3, "", `{"sessionID":"s1","assistantMessageID":"msg01"}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpTypeSeq(t, evs, "message.updated", "message.part.updated", "session.status", "session.idle")
	// Once on step.started, once after the execution ended and forgot it.
	if n := len(f.find("GET /api/session/s1/message/msg01")); n != 2 {
		t.Errorf("message fetched %d times, want 2", n)
	}
}

// A retried step reuses its message id and OpenCode keeps the first
// attempt's content: a stream that missed the first attempt must load it
// so the retry's parts get the stored indexes.
func TestEventsRetriedStepKeepsStoredIndexes(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s1/message/msg01", http.StatusOK, `{"data":{"id":"msg01","type":"assistant","agent":"build",
		"model":{"providerID":"p","id":"m"},"time":{"created":1},"content":[{"type":"text","text":"first attempt"}]}}`)
	f.events = []string{
		fakeEv("session.step.started", 1, "", `{"sessionID":"s1","assistantMessageID":"msg01","started":5}`),
		fakeEv("session.text.started", 2, "", `{"sessionID":"s1","assistantMessageID":"msg01"}`),
		fakeEv("session.step.failed", 3, "", `{"sessionID":"s1","assistantMessageID":"msg01","error":{"type":"x","message":"boom"}}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	if p := cmpPart(evs[2]); p["id"] != PartID("msg01", 2) {
		t.Errorf("retry text part = %v, want index 2", p["id"])
	}
	if info := obj(obj(evs[3], "properties"), "info"); info["finish"] != "error" {
		t.Errorf("failed step info = %v, want finish error", info)
	}
}

// A message whose fetch failed is not refetched for every later delta.
func TestEventsFailedSeedIsNotRetriedPerDelta(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("session.text.delta", 1, "", `{"sessionID":"s1","assistantMessageID":"msgX","delta":"a"}`),
		fakeEv("session.text.delta", 2, "", `{"sessionID":"s1","assistantMessageID":"msgX","delta":"b"}`),
		fakeEv("session.text.delta", 3, "", `{"sessionID":"s1","assistantMessageID":"msgX","delta":"c"}`),
	}
	cmpStream(t, f.URL+"/event")
	if n := len(f.find("GET /api/session/s1/message/msgX")); n != 1 {
		t.Errorf("failed seed fetched %d times, want 1", n)
	}
}

// Events from other directories cost nothing on a directory stream.
func TestEventsOtherDirectoryDoesNoWork(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("session.created", 1, "/other", `{"sessionID":"s9"}`),
		fakeEv("session.text.delta", 2, "/other", `{"sessionID":"s9","assistantMessageID":"msgY","delta":"a"}`),
	}
	cmpStream(t, f.URL+"/event?directory=/mine")
	if n := len(f.find("GET /api/session/s9")) + len(f.find("GET /api/session/s9/message/msgY")); n != 0 {
		t.Errorf("other-directory events made %d fetches", n)
	}
}

func TestEventsSessionEvents(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/session/s3", http.StatusOK, `{"data":{"id":"s3","title":"T","location":{"directory":"/d"},"time":{"created":1,"updated":2}}}`)
	f.json("GET /api/session/s3/message", http.StatusOK, `{"data":[
		{"id":"msg2","type":"user","text":"newer","time":{"created":2}},
		{"id":"msg1","type":"user","text":"older","time":{"created":1}}]}`)
	f.events = []string{
		fakeEv("session.created", 1, "/d", `{"sessionID":"s3"}`),
		fakeEv("session.renamed", 2, "", `{"sessionID":"s3"}`),
		fakeEv("session.created", 3, "", `{"sessionID":"missing"}`), // fetch fails: dropped
		fakeEv("session.deleted", 4, "/d", `{"sessionID":"s3"}`),
		fakeEv("session.inbox.delivered", 5, "/d", `{"sessionID":"s3"}`),
		fakeEv("session.shell.started", 6, "", `{"sessionID":"gone"}`), // fetch fails: dropped
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpTypeSeq(t, evs, "session.created", "session.updated", "session.deleted",
		"message.updated", "message.part.updated", "message.updated", "message.part.updated", QueueChangedEvent)
	v1 := `{"id":"s3","projectID":"","directory":"/d","title":"T","version":"2","time":{"created":1,"updated":2},"permission":[]}`
	cmpJSON(t, "created", evs[0]["properties"], `{"sessionID":"s3","info":`+v1+`}`)
	cmpJSON(t, "updated", evs[1]["properties"], `{"sessionID":"s3","info":`+v1+`}`)
	cmpJSON(t, "deleted", evs[2]["properties"], `{"sessionID":"s3","info":{"id":"s3"}}`)
	if a, b := obj(obj(evs[3], "properties"), "info")["id"], obj(obj(evs[5], "properties"), "info")["id"]; a != "msg1" || b != "msg2" {
		t.Errorf("recent order = %v, %v; want oldest first", a, b)
	}
	c := f.one("GET /api/session/s3/message")
	if c.Query.Get("order") != "desc" || c.Query.Get("limit") != "4" {
		t.Errorf("recent query = %v", c.Query)
	}
}

func TestEventsPermissionsAndQuestions(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("permission.asked", 1, "", `{"id":"per_ev_1","sessionID":"s4","action":"shell","resources":["ls"],"save":["ls *"],"metadata":{}}`),
		fakeEv("permission.replied", 2, "", `{"sessionID":"s4","requestID":"per_ev_1","reply":"once"}`),
		fakeEv("form.created", 3, "", `{"form":{"id":"que_ev_1","sessionID":"s4","metadata":{"kind":"question"},
			"fields":[{"key":"q0","type":"string","title":"T","description":"Q?"}]}}`),
		fakeEv("form.created", 4, "", `{"form":{"id":"frm_ev_x","sessionID":"s4","metadata":{"kind":"mcp"}}}`),
		fakeEv("form.replied", 5, "", `{"sessionID":"s4","id":"que_ev_1"}`),
		fakeEv("form.cancelled", 6, "", `{"sessionID":"s4","id":"que_ev_2"}`),
	}
	evs := cmpStream(t, f.URL+"/event")
	cmpJSON(t, "events", evs, `[
		{"type":"permission.asked","properties":{"id":"per_ev_1","sessionID":"s4","permission":"bash","patterns":["ls"],"always":["ls *"],"metadata":{"command":"ls"}}},
		{"type":"permission.replied","properties":{"sessionID":"s4","requestID":"per_ev_1","reply":"once"}},
		{"type":"question.asked","properties":{"id":"que_ev_1","sessionID":"s4","questions":[{"header":"T","question":"Q?","options":[],"multiple":false,"custom":true}]}},
		{"type":"question.replied","properties":{"sessionID":"s4","requestID":"que_ev_1"}},
		{"type":"question.rejected","properties":{"sessionID":"s4","requestID":"que_ev_2"}}]`)
	// The streamed prompts are remembered for v1 replies.
	if s, _ := promptSessions.Load("per_ev_1"); s != "s4" {
		t.Errorf("permission session = %v", s)
	}
	if s, _ := promptSessions.Load("que_ev_1"); s != "s4" {
		t.Errorf("question session = %v", s)
	}
}

func TestEventsDirectoryFilter(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		fakeEv("session.execution.started", 1, "/a", `{"sessionID":"keep"}`),
		fakeEv("session.execution.started", 2, "/b", `{"sessionID":"drop"}`),
		fakeEv("session.execution.started", 3, "", `{"sessionID":"noloc"}`),
		fakeEv("session.execution.started", 4, "/a/", `{"sessionID":"clean"}`),
	}
	var got []any
	for _, e := range cmpStream(t, f.URL+"/event?directory=/a") {
		got = append(got, obj(e, "properties")["sessionID"])
	}
	if want := []any{"keep", "noloc", "clean"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions = %v, want %v", got, want)
	}

	// The x-opencode-directory header scopes the same way.
	f2 := fakeServer(t)
	f2.events = f.events
	_, b := cmpDo(t, http.MethodGet, f2.URL+"/event", map[string]string{"x-opencode-directory": "%2Fb"}, "")
	if s := string(b); !strings.Contains(s, `"drop"`) || strings.Contains(s, `"keep"`) {
		t.Errorf("header-scoped stream = %s", s)
	}
}

func TestEventsGlobal(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.events = []string{
		": heartbeat",
		fakeEv("session.execution.started", 1, "/a", `{"sessionID":"s1"}`),
		fakeEv("session.execution.started", 2, "/b", `{"sessionID":"s2"}`),
	}
	cmpJSON(t, "global", cmpStream(t, f.URL+"/global/event?directory=/a"), `[
		{"directory":"","payload":{"type":"server.heartbeat","properties":{}}},
		{"directory":"/a","payload":{"type":"session.status","properties":{"sessionID":"s1","status":{"type":"busy"}}}},
		{"directory":"/b","payload":{"type":"session.status","properties":{"sessionID":"s2","status":{"type":"busy"}}}}]`)
}

func TestEventsUpstreamErrorRelayed(t *testing.T) {
	defer SetInstalledV2(true)()
	f := fakeServer(t)
	f.json("GET /api/event", http.StatusServiceUnavailable, `{"name":"Unavailable"}`)
	status, b := cmpDo(t, http.MethodGet, f.URL+"/event", nil, "")
	if status != http.StatusServiceUnavailable || string(b) != `{"name":"Unavailable"}` {
		t.Fatalf("got %d %s", status, b)
	}
}
