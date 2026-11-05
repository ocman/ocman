package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func toolCall(tool, args string, grants []string, confirmation *ToolConfirmed) Call {
	inv := ToolInvocation{Tool: tool, Arguments: json.RawMessage(args), Context: ToolContext{Principal: "agent", Transport: "mcp", Grants: grants}, Confirmation: confirmation}
	raw, _ := json.Marshal(inv)
	return Call{ID: 1, OperationID: "tool:op", Capability: "tool", Version: ToolCapability.Version, Method: ToolInvokeMethod, Params: raw}
}

func decodeResult(t *testing.T, raw json.RawMessage, err error) ToolResult {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var r ToolResult
	if json.Unmarshal(raw, &r) != nil {
		t.Fatalf("result %s", raw)
	}
	return r
}

func TestToolHandlerRoutesAndValidates(t *testing.T) {
	h := ToolHandler(map[string]ToolFunc{
		"echo": func(_ context.Context, op string, inv ToolInvocation) (ToolResult, error) {
			return ToolData(op, map[string]json.RawMessage{"args": inv.Arguments})
		},
		"bad": func(context.Context, string, ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		"err": func(context.Context, string, ToolInvocation) (ToolResult, error) {
			return ToolResult{}, Failure(ErrorConflict)
		},
	})
	raw, err := h(context.Background(), toolCall("echo", `{"a":1}`, []string{}, nil), nil)
	r := decodeResult(t, raw, err)
	if r.Text != "tool:op" || !strings.Contains(string(r.Data), `"a":1`) {
		t.Fatalf("echo %+v", r)
	}
	if _, err := h(context.Background(), toolCall("missing", `{}`, []string{}, nil), nil); !errors.Is(err, Failure(ErrorNotFound)) {
		t.Fatalf("unknown tool %v", err)
	}
	if _, err := h(context.Background(), toolCall("bad", `{}`, []string{}, nil), nil); err == nil || errors.As(err, new(Failure)) {
		t.Fatalf("invalid result %v", err)
	}
	if _, err := h(context.Background(), toolCall("err", `{}`, []string{}, nil), nil); !errors.Is(err, Failure(ErrorConflict)) {
		t.Fatalf("failure %v", err)
	}
	other := toolCall("echo", `{}`, []string{}, nil)
	other.Capability = "action"
	if _, err := h(context.Background(), other, nil); !errors.Is(err, Failure(ErrorNotFound)) {
		t.Fatalf("other capability %v", err)
	}
	malformed := toolCall("echo", `[]`, []string{}, nil)
	if _, err := h(context.Background(), malformed, nil); !errors.Is(err, Failure(ErrorInvalidArgument)) {
		t.Fatalf("malformed %v", err)
	}
	if ToolText("x").Text != "x" || !ToolError("y").IsError {
		t.Fatal("helpers")
	}
	if _, err := ToolData("", func() {}); err == nil {
		t.Fatal("unmarshalable data")
	}
}

func operationTool(t *testing.T) Handler {
	t.Helper()
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)
	var ran []string
	descriptor, run, err := NewOperationTool("things", "Manage things.", []ToolOperation{
		{Name: "things.get", Summary: "Read a thing", InputSchema: schema, Examples: []json.RawMessage{json.RawMessage(`{"id":"a"}`)},
			Run: func(_ context.Context, c ToolOperationCall) (ToolResult, error) {
				ran = append(ran, c.Operation)
				return ToolText("got " + string(c.Arguments)), nil
			}},
		{Name: "things.delete", Summary: "Delete a thing", InputSchema: schema, Mutates: true, RequiredGrants: []string{"things.write"},
			Confirm: func(_ context.Context, c ToolOperationCall) (*ToolConfirmationRequest, error) {
				var args struct{ ID string }
				_ = json.Unmarshal(c.Arguments, &args)
				return &ToolConfirmationRequest{Key: "delete:" + args.ID, Text: "Delete " + args.ID + "?"}, nil
			},
			Run: func(_ context.Context, c ToolOperationCall) (ToolResult, error) {
				return ToolText("deleted with " + c.Confirmation.ID), nil
			}},
	})
	if err != nil || descriptor.Name != "things" || descriptor.Description != "Manage things." {
		t.Fatalf("descriptor %+v %v", descriptor, err)
	}
	return ToolHandler(map[string]ToolFunc{"things": run})
}

func TestOperationToolHelpDispatchAndConfirmation(t *testing.T) {
	h := operationTool(t)
	call := func(args string, grants []string, confirmation *ToolConfirmed) ToolResult {
		t.Helper()
		raw, err := h(context.Background(), toolCall("things", args, grants, confirmation), nil)
		return decodeResult(t, raw, err)
	}
	reader := []string{"tool.agent"}
	writer := []string{"tool.agent", "things.write"}
	if r := call(`{"operation":"help"}`, reader, nil); !strings.Contains(string(r.Data), "things.get") || strings.Contains(string(r.Data), "things.delete") {
		t.Fatalf("reader help %s", r.Data)
	}
	if r := call(`{"operation":"help"}`, writer, nil); !strings.Contains(string(r.Data), `"name":"things.delete","summary":"Delete a thing","mutates":true,"confirmation":"may-require-user"`) {
		t.Fatalf("writer help %s", r.Data)
	}
	if r := call(`{"operation":"help","arguments":{"operation":"things.get"}}`, reader, nil); !strings.Contains(string(r.Data), `"inputSchema"`) || !strings.Contains(string(r.Data), `"examples":[{"id":"a"}]`) {
		t.Fatalf("detail %s", r.Data)
	}
	for args, want := range map[string]string{
		`{"operation":"help","arguments":{"operation":"things.delete"}}`: "unknown operation things.delete",
		`{"operation":"help","arguments":{"other":1}}`:                   "arguments.other: is not an allowed property",
		`{"operation":"things.delete","arguments":{"id":"a"}}`:           "unknown operation things.delete",
		`{"operation":"nope"}`:                                           "unknown operation nope",
		`{"operation":"things.get"}`:                                     "arguments.id: is required",
		`{"operation":"things.get","arguments":{"id":1}}`:                "arguments.id: must be of type string",
	} {
		if r := call(args, reader, nil); !r.IsError || !strings.Contains(r.Text, want) {
			t.Errorf("%s: %+v", args, r)
		}
	}
	if r := call(`{"operation":"things.get","arguments":{"id":"a"}}`, reader, nil); r.Text != `got {"id":"a"}` {
		t.Fatalf("get %+v", r)
	}
	r := call(`{"operation":"things.delete","arguments":{"id":"a"}}`, writer, nil)
	if r.Confirmation == nil || r.Confirmation.Key != "delete:a" {
		t.Fatalf("needs confirmation %+v", r)
	}
	if r := call(`{"operation":"things.delete","arguments":{"id":"a"}}`, writer, &ToolConfirmed{ID: "tc_1", Key: "delete:b", Text: "Delete b?"}); r.Confirmation == nil {
		t.Fatalf("mismatched key ran %+v", r)
	}
	if r := call(`{"operation":"things.delete","arguments":{"id":"a"}}`, writer, &ToolConfirmed{ID: "tc_1", Key: "delete:a", Text: "Delete a?"}); r.Text != "deleted with tc_1" {
		t.Fatalf("confirmed %+v", r)
	}
}

func TestNewOperationToolRejectsInvalidOperations(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	run := func(context.Context, ToolOperationCall) (ToolResult, error) { return ToolText("x"), nil }
	for name, ops := range map[string][]ToolOperation{
		"help name":      {{Name: "help", Summary: "x", InputSchema: schema, Run: run}},
		"bad name":       {{Name: "Bad", Summary: "x", InputSchema: schema, Run: run}},
		"duplicate":      {{Name: "a", Summary: "x", InputSchema: schema, Run: run}, {Name: "a", Summary: "x", InputSchema: schema, Run: run}},
		"no summary":     {{Name: "a", InputSchema: schema, Run: run}},
		"no run":         {{Name: "a", Summary: "x", InputSchema: schema}},
		"bad schema":     {{Name: "a", Summary: "x", InputSchema: json.RawMessage(`{"type":"string"}`), Run: run}},
		"invalid sample": {{Name: "a", Summary: "x", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), Examples: []json.RawMessage{json.RawMessage(`{"b":1}`)}, Run: run}},
	} {
		if _, _, err := NewOperationTool("things", "Manage things.", ops); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
