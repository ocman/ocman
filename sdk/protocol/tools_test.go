package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func toolDescription(tools ...ToolDescriptor) Description {
	return Description{ID: "org.example.tools", Name: "Tools", Version: "1", Protocol: Version{Major: 1}, MaxConcurrency: 1, Scope: ScopeOwner,
		Capabilities: []Capability{ToolCapability}, RequestedGrants: []string{ToolAgentGrant, "items.write"}, Tools: tools}
}

func validTool() ToolDescriptor {
	return ToolDescriptor{Name: "items_list", Description: "List items.\nPaged.", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func TestToolDescriptionValidation(t *testing.T) {
	if err := toolDescription(validTool()).Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	mutate := func(f func(*Description)) Description {
		d := toolDescription(validTool())
		f(&d)
		return d
	}
	tool := func(f func(*ToolDescriptor)) Description {
		t := validTool()
		f(&t)
		return toolDescription(t)
	}
	many := make([]ToolDescriptor, MaxTools+1)
	for i := range many {
		many[i] = validTool()
		many[i].Name = "t" + strings.Repeat("x", i%40) + string(rune('a'+i%26))
	}
	for name, d := range map[string]Description{
		"tools without capability": mutate(func(d *Description) { d.Capabilities = nil }),
		"capability without tools": mutate(func(d *Description) { d.Tools = nil }),
		"hub scope":                mutate(func(d *Description) { d.Scope = ScopeHub }),
		"missing tool.agent":       mutate(func(d *Description) { d.RequestedGrants = []string{"items.write"} }),
		"too many tools":           mutate(func(d *Description) { d.Tools = many }),
		"duplicate names":          mutate(func(d *Description) { d.Tools = []ToolDescriptor{validTool(), validTool()} }),
		"uppercase name":           tool(func(t *ToolDescriptor) { t.Name = "Items" }),
		"long name":                tool(func(t *ToolDescriptor) { t.Name = strings.Repeat("a", 49) }),
		"reserved built-in":        tool(func(t *ToolDescriptor) { t.Name = "sessions" }),
		"reserved ocman prefix":    tool(func(t *ToolDescriptor) { t.Name = "ocman_items" }),
		"reserved plugin prefix":   tool(func(t *ToolDescriptor) { t.Name = "plugin_items" }),
		"empty description":        tool(func(t *ToolDescriptor) { t.Description = "" }),
		"control character":        tool(func(t *ToolDescriptor) { t.Description = "bad\x07" }),
		"untrimmed":                tool(func(t *ToolDescriptor) { t.Description = " padded" }),
		"long description":         tool(func(t *ToolDescriptor) { t.Description = strings.Repeat("a", MaxToolDescriptionBytes+1) }),
		"long confirmation":        tool(func(t *ToolDescriptor) { t.Confirmation = strings.Repeat("a", MaxToolStaticConfirmationBytes+1) }),
		"timeout too long":         tool(func(t *ToolDescriptor) { t.TimeoutSeconds = MaxToolTimeoutSeconds + 1 }),
		"negative timeout":         tool(func(t *ToolDescriptor) { t.TimeoutSeconds = -1 }),
		"undeclared grant":         tool(func(t *ToolDescriptor) { t.RequiredGrants = []string{"other"} }),
		"duplicate grant":          tool(func(t *ToolDescriptor) { t.RequiredGrants = []string{"items.write", "items.write"} }),
		"array root":               tool(func(t *ToolDescriptor) { t.InputSchema = json.RawMessage(`{"type":"array"}`) }),
		"missing schema":           tool(func(t *ToolDescriptor) { t.InputSchema = nil }),
		"unsupported keyword": tool(func(t *ToolDescriptor) {
			t.InputSchema = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string","pattern":"x"}}}`)
		}),
		"reserved intent": tool(func(t *ToolDescriptor) {
			t.InputSchema = json.RawMessage(`{"type":"object","properties":{"intent":{}}}`)
		}),
		"reserved confirmation": tool(func(t *ToolDescriptor) {
			t.InputSchema = json.RawMessage(`{"type":"object","properties":{"confirmation_id":{}}}`)
		}),
	} {
		if d.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestToolGrantsAndLookup(t *testing.T) {
	tool := validTool()
	tool.RequiredGrants = []string{"items.write"}
	d := toolDescription(tool)
	if !d.ToolsAllowed([]string{ToolAgentGrant}) || d.ToolsAllowed(nil) {
		t.Fatal("ToolsAllowed")
	}
	if tool.Granted([]string{ToolAgentGrant}) || !tool.Granted([]string{ToolAgentGrant, "items.write"}) || tool.Granted([]string{"items.write"}) {
		t.Fatal("Granted")
	}
	if _, ok := d.Tool("items_list"); !ok {
		t.Fatal("lookup")
	}
	if _, ok := d.Tool("missing"); ok {
		t.Fatal("missing lookup")
	}
	if tool.Timeout().Seconds() != DefaultToolTimeoutSeconds {
		t.Fatal("default timeout")
	}
	tool.TimeoutSeconds = 5
	if tool.Timeout().Seconds() != 5 {
		t.Fatal("timeout")
	}
}

func TestToolArgumentValidation(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{
		"name":{"type":"string","minLength":2,"maxLength":4},
		"count":{"type":"integer","minimum":1,"maximum":10},
		"ratio":{"type":"number","minimum":0.5},
		"kind":{"enum":["a","b"]},
		"fixed":{"const":{"x":[1,2]}},
		"tags":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":2},
		"flag":{"type":"boolean"},
		"loose":{"minimum":0},
		"meta":{"type":"object","additionalProperties":{"type":"integer"}}
	},"required":["name"],"additionalProperties":false}`)
	if err := ValidateToolSchema(schema); err != nil {
		t.Fatal(err)
	}
	for args, want := range map[string]string{
		`{"name":"ab"}`: "",
		`{"name":"ab","count":3,"ratio":0.5,"kind":"b","fixed":{"x":[1,2.0]},"tags":["t"],"flag":true,"meta":{"a":1}}`: "",
		`{"name":"ab","count":2.0}`:          "",
		`[]`:                                 "arguments: must be an object",
		`{}`:                                 "arguments.name: is required",
		`{"name":"a"}`:                       "arguments.name: must be at least 2 characters",
		`{"name":"ééééé"}`:                   "arguments.name: must be at most 4 characters",
		`{"name":"ab","count":0}`:            "arguments.count: must be at least 1",
		`{"name":"ab","count":11}`:           "arguments.count: must be at most 10",
		`{"name":"ab","count":1.5}`:          "arguments.count: must be of type integer",
		`{"name":"ab","ratio":0.25}`:         "arguments.ratio: must be at least 1/2",
		`{"name":"ab","kind":"c"}`:           "arguments.kind: must be one of the declared values",
		`{"name":"ab","fixed":{"x":[1]}}`:    "arguments.fixed: must equal the declared constant",
		`{"name":"ab","tags":[]}`:            "arguments.tags: must have at least 1 items",
		`{"name":"ab","tags":["a","b","c"]}`: "arguments.tags: must have at most 2 items",
		`{"name":"ab","tags":[1]}`:           "arguments.tags[0]: must be of type string",
		`{"name":"ab","flag":"yes"}`:         "arguments.flag: must be of type boolean",
		`{"name":"ab","meta":{"a":"x"}}`:     "arguments.meta.a: must be of type integer",
		`{"name":"ab","other":1}`:            "arguments.other: is not an allowed property",
		`{"name":"ab","name":"cd"}`:          "arguments: must be valid JSON without duplicate keys",
		`{"name":null}`:                      "arguments.name: must be of type string",
		`{"name":"ab","loose":1e999999}`:     "arguments.loose: must be a number of at most 64 characters with an exponent within ±400",
		`{"name":"ab","count":1e999999999}`:  "arguments.count: must be of type integer",
		`{"name":"ab","ratio":1e999999999}`:  "arguments.ratio: must be of type number",
		`{"name":"ab","meta":{"a":1e9999}}`:  "arguments.meta.a: must be of type integer",
	} {
		err := ValidateToolArguments(schema, json.RawMessage(args))
		var argument *ToolArgumentError
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: %v", args, err)
		case want != "" && (!errors.As(err, &argument) || err.Error() != want):
			t.Errorf("%s: got %v, want %s", args, err, want)
		}
	}
	if !errors.Is(ValidateToolArguments(json.RawMessage(`{"type":"object","$ref":"#"}`), json.RawMessage(`{}`)), ErrInvalidMessage) {
		t.Fatal("unsupported schema accepted")
	}
	if err := ValidateToolArguments(json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"a":"`+strings.Repeat("x", MaxToolArgumentBytes)+`"}`)); err == nil {
		t.Fatal("oversized arguments accepted")
	}
}

func TestToolSchemaRejectsMalformedKeywords(t *testing.T) {
	deep := `{"type":"object"}`
	for range maxToolSchemaDepth + 1 {
		deep = `{"type":"object","properties":{"a":` + deep + `}}`
	}
	for _, schema := range []string{`{}`, `[]`, `{"type":"null"}`, `{"type":"object","required":["a","a"]}`, `{"type":"object","required":"a"}`,
		`{"type":"object","properties":[]}`, `{"type":"object","minItems":-1}`, `{"type":"object","minimum":"1"}`, `{"type":"object","enum":[]}`,
		`{"type":"object","writeOnly":"yes"}`, `{"type":"object","examples":{}}`, `{"type":"object","title":1}`, `{"type":"object","items":true}`,
		`{"type":"object","additionalProperties":1}`, `{"type":"object","properties":{"":{}}}`, deep} {
		if ValidateToolSchema(json.RawMessage(schema)) == nil {
			t.Errorf("accepted %s", schema)
		}
	}
}

func TestMaskToolArguments(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"user":{"type":"string"},"password":{"type":"string","writeOnly":true},
		"accounts":{"type":"array","items":{"type":"object","properties":{"token":{"writeOnly":true}}}},
		"extra":{"type":"object","additionalProperties":{"writeOnly":true}}}}`)
	got := MaskToolArguments(schema, json.RawMessage(`{"user":"a","password":"secret","accounts":[{"token":"t1","name":"n"}],"extra":{"k":"v"}}`))
	if strings.Contains(string(got), "secret") || strings.Contains(string(got), "t1") || strings.Contains(string(got), `"v"`) || !strings.Contains(string(got), `"user":"a"`) {
		t.Fatalf("masked %s", got)
	}
	if MaskToolArguments(json.RawMessage(`{}`), json.RawMessage(`{}`)) != nil || MaskToolArguments(schema, json.RawMessage(`{`)) != nil {
		t.Fatal("invalid input masked")
	}
}

func TestToolInvocationAndResultDecoding(t *testing.T) {
	valid := `{"tool":"items_list","arguments":{},"context":{"principal":"agent","transport":"mcp","grants":["tool.agent"]}}`
	if _, err := DecodeToolInvocation([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"tool":"items_list","arguments":[],"context":{"principal":"agent","transport":"mcp","grants":[]}}`,
		`{"tool":"items_list","arguments":{},"context":{"principal":"user","transport":"mcp","grants":[]}}`,
		`{"tool":"items_list","arguments":{},"context":{"principal":"agent","transport":"mcp"}}`,
		`{"tool":"items_list","arguments":{},"context":{"principal":"agent","transport":"mcp","grants":["bad grant"]}}`,
		`{"tool":"Items","arguments":{},"context":{"principal":"agent","transport":"mcp","grants":[]}}`,
		`{"tool":"items_list","arguments":{},"context":{"principal":"agent","transport":"mcp","grants":[]},"confirmation":{"id":"","key":"","text":"x"}}`,
		`{"tool":"items_list","arguments":{},"context":{"principal":"agent","transport":"mcp","grants":[]},"unknown":1}`,
	} {
		if _, err := DecodeToolInvocation([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{"text":"ok"}`, `{"data":{"a":1}}`, `{"text":"no","isError":true}`, `{"confirmation":{"key":"delete:1","text":"Delete?"}}`} {
		if _, err := DecodeToolResult([]byte(raw)); err != nil {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"data":[1]}`, `{"data":null}`, `{"text":"x","confirmation":{"key":"k","text":"t"}}`, `{"confirmation":{"key":"bad key","text":"t"}}`,
		`{"confirmation":{"key":"k","text":""}}`, `{"text":"` + strings.Repeat("x", MaxToolTextBytes+1) + `"}`, `{"text":"x","extra":1}`} {
		if _, err := DecodeToolResult([]byte(raw)); err == nil {
			t.Errorf("accepted %.80s", raw)
		}
	}
}

func TestToolWriteOnlyPointersAndRedaction(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"a/b":{"writeOnly":true},"list":{"type":"array","items":{"type":"object","properties":{"key":{"writeOnly":true}}}}}}`)
	args := json.RawMessage(`{"a/b":"x","list":[{"key":"k0","n":1},{"n":2}]}`)
	pointers := ToolWriteOnlyPointers(schema, args)
	if strings.Join(pointers, ",") != "/a~1b,/list/0/key" {
		t.Fatalf("pointers %v", pointers)
	}
	got := string(RedactToolArguments(args, append(pointers, "/list/1/n", "/missing", "/list/9", "relative", "/list/x")))
	if got != `{"a/b":"(hidden)","list":[{"key":"(hidden)","n":1},{"n":"(hidden)"}]}` {
		t.Fatalf("redacted %s", got)
	}
	if string(RedactToolArguments(json.RawMessage(`{"a":1}`), []string{""})) != `"(hidden)"` || RedactToolArguments(json.RawMessage(`{`), nil) != nil {
		t.Fatal("whole-document or invalid redaction")
	}
	if ToolWriteOnlyPointers(json.RawMessage(`{}`), args) != nil || ToolWriteOnlyPointers(schema, json.RawMessage(`{`)) != nil {
		t.Fatal("invalid input")
	}
	for raw, ok := range map[string]bool{
		`{"confirmation":{"key":"k","text":"t","redact":["/arguments/token"]}}`: true,
		`{"confirmation":{"key":"k","text":"t","redact":["arguments"]}}`:        false,
	} {
		if _, err := DecodeToolResult([]byte(raw)); (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
