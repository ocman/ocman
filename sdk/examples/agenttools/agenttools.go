// Package agenttools holds two deterministic tool.v1 example workflows with
// in-memory data: a notes plugin declaring several independent tools, and an
// inventory plugin declaring one operation tool with a built-in help
// operation. Neither touches the network or the filesystem.
package agenttools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ocman/ocman/sdk/plugin"
)

// Plugin is one runnable example: its description and call handler.
type Plugin struct {
	Description plugin.Description
	Handler     plugin.Handler
}

func boolPtr(b bool) *bool { return &b }

func description(id, name string, grants []string, tools []plugin.ToolDescriptor) plugin.Description {
	return plugin.Description{ID: id, Name: name, Version: "1.0.0", Protocol: plugin.Version{Major: 1}, Scope: plugin.ScopeOwner,
		MaxConcurrency: 4, Capabilities: []plugin.Capability{plugin.ToolCapability}, RequestedGrants: grants, Tools: tools}
}

type note struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Notes declares notes_search, notes_create and notes_delete. Creating and
// deleting need the plugin-defined notes.write grant; deleting also needs
// the user's confirmation every time.
func Notes(id string) Plugin {
	var mu sync.Mutex
	notes := []note{{ID: "n1", Title: "Release plan", Body: "Ship on Friday"}, {ID: "n2", Title: "Retro", Body: "Keep the demo short"}}
	next := 3
	tools := []plugin.ToolDescriptor{
		{Name: "notes_search", Title: "Search notes", Description: "Search saved notes by title or body text. Returns at most limit matches.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":200},"limit":{"type":"integer","minimum":1,"maximum":50}},"required":["query"],"additionalProperties":false}`),
			Annotations: &plugin.ToolAnnotations{ReadOnly: boolPtr(true)}},
		{Name: "notes_create", Description: "Create a note and return its ID.", RequiredGrants: []string{"notes.write"},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","minLength":1,"maxLength":100},"body":{"type":"string","maxLength":2000}},"required":["title"],"additionalProperties":false}`)},
		{Name: "notes_delete", Description: "Delete one note by ID.", RequiredGrants: []string{"notes.write"}, Confirmation: "Delete a saved note?", TimeoutSeconds: 10,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1}},"required":["id"],"additionalProperties":false}`),
			Annotations: &plugin.ToolAnnotations{Destructive: boolPtr(true)}},
	}
	d := description(id, "Notes example", []string{plugin.ToolAgentGrant, "notes.write"}, tools)
	handler := plugin.ToolHandler(map[string]plugin.ToolFunc{
		"notes_search": func(_ context.Context, _ string, inv plugin.ToolInvocation) (plugin.ToolResult, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(inv.Arguments, &args)
			if args.Limit == 0 {
				args.Limit = 10
			}
			mu.Lock()
			defer mu.Unlock()
			matches := []note{}
			for _, n := range notes {
				if len(matches) < args.Limit && (strings.Contains(strings.ToLower(n.Title+" "+n.Body), strings.ToLower(args.Query))) {
					matches = append(matches, n)
				}
			}
			return plugin.ToolData(fmt.Sprintf("%d notes matched", len(matches)), map[string]any{"notes": matches})
		},
		"notes_create": func(_ context.Context, _ string, inv plugin.ToolInvocation) (plugin.ToolResult, error) {
			var n note
			_ = json.Unmarshal(inv.Arguments, &n)
			mu.Lock()
			defer mu.Unlock()
			n.ID = fmt.Sprintf("n%d", next)
			next++
			notes = append(notes, n)
			return plugin.ToolData("created "+n.ID, map[string]any{"id": n.ID})
		},
		"notes_delete": func(_ context.Context, _ string, inv plugin.ToolInvocation) (plugin.ToolResult, error) {
			// The host withholds this call until the user approves it; a
			// plugin still refuses an unconfirmed call defensively.
			if inv.Confirmation == nil {
				return plugin.ToolError("deleting requires the user's confirmation"), nil
			}
			var args struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(inv.Arguments, &args)
			mu.Lock()
			defer mu.Unlock()
			i := slices.IndexFunc(notes, func(n note) bool { return n.ID == args.ID })
			if i < 0 {
				return plugin.ToolError("note " + args.ID + " does not exist"), nil
			}
			notes = slices.Delete(notes, i, i+1)
			return plugin.ToolText("deleted " + args.ID), nil
		},
	})
	return Plugin{Description: d, Handler: handler}
}

// Inventory declares one tool, inventory, whose operations are listed by its
// built-in help. items.adjust needs the inventory.adjust grant and asks the
// user to confirm adjustments larger than ten units.
func Inventory(id string) Plugin {
	var mu sync.Mutex
	stock := map[string]int{"bolt": 120, "nut": 80}
	itemSchema := json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string","enum":["bolt","nut"]}},"required":["sku"],"additionalProperties":false}`)
	operations := []plugin.ToolOperation{
		{Name: "items.list", Summary: "List stock levels", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Run: func(context.Context, plugin.ToolOperationCall) (plugin.ToolResult, error) {
				mu.Lock()
				defer mu.Unlock()
				return plugin.ToolData("", map[string]any{"items": stock})
			}},
		{Name: "items.get", Summary: "Read one item's stock level", InputSchema: itemSchema, Examples: []json.RawMessage{json.RawMessage(`{"sku":"bolt"}`)},
			Run: func(_ context.Context, call plugin.ToolOperationCall) (plugin.ToolResult, error) {
				var args struct {
					SKU string `json:"sku"`
				}
				_ = json.Unmarshal(call.Arguments, &args)
				mu.Lock()
				defer mu.Unlock()
				return plugin.ToolData("", map[string]any{"sku": args.SKU, "quantity": stock[args.SKU]})
			}},
		{Name: "items.adjust", Summary: "Add or remove stock", Mutates: true, RequiredGrants: []string{"inventory.adjust"},
			InputSchema: json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string","enum":["bolt","nut"]},"delta":{"type":"integer","minimum":-1000,"maximum":1000}},"required":["sku","delta"],"additionalProperties":false}`),
			Examples:    []json.RawMessage{json.RawMessage(`{"sku":"nut","delta":-5}`)},
			Confirm: func(_ context.Context, call plugin.ToolOperationCall) (*plugin.ToolConfirmationRequest, error) {
				var args struct {
					SKU   string `json:"sku"`
					Delta int    `json:"delta"`
				}
				_ = json.Unmarshal(call.Arguments, &args)
				if args.Delta >= -10 && args.Delta <= 10 {
					return nil, nil
				}
				return &plugin.ToolConfirmationRequest{Key: fmt.Sprintf("adjust:%s:%d", args.SKU, args.Delta), Text: fmt.Sprintf("Adjust %s stock by %d units?", args.SKU, args.Delta)}, nil
			},
			Run: func(_ context.Context, call plugin.ToolOperationCall) (plugin.ToolResult, error) {
				var args struct {
					SKU   string `json:"sku"`
					Delta int    `json:"delta"`
				}
				_ = json.Unmarshal(call.Arguments, &args)
				mu.Lock()
				defer mu.Unlock()
				if stock[args.SKU]+args.Delta < 0 {
					return plugin.ToolError("stock cannot go below zero"), nil
				}
				stock[args.SKU] += args.Delta
				return plugin.ToolData("adjusted", map[string]any{"sku": args.SKU, "quantity": stock[args.SKU]})
			}},
	}
	tool, run, err := plugin.NewOperationTool("inventory", "Inspect and adjust warehouse stock. Call operation help to list operations and their arguments.", operations)
	if err != nil {
		panic(err)
	}
	d := description(id, "Inventory example", []string{plugin.ToolAgentGrant, "inventory.adjust"}, []plugin.ToolDescriptor{tool})
	return Plugin{Description: d, Handler: plugin.ToolHandler(map[string]plugin.ToolFunc{"inventory": run})}
}
