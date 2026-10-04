package ocv2

import (
	"encoding/json"
	"testing"
)

// convList decodes a JSON array literal.
func convList(t *testing.T, s string) []any {
	t.Helper()
	var a []any
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		t.Fatalf("decode fixture: %v\n%s", err, s)
	}
	return a
}

func TestV1Session(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{
			"full",
			`{"id":"ses_1","projectID":"prj","parentID":"ses_0","title":"T",
			  "location":{"directory":"/repo"},
			  "time":{"created":1,"updated":2,"archived":3},
			  "permissions":[{"action":"shell","resource":"*","effect":"allow"}],
			  "revert":{"messageID":"msg_1"}}`,
			`{"id":"ses_1","projectID":"prj","parentID":"ses_0","title":"T","directory":"/repo","version":"2",
			  "time":{"created":1,"updated":2,"archived":3},
			  "permission":[{"permission":"bash","pattern":"*","action":"allow"}],
			  "revert":{"messageID":"msg_1"}}`,
		},
		{
			"minimal",
			`{"id":"ses_2","projectID":"prj","title":"","location":{"directory":"/r"},"time":{"created":1,"updated":1}}`,
			`{"id":"ses_2","projectID":"prj","title":"","directory":"/r","version":"2",
			  "time":{"created":1,"updated":1},"permission":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convEqual(t, "session", V1Session(convDecode(t, tt.in)), tt.want)
		})
	}
}

func TestV1Agents(t *testing.T) {
	got := V1Agents(convList(t, `[
		{"id":"build","description":"Builds","mode":"primary","color":"#fff","model":{"providerID":"anthropic","id":"claude"}},
		{"id":"plan","mode":"primary"},
		{"id":"mine","mode":"subagent","hidden":true}
	]`))
	convEqual(t, "agents", got, `[
		{"name":"build","description":"Builds","mode":"primary","hidden":false,"native":true,"color":"#fff","model":"anthropic/claude"},
		{"name":"plan","description":"","mode":"primary","hidden":false,"native":true,"color":""},
		{"name":"mine","description":"","mode":"subagent","hidden":true,"native":false,"color":""}
	]`)
}

func TestV1Commands(t *testing.T) {
	got := V1Commands(convList(t, `[{"name":"review","description":"Review it"},{"name":"x"}]`))
	convEqual(t, "commands", got, `[
		{"name":"review","description":"Review it","template":"","source":"command"},
		{"name":"x","description":"","template":"","source":"command"}
	]`)
	if got := V1Commands(nil); got == nil || len(got) != 0 {
		t.Errorf("V1Commands(nil) = %#v, want empty non-nil", got)
	}
}

func TestV1Providers(t *testing.T) {
	providers := convList(t, `[{"id":"anthropic","name":"Anthropic"},{"id":"openai","name":"OpenAI"}]`)
	models := convList(t, `[
		{"providerID":"anthropic","modelID":"claude","name":"Claude","status":"active",
		 "variants":[{"id":"high"},{"id":"low"}],"limit":{"context":200000,"output":8000}},
		{"providerID":"openai","modelID":"gpt","name":"GPT","enabled":false},
		{"providerID":"local","modelID":"llama","name":"Llama","enabled":true}
	]`)
	got := V1Providers(providers, models, map[string]any{"providerID": "anthropic", "modelID": "claude"})
	convEqual(t, "providers", got, `{
		"all":[
			{"id":"anthropic","name":"Anthropic","models":{
				"claude":{"id":"claude","name":"Claude","status":"active",
				          "variants":{"high":{},"low":{}},"limit":{"context":200000,"output":8000}}}},
			{"id":"openai","name":"OpenAI","models":{}},
			{"id":"local","name":"local","models":{
				"llama":{"id":"llama","name":"Llama","status":"","variants":{},"limit":{"context":null,"output":null}}}}
		],
		"connected":["anthropic","local"],
		"default":{"anthropic":"claude"}
	}`)

	empty := V1Providers(nil, nil, nil)
	convEqual(t, "empty", empty, `{"all":[],"connected":[],"default":{}}`)
}

func TestMergeConfig(t *testing.T) {
	tests := []struct {
		name, entries, want string
	}{
		{
			"deep merge, later wins, non-documents skipped",
			`[
				{"type":"document","info":{"theme":"a","agent":{"build":{"model":"x","temperature":1}},"list":[1]}},
				{"type":"env","info":{"theme":"env"}},
				{"type":"document","info":{"theme":"b","agent":{"build":{"model":"y"}},"list":[2]}}
			]`,
			`{"theme":"b","agent":{"build":{"model":"y","temperature":1}},"list":[2]}`,
		},
		{
			"small_model from string title model",
			`[{"type":"document","info":{"agents":{"title":{"model":"anthropic/haiku"}}}}]`,
			`{"agents":{"title":{"model":"anthropic/haiku"}},"small_model":"anthropic/haiku"}`,
		},
		{
			"small_model from object title model",
			`[{"type":"document","info":{"agents":{"title":{"model":{"providerID":"anthropic","id":"haiku"}}}}}]`,
			`{"agents":{"title":{"model":{"providerID":"anthropic","id":"haiku"}}},"small_model":"anthropic/haiku"}`,
		},
		{
			"explicit small_model kept",
			`[{"type":"document","info":{"small_model":"p/m","agents":{"title":{"model":"q/n"}}}}]`,
			`{"small_model":"p/m","agents":{"title":{"model":"q/n"}}}`,
		},
		{
			"mcp servers lifted unless present",
			`[{"type":"document","info":{"mcp":{"keep":{"url":"old"},"servers":{"keep":{"url":"new"},"lift":{"url":"l"}}}}}]`,
			`{"mcp":{"keep":{"url":"old"},"lift":{"url":"l"},"servers":{"keep":{"url":"new"},"lift":{"url":"l"}}}}`,
		},
		{"nothing", `[]`, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convEqual(t, "merged", MergeConfig(convList(t, tt.entries)), tt.want)
		})
	}
}

func TestMergeConfigDoesNotMutateEntries(t *testing.T) {
	entries := convList(t, `[
		{"type":"document","info":{"agent":{"build":{"model":"x"}}}},
		{"type":"document","info":{"agent":{"build":{"model":"y"}}}}
	]`)
	MergeConfig(entries)
	first := obj(obj(obj(entries[0].(map[string]any), "info"), "agent"), "build")
	if first["model"] != "x" {
		t.Errorf("first entry mutated: model = %v", first["model"])
	}
}
