package codexappserver

import (
	"encoding/json"
	"testing"
)

func TestUserInputEncoding(t *testing.T) {
	cases := []struct {
		in   UserInput
		want string
	}{
		{TextInput("hi"), `{"type":"text","text":"hi","text_elements":[]}`},
		{UserInput{Type: "text", Text: "x", TextElements: []json.RawMessage{json.RawMessage(`{"placeholder":null}`)}},
			`{"type":"text","text":"x","text_elements":[{"placeholder":null}]}`},
		{UserInput{Type: "localImage", Path: "/a.png"}, `{"type":"localImage","path":"/a.png"}`},
		{UserInput{Type: "image", URL: "data:image/png;base64,AA"}, `{"type":"image","url":"data:image/png;base64,AA"}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.in)
		if err != nil || string(got) != tc.want {
			t.Errorf("Marshal(%+v) = %s, %v; want %s", tc.in, got, err, tc.want)
		}
	}
}

func TestItemDecoding(t *testing.T) {
	raw := `[
	{"type":"userMessage","id":"u1","content":[{"type":"text","text":"hello","text_elements":[]}],"startedAtMs":1,"completedAtMs":2},
	{"type":"reasoning","id":"r1","summary":["plan"],"content":["raw thought"]},
	{"type":"commandExecution","id":"c1","command":"ls","cwd":"/r","status":"completed","aggregatedOutput":"a\n","exitCode":0,"durationMs":12,"commandActions":[]},
	{"type":"fileChange","id":"f1","status":"completed","changes":[{"path":"a.go","kind":{"type":"update","move_path":"b.go"},"diff":"@@"}]},
	{"type":"mcpToolCall","id":"m1","server":"ocman","tool":"inbox","status":"failed","arguments":{"a":1},"result":null,"error":{"message":"boom"}},
	{"type":"futureThing","id":"z","novel":true}
	]`
	var items []Item
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatal(err)
	}
	if u := items[0]; u.Content[0].Text != "hello" || u.StartedAtMs != 1 || u.CompletedAtMs != 2 {
		t.Errorf("userMessage = %+v", u)
	}
	if r := items[1]; r.Summary[0] != "plan" || len(r.ReasoningContent) != 1 || r.ReasoningContent[0] != "raw thought" || r.Content != nil {
		t.Errorf("reasoning = %+v", r)
	}
	if c := items[2]; c.Command != "ls" || *c.AggregatedOutput != "a\n" || *c.ExitCode != 0 || *c.DurationMs != 12 {
		t.Errorf("commandExecution = %+v", c)
	}
	if f := items[3]; f.Changes[0].Kind.Type != "update" || f.Changes[0].Kind.MovePath != "b.go" {
		t.Errorf("fileChange = %+v", f)
	}
	if m := items[4]; m.ToolError.Message != "boom" || string(m.Arguments) != `{"a":1}` {
		t.Errorf("mcpToolCall = %+v", m)
	}
	if z := items[5]; z.Type != "futureThing" || string(z.Raw) != `{"type":"futureThing","id":"z","novel":true}` {
		t.Errorf("unknown item = %+v raw %s", z, z.Raw)
	}
	for _, bad := range []string{`[1]`, `{"type":"reasoning","content":[1]}`, `{"type":"userMessage","content":"x"}`} {
		var it Item
		if json.Unmarshal([]byte(bad), &it) == nil {
			t.Errorf("Unmarshal(%s) succeeded", bad)
		}
	}
}

func TestSessionSource(t *testing.T) {
	for raw, kind := range map[string]string{`"cli"`: "cli", `{"subAgent":{"parent":"x"}}`: "subAgent", `{"custom":"ci"}`: "custom"} {
		var s SessionSource
		if err := json.Unmarshal([]byte(raw), &s); err != nil || s.Kind != kind {
			t.Errorf("Unmarshal(%s) = %+v, %v", raw, s, err)
		}
		if out, err := json.Marshal(s); err != nil || string(out) != raw {
			t.Errorf("Marshal round trip = %s, %v", out, err)
		}
	}
	var s SessionSource
	if json.Unmarshal([]byte(`3`), &s) == nil {
		t.Error("numeric source decoded")
	}
	if out, _ := json.Marshal(SessionSource{Kind: "exec"}); string(out) != `"exec"` {
		t.Errorf("Marshal(kind only) = %s", out)
	}
}
