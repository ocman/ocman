package ocv2

import (
	"encoding/json"
	"reflect"
	"testing"
)

// convDecode parses a JSON object literal the way the HTTP layer would.
func convDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode fixture: %v\n%s", err, s)
	}
	return m
}

// convNorm round-trips v through JSON so Go-built maps compare equal to
// decoded fixtures (ints vs float64, typed slices vs []any).
func convNorm(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// convEqual compares got against a JSON literal after normalization.
func convEqual(t *testing.T, label string, got any, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: decode want: %v", label, err)
	}
	if g := convNorm(t, got); !reflect.DeepEqual(g, w) {
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		t.Errorf("%s:\n got  %s\n want %s", label, gb, wb)
	}
}

func TestPartID(t *testing.T) {
	tests := []struct {
		msg   string
		index int
		want  string
	}{
		{"msg_abc", 3, "prt_abc0003"},
		{"msg_abc", 0, "prt_abc0000"},
		{"msg_abc", 12, "prt_abc0012"},
	}
	for _, tt := range tests {
		if got := PartID(tt.msg, tt.index); got != tt.want {
			t.Errorf("PartID(%q, %d) = %q, want %q", tt.msg, tt.index, got, tt.want)
		}
	}
}

func TestConvertMessageUser(t *testing.T) {
	msg := convDecode(t, `{
		"id": "msg_u1", "type": "user", "text": "hello",
		"time": {"created": 100},
		"files": [
			{"name": "a.png", "mime": "image/png", "source": {"type": "uri", "uri": "file:///tmp/a.png"}},
			{"name": "b.txt", "mime": "text/plain", "data": "aGk="}
		]
	}`)
	got, ok := ConvertMessage("ses_1", msg)
	if !ok {
		t.Fatal("ok = false for user message")
	}
	convEqual(t, "info", got.Info, `{"id":"msg_u1","sessionID":"ses_1","role":"user","time":{"created":100}}`)
	convEqual(t, "parts", got.Parts, `[
		{"id":"prt_u10001","messageID":"msg_u1","sessionID":"ses_1","type":"text","text":"hello","synthetic":false},
		{"id":"prt_u10002","messageID":"msg_u1","sessionID":"ses_1","type":"file","mime":"image/png","filename":"a.png","url":"file:///tmp/a.png"},
		{"id":"prt_u10003","messageID":"msg_u1","sessionID":"ses_1","type":"file","mime":"text/plain","filename":"b.txt","url":"data:text/plain;base64,aGk="}
	]`)
}

func TestConvertMessageSynthetic(t *testing.T) {
	got, ok := ConvertMessage("ses_1", convDecode(t, `{"id":"msg_s","type":"synthetic","text":"auto","time":{"created":5}}`))
	if !ok {
		t.Fatal("ok = false")
	}
	if got.Info["role"] != "user" {
		t.Errorf("role = %v, want user", got.Info["role"])
	}
	if len(got.Parts) != 1 || got.Parts[0]["synthetic"] != true || got.Parts[0]["text"] != "auto" {
		t.Errorf("parts = %v", got.Parts)
	}
}

func TestFileURLTopLevelURI(t *testing.T) {
	if got := fileURL(map[string]any{"uri": "https://x/y"}); got != "https://x/y" {
		t.Errorf("fileURL = %q", got)
	}
}

const convAssistantFixture = `{
	"id": "msg_a1", "type": "assistant", "agent": "build",
	"model": {"id": "claude", "providerID": "anthropic", "variant": "high"},
	"cost": 0.5,
	"tokens": {"input": 10, "output": 20, "reasoning": 1, "cache": {"read": 2, "write": 3}},
	"finish": "stop",
	"time": {"created": 1000, "completed": 2000},
	"snapshot": {"start": "snap1"},
	"error": {"type": "APIError", "message": "boom", "status": 500},
	"content": [
		{"type": "text", "text": "hi"},
		{"type": "reasoning", "text": "think", "time": {"created": 1100, "completed": 1200}},
		{"type": "tool", "id": "call_sh", "name": "shell", "time": {"created": 1300, "completed": 1400},
		 "state": {"status": "completed", "input": {"command": "ls", "description": "list"},
		           "content": [{"type": "text", "text": "a"}, {"type": "text", "text": "b"}], "metadata": {"exit": 0}}},
		{"type": "tool", "id": "call_ed", "name": "edit", "time": {"created": 1500},
		 "state": {"status": "completed", "input": {"path": "/x.go"},
		           "metadata": {"files": [{"file": "/x.go", "patch": "@@ -1 +1 @@", "additions": 1, "deletions": 2}]}}},
		{"type": "tool", "id": "call_pa", "name": "patch",
		 "state": {"status": "completed", "input": {},
		           "metadata": {"files": [
		             {"file": "n.go", "status": "added", "patch": "p1", "additions": 3, "deletions": 0},
		             {"file": "d.go", "status": "deleted", "patch": "p2", "additions": 0, "deletions": 4},
		             {"file": "m.go", "status": "modified", "patch": "p3", "additions": 1, "deletions": 1}]}}},
		{"type": "tool", "id": "call_sub", "name": "subagent",
		 "state": {"status": "running", "input": {"description": "explore"}, "metadata": {"sessionID": "ses_child"}}},
		{"type": "tool", "id": "call_st", "name": "read", "state": {"status": "streaming", "input": "{\"pa"}},
		{"type": "tool", "id": "call_er", "name": "grep",
		 "state": {"status": "error", "input": {"pattern": "foo", "path": "/src"}, "error": {"message": "bad regex"}}}
	]
}`

func TestConvertMessageAssistant(t *testing.T) {
	got, ok := ConvertMessage("ses_1", convDecode(t, convAssistantFixture))
	if !ok {
		t.Fatal("ok = false")
	}
	convEqual(t, "info", got.Info, `{
		"id":"msg_a1","sessionID":"ses_1","role":"assistant",
		"agent":"build","mode":"build","providerID":"anthropic","modelID":"claude","variant":"high",
		"time":{"created":1000,"completed":2000},
		"cost":0.5,"tokens":{"input":10,"output":20,"reasoning":1,"cache":{"read":2,"write":3}},
		"finish":"stop",
		"error":{"name":"APIError","data":{"message":"boom","statusCode":500}}
	}`)

	if n := len(got.Parts); n != 10 {
		t.Fatalf("len(parts) = %d, want 10", n)
	}
	wantTypes := []string{"step-start", "text", "reasoning", "tool", "tool", "tool", "tool", "tool", "tool", "step-finish"}
	for i, p := range got.Parts {
		if p["type"] != wantTypes[i] {
			t.Errorf("part[%d].type = %v, want %s", i, p["type"], wantTypes[i])
		}
		if p["id"] != PartID("msg_a1", i) {
			t.Errorf("part[%d].id = %v, want %s", i, p["id"], PartID("msg_a1", i))
		}
		if p["messageID"] != "msg_a1" || p["sessionID"] != "ses_1" {
			t.Errorf("part[%d] ids = %v/%v", i, p["messageID"], p["sessionID"])
		}
	}

	strip := func(p map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range p {
			if k != "id" && k != "messageID" && k != "sessionID" {
				out[k] = v
			}
		}
		return out
	}
	cases := []struct {
		name string
		idx  int
		want string
	}{
		{"step-start", 0, `{"type":"step-start","snapshot":"snap1"}`},
		{"text", 1, `{"type":"text","text":"hi","time":{"start":1000,"end":2000}}`},
		{"reasoning", 2, `{"type":"reasoning","text":"think","time":{"start":1100,"end":1200}}`},
		{"shell", 3, `{"type":"tool","tool":"bash","callID":"call_sh","state":{
			"status":"completed","input":{"command":"ls","description":"list"},"title":"list",
			"output":"a\nb","metadata":{"exit":0,"output":"a\nb"},"time":{"start":1300,"end":1400}}}`},
		{"edit", 4, `{"type":"tool","tool":"edit","callID":"call_ed","state":{
			"status":"completed","input":{"path":"/x.go","filePath":"/x.go"},"title":"/x.go","output":"",
			"metadata":{"files":[{"file":"/x.go","patch":"@@ -1 +1 @@","additions":1,"deletions":2}],
			  "filediff":{"file":"/x.go","patch":"@@ -1 +1 @@","additions":1,"deletions":2},
			  "diff":"@@ -1 +1 @@","filepath":"/x.go"},
			"time":{"start":1500}}}`},
		{"apply_patch", 5, `{"type":"tool","tool":"apply_patch","callID":"call_pa","state":{
			"status":"completed","input":{},"title":"","output":"","time":{"start":null},
			"metadata":{
			  "files":[
			    {"filePath":"n.go","relativePath":"n.go","type":"add","patch":"p1","additions":3,"deletions":0},
			    {"filePath":"d.go","relativePath":"d.go","type":"delete","patch":"p2","additions":0,"deletions":4},
			    {"filePath":"m.go","relativePath":"m.go","type":"update","patch":"p3","additions":1,"deletions":1}]}}}`},
		{"task", 6, `{"type":"tool","tool":"task","callID":"call_sub","state":{
			"status":"running","input":{"description":"explore"},"title":"explore",
			"metadata":{"sessionID":"ses_child","sessionId":"ses_child"},"time":{"start":null}}}`},
		{"streaming", 7, `{"type":"tool","tool":"read","callID":"call_st","state":{
			"status":"pending","input":{},"title":"","metadata":{},"time":{"start":null}}}`},
		{"error", 8, `{"type":"tool","tool":"grep","callID":"call_er","state":{
			"status":"error","input":{"pattern":"foo","path":"/src"},"title":"foo","output":"",
			"error":"bad regex","metadata":{},"time":{"start":null}}}`},
		{"step-finish", 9, `{"type":"step-finish","reason":"stop","cost":0.5,
			"tokens":{"input":10,"output":20,"reasoning":1,"cache":{"read":2,"write":3}}}`},
	}
	for _, tc := range cases {
		convEqual(t, tc.name, strip(got.Parts[tc.idx]), tc.want)
	}
}

func TestConvertMessageAssistantUnfinished(t *testing.T) {
	got, ok := ConvertMessage("ses_1", convDecode(t, `{
		"id":"msg_a2","type":"assistant","agent":"plan","model":{"id":"m","providerID":"p"},
		"time":{"created":1},"content":[{"type":"text","text":"x"}]}`))
	if !ok {
		t.Fatal("ok = false")
	}
	if _, has := got.Info["finish"]; has {
		t.Error("finish set without v2 finish")
	}
	if _, has := got.Info["variant"]; has {
		t.Error("variant set without model variant")
	}
	if _, has := got.Info["error"]; has {
		t.Error("error set without v2 error")
	}
	if _, has := got.Info["time"].(map[string]any)["completed"]; has {
		t.Error("time.completed present while unset")
	}
	convEqual(t, "tokens", got.Info["tokens"], `{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`)
	if got.Info["cost"] != 0.0 {
		t.Errorf("cost = %v, want 0", got.Info["cost"])
	}
	if n := len(got.Parts); n != 2 {
		t.Fatalf("len(parts) = %d, want 2 (step-start + text)", n)
	}
	if got.Parts[1]["type"] == "step-finish" {
		t.Error("step-finish emitted without finish")
	}
	if _, has := got.Parts[1]["time"].(map[string]any)["end"]; has {
		t.Error("text time.end present while unset")
	}
}

func TestConvertContentUnknown(t *testing.T) {
	convEqual(t, "unknown", ConvertContent(map[string]any{"type": "image"}, nil), `{"type":"image"}`)
}

func TestConvertToolInputAndTitle(t *testing.T) {
	tests := []struct {
		name, tool, input string
		wantTool          string
		wantFilePath      any // nil = absent
		wantTitle         string
	}{
		{"read copies path", "read", `{"path":"/r"}`, "read", "/r", "/r"},
		{"write copies path", "write", `{"path":"/w"}`, "write", "/w", "/w"},
		{"edit keeps explicit filePath", "edit", `{"path":"/p","filePath":"/f"}`, "edit", "/f", "/f"},
		{"glob keeps path only", "glob", `{"path":"/g","pattern":"*.go"}`, "glob", nil, "*.go"},
		{"grep keeps path only", "grep", `{"path":"/g"}`, "grep", nil, ""},
		{"webfetch url title", "webfetch", `{"url":"https://x"}`, "webfetch", nil, "https://x"},
		{"description wins", "shell", `{"description":"d","command":"c"}`, "bash", nil, "d"},
		{"command title", "shell", `{"command":"c"}`, "bash", nil, "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := convDecode(t, `{"type":"tool","id":"c","name":"`+tt.tool+`","state":{"status":"running","input":`+tt.input+`}}`)
			got := ConvertContent(item, nil)
			if got["tool"] != tt.wantTool {
				t.Errorf("tool = %v, want %s", got["tool"], tt.wantTool)
			}
			state := got["state"].(map[string]any)
			in := state["input"].(map[string]any)
			fp, has := in["filePath"]
			if tt.wantFilePath == nil && has {
				t.Errorf("filePath = %v, want absent", fp)
			}
			if tt.wantFilePath != nil && fp != tt.wantFilePath {
				t.Errorf("filePath = %v, want %v", fp, tt.wantFilePath)
			}
			if state["title"] != tt.wantTitle {
				t.Errorf("title = %v, want %q", state["title"], tt.wantTitle)
			}
			if _, has := state["output"]; has {
				t.Error("output set for running tool")
			}
		})
	}
}

func TestConvertToolDoesNotMutateInput(t *testing.T) {
	item := convDecode(t, `{"type":"tool","name":"read","state":{"status":"running","input":{"path":"/r"}}}`)
	ConvertContent(item, nil)
	if _, has := obj(obj(item, "state"), "input")["filePath"]; has {
		t.Error("v2 input mutated")
	}
}

func TestConvertToolEditWithoutFiles(t *testing.T) {
	got := ConvertContent(convDecode(t, `{"type":"tool","name":"write","state":{"status":"completed","input":{}}}`), nil)
	md := got["state"].(map[string]any)["metadata"].(map[string]any)
	for _, k := range []string{"filediff", "diff", "filepath"} {
		if _, has := md[k]; has {
			t.Errorf("metadata.%s set without files", k)
		}
	}
}

func TestConvertMessageShell(t *testing.T) {
	tests := []struct {
		name, status string
		completed    string
		wantStatus   string
		wantError    bool
	}{
		{"running", "running", "", "running", false},
		{"exited", "exited", `,"completed":20`, "completed", false},
		{"killed", "killed", `,"completed":20`, "error", true},
		{"timeout", "timeout", `,"completed":20`, "error", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ConvertMessage("ses_1", convDecode(t, `{
				"id":"msg_sh","type":"shell","shellID":"sh_1","command":"make test","status":"`+tt.status+`",
				"exit":2,"output":{"output":"out text"},"time":{"created":10`+tt.completed+`}}`))
			if !ok {
				t.Fatal("ok = false")
			}
			if got.Info["role"] != "assistant" || got.Info["agent"] != "build" || got.Info["mode"] != "build" {
				t.Errorf("info = %v", got.Info)
			}
			if len(got.Parts) != 1 {
				t.Fatalf("len(parts) = %d, want 1 (no step-start)", len(got.Parts))
			}
			p := got.Parts[0]
			if p["type"] != "tool" || p["tool"] != "bash" || p["callID"] != "sh_1" || p["id"] != PartID("msg_sh", 1) {
				t.Errorf("part = %v", p)
			}
			state := p["state"].(map[string]any)
			if state["status"] != tt.wantStatus {
				t.Errorf("status = %v, want %s", state["status"], tt.wantStatus)
			}
			if state["output"] != "out text" || state["title"] != "make test" {
				t.Errorf("output/title = %v/%v", state["output"], state["title"])
			}
			md := state["metadata"].(map[string]any)
			if md["exit"] != 2.0 || md["output"] != "out text" {
				t.Errorf("metadata = %v", md)
			}
			if e, has := state["error"]; has != tt.wantError || (has && e != tt.status) {
				t.Errorf("error = %v (has %v), want %v", e, has, tt.wantError)
			}
			_, hasEnd := state["time"].(map[string]any)["end"]
			_, hasCompleted := got.Info["time"].(map[string]any)["completed"]
			if want := tt.completed != ""; hasEnd != want || hasCompleted != want {
				t.Errorf("time end/completed present = %v/%v, want %v", hasEnd, hasCompleted, want)
			}
		})
	}
}

func TestConvertMessageCompaction(t *testing.T) {
	tests := []struct {
		name, fixture string
		wantFinish    bool
		wantError     bool
	}{
		{"completed", `{"id":"msg_c","type":"compaction","status":"completed","summary":"sum","cost":1,"tokens":{"input":1},"time":{"created":1}}`, true, false},
		{"running", `{"id":"msg_c","type":"compaction","status":"running","summary":"","time":{"created":1}}`, false, false},
		{"failed", `{"id":"msg_c","type":"compaction","status":"failed","error":{"message":"nope"},"time":{"created":1}}`, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := convDecode(t, tt.fixture)
			got, ok := ConvertMessage("ses_1", msg)
			if !ok {
				t.Fatal("ok = false")
			}
			if got.Info["role"] != "assistant" || got.Info["summary"] != true || got.Info["mode"] != "compaction" {
				t.Errorf("info = %v", got.Info)
			}
			if f, has := got.Info["finish"]; has != tt.wantFinish || (has && f != "stop") {
				t.Errorf("finish = %v (has %v)", f, has)
			}
			if _, has := got.Info["error"]; has != tt.wantError {
				t.Errorf("error present = %v, want %v", has, tt.wantError)
			}
			if tt.wantError {
				convEqual(t, "error", got.Info["error"], `{"name":"UnknownError","data":{"message":"nope"}}`)
			}
			if len(got.Parts) != 1 || got.Parts[0]["type"] != "text" || got.Parts[0]["text"] != str(msg, "summary") {
				t.Errorf("parts = %v", got.Parts)
			}
		})
	}
}

func TestConvertMessageHidden(t *testing.T) {
	for _, typ := range []string{"system", "idle", "agent-switched", "model-switched", "location-switched", "skill", ""} {
		if _, ok := ConvertMessage("ses_1", map[string]any{"id": "msg_x", "type": typ}); ok {
			t.Errorf("type %q: ok = true, want false", typ)
		}
	}
}
