package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// dynamicTools mirrors a runtime tool set, such as approved plugin tools,
// into the shared MCP server before each list and call. Built-in tools always
// win a name collision. Handlers must still authorize every call: the mirror
// only decides what is listed and routable at that moment.
type dynamicTools struct {
	server  *mcpserver.MCPServer
	source  func(context.Context) []mcpserver.ServerTool
	builtin map[string]bool

	mu      sync.Mutex
	current map[string]bool // names this mirror registered
}

func newDynamicTools(server *mcpserver.MCPServer, builtin []mcpserver.ServerTool, source func(context.Context) []mcpserver.ServerTool) *dynamicTools {
	d := &dynamicTools{server: server, source: source, builtin: map[string]bool{}, current: map[string]bool{}}
	for _, t := range builtin {
		d.builtin[t.Tool.Name] = true
	}
	return d
}

func (d *dynamicTools) hooks() *mcpserver.Hooks {
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeListTools(func(ctx context.Context, _ any, _ *mcplib.ListToolsRequest) { d.sync(ctx) })
	hooks.AddBeforeCallTool(func(ctx context.Context, _ any, req *mcplib.CallToolRequest) {
		// Built-ins never depend on the dynamic set.
		if !d.builtin[req.Params.Name] {
			d.sync(ctx)
		}
	})
	return hooks
}

func (d *dynamicTools) sync(ctx context.Context) {
	tools := d.source(ctx)
	count := map[string]int{}
	for _, t := range tools {
		count[t.Tool.Name]++
	}
	var wanted []mcpserver.ServerTool
	names := map[string]bool{}
	for _, t := range tools {
		name := t.Tool.Name
		// Never let one tool silently replace a built-in or another provider's.
		if d.builtin[name] || count[name] > 1 || t.Handler == nil {
			continue
		}
		tool, ok := withRawIntent(t.Tool)
		if !ok {
			continue
		}
		t.Tool = tool
		wanted = append(wanted, t)
		names[name] = true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var stale []string
	for name := range d.current {
		if !names[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		d.server.DeleteTools(stale...)
	}
	// Re-register every wanted tool: an identical definition can move to a
	// different owner, and only the handler would tell them apart.
	if len(wanted) > 0 {
		d.server.AddTools(wanted...)
	}
	d.current = names
}

// withRawIntent adds the optional intent property every ocman tool accepts to
// a raw input schema, as ServerTools does for built-in tools. A dynamic tool
// must use a raw object schema; anything else is not listed.
func withRawIntent(tool mcplib.Tool) (mcplib.Tool, bool) {
	d := json.NewDecoder(bytes.NewReader(tool.RawInputSchema))
	d.UseNumber()
	var schema map[string]any
	if d.Decode(&schema) != nil || schema == nil {
		return tool, false
	}
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
	}
	if _, declared := properties["intent"]; !declared {
		properties["intent"] = map[string]any{"type": "string", "description": intentDescription}
	}
	schema["properties"] = properties
	raw, err := json.Marshal(schema)
	if err != nil {
		return tool, false
	}
	tool.RawInputSchema = raw
	return tool, true
}
