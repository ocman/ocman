package agentui

import (
	"context"
	"encoding/json"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/module"
)

type Module struct{}

func (*Module) Name() string         { return "agentui" }
func (*Module) Routes(module.Router) {}

func (*Module) Tools() []server.ServerTool {
	return []server.ServerTool{{Tool: mcplib.NewTool("render_ui",
		mcplib.WithDescription("Render a user-friendly result inside this conversation using A2UI v0.9.1 and Ocman's approved catalog: text, layouts, sortable tables, charts (bar, grouped/stacked bar, pie/donut, line/area, scatter), KPI stat tiles, and simple forms. Use when structured or interactive results communicate better than prose; prefer charts over screenshots. Call action help first for the exact catalog, schemas, limits and examples. action render takes title, summary and messages. Results appear under this tool call; do not copy JSON into your reply. Buttons require user confirmation before sending an event back to this session; they never execute commands directly. Keep a short text summary for other clients."),
		mcplib.WithString("action", mcplib.Required(), mcplib.Enum("help", "render")),
		mcplib.WithString("title"), mcplib.WithString("summary"),
		mcplib.WithArray("messages", mcplib.Items(map[string]any{"type": "object"}))), Handler: handle}}
}

func handle(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, _ := req.RequireString("action")
	if action == "help" {
		return internalmcp.ToolResultJSON(help()), nil
	}
	if action != "render" {
		return mcplib.NewToolResultError("action must be help or render"), nil
	}
	args := req.GetArguments()
	raw, err := json.Marshal(map[string]any{"title": args["title"], "summary": args["summary"], "messages": args["messages"]})
	if err != nil {
		return mcplib.NewToolResultError("result must be JSON"), nil
	}
	result, err := Validate(raw)
	if err != nil {
		out := internalmcp.ToolResultJSON(map[string]any{"error": map[string]any{
			"code": "VALIDATION_FAILED", "surfaceId": "", "path": "/messages", "message": err.Error(),
		}})
		out.IsError = true
		return out, nil
	}
	return internalmcp.ToolResultJSON(map[string]any{"ocmanA2ui": result, "hint": "The result is displayed below this tool call in Ocman. Do not repeat the JSON. Give the user a short text summary."}), nil
}

func help() map[string]any {
	docs := map[string]any{}
	for _, name := range []string{"common_types.json", "catalog.json", "result.schema.json"} {
		var doc any
		_ = json.Unmarshal(schemaBytes(name), &doc)
		docs[name] = doc
	}
	return map[string]any{
		"version": "v0.9.1", "a2uiClientCapabilities": map[string]any{"supportedCatalogIds": []string{CatalogID}},
		"schemas": docs,
		"rules": []string{
			"Call action render with title, summary, messages. Each result is a self-contained snapshot saved in the tool output; separate calls do not patch earlier results.",
			"Use exactly one createSurface followed by updateComponents and optional updateDataModel messages for the same surfaceId. Every message has version v0.9.1. Include a root component.",
			"Only this custom catalog is supported, not the full A2UI basic catalog. No HTML, JavaScript, URLs, functions, templates, theme overrides, deleteSurface or cross-call updates.",
			"Bindings use one top-level field, e.g. /name. updateDataModel replaces the root object; nested paths and non-scalar data model values are unsupported.",
			"Button child must be Text. action.event.context may reference current form bindings. The user reviews and confirms the resolved action before it is sent as a normal message to this session. Existing session permissions still apply. Read-only views disable actions.",
			"Limits: 64 KiB per result, 32 messages, 100 components, depth 16, 500 rendered nodes. Table supports 200 rows and 12 columns; BarChart supports 50 non-negative values.",
			"Charts: PieChart (variant donut or pie, 1-50 non-negative slices with unique labels; the largest 7 show and the rest, plus slices under 2% when there are at least two, fold into Other). MultiBarChart (1-50 categories, 1-8 series of non-negative values or null, mode grouped or stacked, orientation vertical or horizontal). LineChart (x of type category, number or date with 2-366 values, 1-8 series of numbers or null for gaps, optional area). ScatterChart (1-3 series of up to 200 [x, y] points). Every series has one value per category or x value; series names are unique and number or date x values strictly increase. At most 12 PieChart, MultiBarChart, LineChart or ScatterChart renders per result.",
			"Dates are ISO 8601: YYYY-MM-DD or YYYY-MM-DDTHH:MM[:SS[.sss]] with optional Z or ±HH:MM; times without a zone are UTC.",
			"Values: unit is a short suffix such as cores, MiB or req/s. format is number (default), compact (1.2K), percent (values already 0-100) or bytes (raw bytes shown as KiB/MiB/GiB; unit is ignored). Stat shows one headline value with an optional delta, deltaLabel such as vs last week, and better up, down or neither to color the delta.",
			"Colors are assigned automatically and cannot be set: each distinct series or slice name keeps one color across all charts in the result, in order of first appearance, for up to 8 names. A slice or series named Other is always neutral. Charts include tooltips, a legend for two or more series and a data table.",
		},
		"chartExample": chartExample(),
		"example": map[string]any{
			"action": "render", "title": "Investigation", "summary": "Two jobs failed. Choose a follow-up.",
			"messages": []any{
				map[string]any{"version": "v0.9.1", "createSurface": map[string]any{"surfaceId": "investigation", "catalogId": CatalogID}},
				map[string]any{"version": "v0.9.1", "updateComponents": map[string]any{"surfaceId": "investigation", "components": []any{
					map[string]any{"id": "root", "component": "Column", "children": []string{"summary", "jobs", "followup", "button"}},
					map[string]any{"id": "summary", "component": "Text", "text": "Two jobs failed", "variant": "h3"},
					map[string]any{"id": "jobs", "component": "Table", "columns": []string{"Job", "Status"}, "rows": [][]string{{"Backend", "Failed"}, {"Frontend", "Failed"}}},
					map[string]any{"id": "followup", "component": "TextField", "label": "Investigate next", "value": map[string]any{"path": "/job"}},
					map[string]any{"id": "button", "component": "Button", "child": "buttonLabel", "action": map[string]any{"event": map[string]any{"name": "investigate", "context": map[string]any{"job": map[string]any{"path": "/job"}}}}},
					map[string]any{"id": "buttonLabel", "component": "Text", "text": "Continue investigation"},
				}}},
				map[string]any{"version": "v0.9.1", "updateDataModel": map[string]any{"surfaceId": "investigation", "value": map[string]any{"job": "Backend"}}},
			},
		},
	}
}

func chartExample() map[string]any {
	pie := func(id, label, unit, format string, values ...float64) map[string]any {
		data := []any{}
		for i, app := range []string{"api", "worker", "web", "cron"} {
			data = append(data, map[string]any{"label": app, "value": values[i]})
		}
		c := map[string]any{"id": id, "component": "PieChart", "label": label, "format": format, "data": data}
		if unit != "" {
			c["unit"] = unit
		}
		return c
	}
	return map[string]any{
		"action": "render", "title": "Cluster resources", "summary": "api uses most of the requested CPU and memory.",
		"messages": []any{
			map[string]any{"version": "v0.9.1", "createSurface": map[string]any{"surfaceId": "resources", "catalogId": CatalogID}},
			map[string]any{"version": "v0.9.1", "updateComponents": map[string]any{"surfaceId": "resources", "components": []any{
				map[string]any{"id": "root", "component": "Column", "children": []string{"stats", "pies", "usage"}},
				map[string]any{"id": "stats", "component": "Row", "children": []string{"cpu", "memory"}},
				map[string]any{"id": "cpu", "component": "Stat", "label": "CPU used", "value": 6.4, "unit": "cores", "delta": 0.8, "deltaLabel": "vs last week", "better": "down"},
				map[string]any{"id": "memory", "component": "Stat", "label": "Memory requested", "value": 25769803776, "format": "bytes"},
				map[string]any{"id": "pies", "component": "Row", "children": []string{"cpuRequested", "memoryRequested", "cpuUsed"}},
				pie("cpuRequested", "CPU requested", "cores", "number", 4, 2.5, 1, 0.5),
				pie("memoryRequested", "Memory requested", "", "bytes", 12884901888, 8589934592, 3221225472, 1073741824),
				pie("cpuUsed", "CPU used", "cores", "number", 3.1, 2.2, 0.7, 0.4),
				map[string]any{"id": "usage", "component": "LineChart", "label": "CPU used per day", "unit": "cores", "area": true,
					"x": map[string]any{"type": "date", "values": []string{"2026-11-01", "2026-11-02", "2026-11-03"}},
					"series": []any{
						map[string]any{"name": "api", "values": []any{2.8, 3.0, 3.1}},
						map[string]any{"name": "worker", "values": []any{1.9, nil, 2.2}},
					}},
			}}},
		},
	}
}
