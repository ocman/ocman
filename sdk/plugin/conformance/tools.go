package conformance

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/sdk/plugin"
	"github.com/ocman/ocman/sdk/protocol"
)

// RunTools checks a tool.v1 plugin: its declarations, one supplied
// successful invocation whose result must be a valid ToolResult without
// chunks, and the required refusals for unknown tools and malformed params.
// The invocation's arguments must satisfy the tool's declared schema.
func RunTools(t *testing.T, executable string, inv plugin.ToolInvocation) {
	t.Helper()
	c, d := start(t, executable, plugin.ModeServe, strings.Repeat("7c", 32))
	if d.Scope != plugin.ScopeOwner || !slices.Contains(d.RequestedGrants, plugin.ToolAgentGrant) || len(d.Tools) == 0 {
		t.Fatal("tool plugins must be owner-scoped, request tool.agent and declare tools")
	}
	tool, ok := d.Tool(inv.Tool)
	if !ok {
		t.Fatalf("tool %q is not declared", inv.Tool)
	}
	if err := protocol.ValidateToolArguments(tool.InputSchema, inv.Arguments); err != nil {
		t.Fatalf("supplied arguments do not satisfy %s's schema: %v", inv.Tool, err)
	}
	if inv.Context.Principal == "" {
		inv.Context = plugin.ToolContext{Principal: protocol.ToolPrincipalAgent, Transport: protocol.ToolTransportMCP, Grants: slices.Clone(d.RequestedGrants)}
	}
	c.ready()
	invoke := func(params any) plugin.Result {
		t.Helper()
		raw, _ := json.Marshal(params)
		id := c.call(plugin.Call{Capability: plugin.ToolCapability.Name, Version: plugin.ToolCapability.Version, Method: plugin.ToolInvokeMethod, Params: raw}, time.Now().Add(tool.Timeout()))
		for {
			e, err := c.decoder.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if err := c.stream.Accept(plugin.FromPlugin, e); err != nil {
				t.Fatal(err)
			}
			if e.Chunk != nil && e.Chunk.ID == id {
				t.Fatal("tool invoke is unary; chunks are a protocol error")
			}
			if e.Result != nil && e.Result.ID == id {
				return *e.Result
			}
		}
	}
	r := invoke(inv)
	if r.Error != nil {
		t.Fatalf("invoke %s: %+v", inv.Tool, r.Error)
	}
	if _, err := protocol.DecodeToolResult(r.Value); err != nil {
		t.Fatalf("invoke %s returned an invalid ToolResult: %s", inv.Tool, r.Value)
	}
	unknown := inv
	unknown.Tool = "conformance_unknown"
	if r := invoke(unknown); r.Error == nil || r.Error.Category != plugin.ErrorNotFound {
		t.Fatalf("unknown tool: want not_found, got %+v", r)
	}
	if r := invoke(map[string]any{"tool": inv.Tool, "arguments": inv.Arguments}); r.Error == nil || r.Error.Category != plugin.ErrorInvalidArgument {
		t.Fatalf("missing context: want invalid_argument, got %+v", r)
	}
	c.send(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}, false)
	c.exit()
}
