package ocv2

import (
	"reflect"
	"testing"
)

func TestV1ToolName(t *testing.T) {
	tests := map[string]string{
		"shell": "bash", "patch": "apply_patch", "subagent": "task",
		"read": "read", "edit": "edit", "": "",
	}
	for in, want := range tests {
		if got := V1ToolName(in); got != want {
			t.Errorf("V1ToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestV1Action(t *testing.T) {
	tests := map[string]string{"shell": "bash", "subagent": "task", "edit": "edit", "read": "read", "patch": "patch"}
	for in, want := range tests {
		if got := V1Action(in); got != want {
			t.Errorf("V1Action(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestV2Action(t *testing.T) {
	tests := map[string]string{
		"bash": "shell", "task": "subagent",
		"write": "edit", "patch": "edit", "apply_patch": "edit", "edit": "edit",
		"read": "read", "webfetch": "webfetch",
	}
	for in, want := range tests {
		if got := V2Action(in); got != want {
			t.Errorf("V2Action(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestV1RulesV2Rules(t *testing.T) {
	v2 := []any{
		map[string]any{"action": "shell", "resource": "git *", "effect": "allow"},
		map[string]any{"action": "subagent", "resource": "*", "effect": "ask"},
		map[string]any{"action": "read", "resource": "/etc/*", "effect": "deny"},
	}
	v1 := V1Rules(v2)
	convEqual(t, "V1Rules", v1, `[
		{"permission":"bash","pattern":"git *","action":"allow"},
		{"permission":"task","pattern":"*","action":"ask"},
		{"permission":"read","pattern":"/etc/*","action":"deny"}
	]`)

	back := make([]any, len(v1))
	for i, r := range v1 {
		back[i] = r
	}
	rt := V2Rules(back)
	for i, r := range rt {
		if !reflect.DeepEqual(r, v2[i]) {
			t.Errorf("round trip[%d] = %v, want %v", i, r, v2[i])
		}
	}

	convEqual(t, "V2Rules write", V2Rules([]any{map[string]any{"permission": "write", "pattern": "*", "action": "allow"}}),
		`[{"action":"edit","resource":"*","effect":"allow"}]`)

	if got := V1Rules(nil); got == nil || len(got) != 0 {
		t.Errorf("V1Rules(nil) = %#v, want empty non-nil", got)
	}
	if got := V2Rules(nil); got == nil || len(got) != 0 {
		t.Errorf("V2Rules(nil) = %#v, want empty non-nil", got)
	}
}

func TestV1Permission(t *testing.T) {
	tests := []struct {
		name, req, want string
	}{
		{
			"shell joins resources into command",
			`{"id":"per_1","sessionID":"ses_1","action":"shell","resources":["git status","git diff"],"save":["git *"],
			  "source":{"messageID":"msg_1","id":"call_1"}}`,
			`{"id":"per_1","sessionID":"ses_1","permission":"bash","patterns":["git status","git diff"],"always":["git *"],
			  "metadata":{"command":"git status && git diff"},"tool":{"messageID":"msg_1","callID":"call_1"}}`,
		},
		{
			"shell keeps explicit command",
			`{"id":"per_2","sessionID":"ses_1","action":"shell","resources":["ls"],"metadata":{"command":"ls -la"}}`,
			`{"id":"per_2","sessionID":"ses_1","permission":"bash","patterns":["ls"],"always":[],"metadata":{"command":"ls -la"}}`,
		},
		{
			"shell without resources has no command",
			`{"id":"per_3","sessionID":"ses_1","action":"shell"}`,
			`{"id":"per_3","sessionID":"ses_1","permission":"bash","patterns":[],"always":[],"metadata":{}}`,
		},
		{
			"edit joins file diffs",
			`{"id":"per_4","sessionID":"ses_1","action":"edit","resources":["a.go","b.go"],
			  "metadata":{"files":[{"file":"a.go","patch":"pa"},{"file":"b.go","patch":"pb"}]}}`,
			`{"id":"per_4","sessionID":"ses_1","permission":"edit","patterns":["a.go","b.go"],"always":[],
			  "metadata":{"files":[{"file":"a.go","patch":"pa"},{"file":"b.go","patch":"pb"}],"diff":"pa\npb","filepath":"a.go, b.go"}}`,
		},
		{
			"edit keeps explicit diff",
			`{"id":"per_5","sessionID":"ses_1","action":"edit","metadata":{"diff":"d","files":[{"file":"a.go","patch":"pa"}]}}`,
			`{"id":"per_5","sessionID":"ses_1","permission":"edit","patterns":[],"always":[],
			  "metadata":{"diff":"d","files":[{"file":"a.go","patch":"pa"}]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convEqual(t, "permission", V1Permission(convDecode(t, tt.req)), tt.want)
		})
	}
}

func TestV1PermissionDoesNotMutateRequest(t *testing.T) {
	req := convDecode(t, `{"action":"shell","resources":["ls"],"metadata":{}}`)
	V1Permission(req)
	if _, has := obj(req, "metadata")["command"]; has {
		t.Error("request metadata mutated")
	}
}

func TestIsQuestionForm(t *testing.T) {
	tests := []struct {
		form string
		want bool
	}{
		{`{"metadata":{"kind":"question"}}`, true},
		{`{"metadata":{"kind":"other"}}`, false},
		{`{}`, false},
	}
	for _, tt := range tests {
		if got := IsQuestionForm(convDecode(t, tt.form)); got != tt.want {
			t.Errorf("IsQuestionForm(%s) = %v, want %v", tt.form, got, tt.want)
		}
	}
}

func TestV1Question(t *testing.T) {
	form := convDecode(t, `{
		"id":"frm_1","sessionID":"ses_1",
		"metadata":{"kind":"question","tool":{"messageID":"msg_1","id":"call_1"}},
		"fields":[
			{"key":"a","title":"Pick","description":"Which one?","type":"string",
			 "options":[{"label":"X","description":"the x"},{"label":"Y"}]},
			{"key":"b","title":"Many","description":"Which ones?","type":"multiselect","custom":false},
			{"key":"c","title":"Free","description":"Say","type":"string","custom":true}
		]}`)
	convEqual(t, "question", V1Question(form), `{
		"id":"frm_1","sessionID":"ses_1","tool":{"messageID":"msg_1","callID":"call_1"},
		"questions":[
			{"header":"Pick","question":"Which one?","multiple":false,"custom":true,
			 "options":[{"label":"X","description":"the x"},{"label":"Y","description":""}]},
			{"header":"Many","question":"Which ones?","multiple":true,"custom":false,"options":[]},
			{"header":"Free","question":"Say","multiple":false,"custom":true,"options":[]}
		]}`)

	convEqual(t, "no fields/tool", V1Question(convDecode(t, `{"id":"frm_2","sessionID":"ses_1"}`)),
		`{"id":"frm_2","sessionID":"ses_1","questions":[]}`)
}

func TestFormAnswer(t *testing.T) {
	form := convDecode(t, `{"fields":[
		{"key":"a","type":"string"},
		{"key":"b","type":"multiselect"},
		{"key":"c","type":"string"},
		{"key":"d","type":"string"}
	]}`)
	got := FormAnswer(form, [][]string{{"x", "ignored"}, {"p", "q"}, {}})
	convEqual(t, "answer", got, `{"a":"x","b":["p","q"]}`)

	if got := FormAnswer(form, nil); len(got) != 0 {
		t.Errorf("FormAnswer(nil answers) = %v, want empty", got)
	}
}

// A malformed tool result (non-object content item) must not panic: the
// converter runs inside the live event translator.
func TestConvertToolToleratesNonObjectContent(t *testing.T) {
	part := ConvertContent(map[string]any{
		"type": "tool", "id": "c1", "name": "shell",
		"state": map[string]any{"status": "completed", "input": map[string]any{}, "content": []any{"oops", map[string]any{"type": "text", "text": "ok"}}},
		"time":  map[string]any{"created": 1.0},
	}, nil)
	if got := part["state"].(map[string]any)["output"]; got != "ok" {
		t.Fatalf("output = %v, want ok", got)
	}
}
