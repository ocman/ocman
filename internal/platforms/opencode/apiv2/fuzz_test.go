package ocv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// FuzzConvertMessage checks the part-id invariant every v1 consumer relies
// on: a converted message's parts are numbered PartID(id, index), unique,
// owned by the message, and the conversion is deterministic and leaves its
// input untouched. The HTTP layer, the SSE translator and the database views
// all agree on ids only because of this.
func FuzzConvertMessage(f *testing.F) {
	for _, s := range []string{
		`{"id":"msg_a","type":"user","text":"hi","time":{"created":1}}`,
		`{"id":"msg_a","type":"synthetic","text":"hi","files":[{"mime":"image/png","name":"x.png","data":"AAA"},{"mime":"text/plain","uri":"file:///x"},{"source":{"type":"uri","uri":"https://x"}}]}`,
		`{"id":"msg_b","type":"assistant","agent":"build","model":{"providerID":"openai","id":"gpt","variant":"high"},"time":{"created":1,"completed":2},"finish":"stop","cost":0.5,"tokens":{"input":1,"output":2},"snapshot":{"start":"s"},"content":[{"type":"text","text":"Hello"},{"type":"reasoning","text":"th","time":{"created":1}},{"type":"tool","id":"c1","name":"shell","state":{"status":"completed","input":{"command":"ls"},"content":[{"type":"text","text":"out"}],"metadata":{"exit":0}},"time":{"created":1,"completed":2}},{"type":"tool","id":"c2","name":"patch","state":{"status":"completed","input":{},"metadata":{"files":[{"file":"a","patch":"p","status":"added","additions":1}]}}},{"type":"tool","id":"c3","name":"edit","state":{"status":"error","input":{"path":"/a"},"error":{"message":"boom"},"metadata":{"files":[{"file":"/a","patch":"p"}]}}},{"type":"tool","id":"c4","name":"subagent","state":{"status":"running","input":{"description":"d"},"metadata":{"sessionID":"ses_c"}}},{"type":"weird"}]}`,
		`{"id":"msg_c","type":"shell","command":"ls","shellID":"sh1","status":"killed","exit":137,"output":{"output":"x"},"time":{"created":1,"completed":3}}`,
		`{"id":"msg_d","type":"compaction","status":"running","summary":"sum","error":{"type":"X","message":"m","status":500}}`,
		`{"id":"msg_e","type":"system"}`,
		`{"id":"msg_f","type":"assistant","error":{"message":"bad"},"content":"notarray","tokens":"x","model":5}`,
	} {
		f.Add("ses_1", []byte(s))
	}
	f.Fuzz(func(t *testing.T, sessionID string, raw []byte) {
		var msg map[string]any
		if json.Unmarshal(raw, &msg) != nil || msg == nil {
			return
		}
		before, _ := json.Marshal(msg)
		v1, ok := ConvertMessage(sessionID, msg)
		if after, _ := json.Marshal(msg); !bytes.Equal(before, after) {
			t.Fatalf("ConvertMessage mutated its input:\n%s\n%s", before, after)
		}
		if !ok {
			if len(v1.Parts) != 0 {
				t.Fatalf("unconverted message has %d parts", len(v1.Parts))
			}
			return
		}
		first, err := json.Marshal(v1)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		again, _ := ConvertMessage(sessionID, msg)
		if second, _ := json.Marshal(again); !bytes.Equal(first, second) {
			t.Fatalf("non-deterministic conversion:\n%s\n%s", first, second)
		}
		if want := expectedPartCount(msg); len(v1.Parts) != want {
			t.Fatalf("%d parts, want %d\n%s", len(v1.Parts), want, first)
		}
		id := str(msg, "id")
		// Only assistant messages have the synthetic step-start part 0.
		offset := 1
		if str(msg, "type") == "assistant" {
			offset = 0
		}
		seen := map[any]bool{}
		for i, p := range v1.Parts {
			if seen[p["id"]] {
				t.Fatalf("duplicate part id %v", p["id"])
			}
			seen[p["id"]] = true
			if want := PartID(id, i+offset); p["id"] != want {
				t.Fatalf("part %d id %v, want %s", i, p["id"], want)
			}
			if p["messageID"] != id || p["sessionID"] != sessionID {
				t.Fatalf("part %d owned by %v/%v, want %s/%s", i, p["sessionID"], p["messageID"], sessionID, id)
			}
		}
		if v1.Info["id"] != id || v1.Info["sessionID"] != sessionID {
			t.Fatalf("info owned by %v/%v", v1.Info["sessionID"], v1.Info["id"])
		}
	})
}

func expectedPartCount(msg map[string]any) int {
	switch str(msg, "type") {
	case "user", "synthetic":
		return 1 + len(arr(msg, "files"))
	case "assistant":
		n := 1 + len(arr(msg, "content"))
		if str(msg, "finish") != "" {
			n++
		}
		return n
	case "shell", "compaction":
		return 1
	}
	return 0
}

// translatorOps are the v2 session events FuzzTranslatorReplay sequences.
var translatorOps = []string{
	"session.step.started", "session.step.ended", "session.step.failed",
	"session.text.started", "session.text.delta", "session.text.ended",
	"session.reasoning.started", "session.reasoning.delta", "session.reasoning.ended",
	"session.tool.input.started", "session.tool.called", "session.tool.progress", "session.tool.success", "session.tool.failed",
	"session.execution.succeeded",
}

// FuzzTranslatorReplay drives the event translator with structured event
// sequences and reduces its v1 output like the frontend does. Every delta
// must target an emitted text/reasoning part of the same message, and,
// without a stored snapshot, the reduced live stream must agree with
// ConvertMessage of the translator's in-flight message: a reload must show
// what the stream showed.
//
// Each byte pair is one event: the first picks the event type, the second
// its parameters (bit 0 the message, bit 1 the tool call id, arg>>2 mod 3 the
// delta text, bit 6 a step finish, bit 7 a start time older than the
// stored snapshot). Tool call ids repeat freely, within and across messages.
func FuzzTranslatorReplay(f *testing.F) {
	f.Add([]byte{0, 0, 3, 0, 4, 1, 5, 2, 9, 0, 10, 0, 12, 0, 1, 0}, false)
	f.Add([]byte{0, 0, 3, 0, 4, 1, 2, 0, 0, 1, 3, 0, 4, 2, 14, 0}, true)
	f.Add([]byte{4, 1, 11, 0, 0, 0, 3, 0, 4, 1}, true)
	f.Add([]byte{0, 0, 9, 0, 9, 0, 10, 0}, false)
	f.Add([]byte{0, 0, 9, 0, 9, 2, 12, 2, 13, 0, 1, 0x40}, false)
	defer SetInstalledV2(true)()
	f.Fuzz(func(t *testing.T, ops []byte, withSnapshot bool) {
		if len(ops) > 400 {
			return
		}
		var snapshot []byte
		if withSnapshot {
			snapshot = []byte(`{"data":{"type":"assistant","time":{"created":150},"content":[{"type":"text","text":"Hi"},{"type":"tool","id":"call1","name":"shell","state":{"status":"running","input":{}}}]}}`)
		}
		checkTranslator(t, translatorEvents(ops), snapshot)
	})
}

func translatorEvents(ops []byte) string {
	var b strings.Builder
	ts := 100.0
	for k := 0; k+1 < len(ops); k += 2 {
		op, arg := translatorOps[int(ops[k])%len(translatorOps)], ops[k+1]
		mid := []string{"msg01", "msg02"}[arg&1]
		call := []string{"call1", "call2"}[(arg>>1)&1]
		delta := []string{"a", "bc", ""}[int(arg>>2)%3]
		ts++
		started := ts
		if arg&0x80 != 0 {
			started = 100 // a buffered event older than the snapshot
		}
		d := fmt.Sprintf(`"sessionID":"s1","assistantMessageID":%q`, mid)
		var data string
		switch op {
		case "session.step.started":
			data = fmt.Sprintf(`{%s,"agent":"build","started":%v}`, d, started)
		case "session.step.ended", "session.step.failed":
			finish := ""
			if arg&0x40 != 0 {
				finish = `,"finish":"stop"`
			}
			data = fmt.Sprintf(`{%s%s,"tokens":{"input":1,"output":2}}`, d, finish)
		case "session.text.delta", "session.reasoning.delta":
			data = fmt.Sprintf(`{%s,"delta":%q}`, d, delta)
		case "session.text.ended", "session.reasoning.ended":
			data = fmt.Sprintf(`{%s,"text":%q}`, d, delta+"!")
		case "session.tool.input.started":
			data = fmt.Sprintf(`{%s,"id":%q,"name":"shell"}`, d, call)
		case "session.tool.called":
			data = fmt.Sprintf(`{%s,"id":%q,"input":{"command":"ls"}}`, d, call)
		case "session.tool.progress":
			data = fmt.Sprintf(`{%s,"id":%q,"metadata":{"n":%v}}`, d, call, ts)
		case "session.tool.success", "session.tool.failed":
			data = fmt.Sprintf(`{%s,"id":%q,"content":[{"type":"text","text":"o"}],"error":{"message":"e"}}`, d, call)
		case "session.execution.succeeded":
			data = `{"sessionID":"s1"}`
		default:
			data = "{" + d + "}"
		}
		b.WriteString("data: " + fakeEv(op, ts, "", data) + "\n\n")
	}
	return b.String()
}

// snapshotRT answers GET /api/session/*/message/<id> with the stored
// snapshot (renamed to the requested assistant message) and 404s the rest.
type snapshotRT struct{ snapshot []byte }

func (r snapshotRT) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusNotFound, []byte(`{}`)
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/message/") && len(r.snapshot) > 0 {
		var env map[string]any
		if json.Unmarshal(r.snapshot, &env) == nil {
			if d, ok := env["data"].(map[string]any); ok {
				d["id"] = req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
				d["type"] = "assistant"
			}
			body, _ = json.Marshal(env)
			status = http.StatusOK
		}
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func checkTranslator(t *testing.T, sse string, snapshot []byte) {
	var out bytes.Buffer
	tr := &translator{c: &compat{rt: snapshotRT{snapshot}, base: "http://x"}, ctx: context.Background(),
		out: &out, msgs: map[string]map[string]any{}, unseeded: map[string]bool{}, snapshots: map[string]bool{},
		activity: &activity{foreground: map[string]any{}, shells: map[string]string{}}}
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		_ = tr.run(strings.NewReader(sse))
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("translator panicked: %v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("translator hung on %d-byte input", len(sse))
	}

	// The client already holds the stored message's parts as history.
	parts := map[string]map[string]any{}
	if len(snapshot) > 0 {
		var env struct {
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(snapshot, &env) == nil && env.Data != nil {
			for _, mid := range []string{"msg01", "msg02"} {
				env.Data["id"], env.Data["type"] = mid, "assistant"
				if v1, ok := ConvertMessage("s1", env.Data); ok {
					for _, p := range v1.Parts {
						parts[str(p, "id")] = p
					}
				}
			}
		}
	}
	for _, frame := range strings.Split(out.String(), "\n\n") {
		if frame == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &ev); err != nil {
			t.Fatalf("bad frame %q: %v", frame, err)
		}
		props := obj(ev, "properties")
		switch str(ev, "type") {
		case "message.part.updated":
			p := obj(props, "part")
			parts[str(p, "id")] = p
		case "message.part.delta":
			p := parts[str(props, "partID")]
			if p == nil {
				t.Fatalf("delta for never-emitted part %v\n%s", props["partID"], out.String())
			}
			if p["messageID"] != props["messageID"] {
				t.Fatalf("delta messageID %v, part belongs to %v", props["messageID"], p["messageID"])
			}
			if typ := str(p, "type"); typ != "text" && typ != "reasoning" {
				t.Fatalf("delta into %s part %v", typ, props["partID"])
			}
			field := str(props, "field")
			p[field] = str(p, field) + str(props, "delta")
		}
	}
	if len(snapshot) > 0 {
		return // the stub's stored message never advances; only stream checks apply
	}
	for mid, msg := range tr.msgs {
		if tr.snapshots[mid] {
			continue
		}
		v1, ok := ConvertMessage(str(msg, "sessionID"), msg)
		if !ok {
			continue
		}
		content := arr(msg, "content")
		for i := 0; i < len(content)+1 && i < len(v1.Parts); i++ {
			want := v1.Parts[i]
			got := parts[str(want, "id")]
			if got == nil {
				t.Fatalf("history part %v (%v) never streamed\nstream:\n%s", want["id"], want["type"], out.String())
			}
			if got["type"] != want["type"] {
				t.Fatalf("part %v type stream=%v history=%v", want["id"], got["type"], want["type"])
			}
			switch str(want, "type") {
			case "text", "reasoning":
				if got["text"] != want["text"] {
					t.Fatalf("part %v text stream=%q history=%q\n%s", want["id"], got["text"], want["text"], out.String())
				}
			case "tool":
				if gs, ws := obj(got, "state")["status"], obj(want, "state")["status"]; gs != ws {
					t.Fatalf("tool %v status stream=%v history=%v\ninput:\n%s\nstream:\n%s", want["id"], gs, ws, sse, out.String())
				}
			}
		}
	}
}
