package pluginsmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"

	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

const (
	notesID     = "org.example.notes"
	inventoryID = "org.example.inventory"
)

func listedNames(t *testing.T, m *Module) []string {
	t.Helper()
	var names []string
	for _, tool := range m.DynamicTools(t.Context()) {
		names = append(names, tool.Tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestPluginAgentToolsIsolateUnrelatedWorkflows(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	m.SetBuiltinTools([]string{"sessions", "plugin_session_tool"})
	startAgentToolPlugin(t, db, m, "notes", notesID, []string{plugins.ToolAgentGrant, "notes.write"})
	startAgentToolPlugin(t, db, m, "inventory", inventoryID, []string{plugins.ToolAgentGrant})

	if got := listedNames(t, m); !slices.Equal(got, []string{"inventory", "notes_create", "notes_delete", "notes_search"}) {
		t.Fatalf("listed %v", got)
	}
	search, _ := listedTool(t, m, "notes_search")
	if !strings.HasSuffix(search.Tool.Description, "(Plugin: Notes example.)") || search.Tool.Annotations.ReadOnlyHint == nil || !*search.Tool.Annotations.ReadOnlyHint ||
		!strings.Contains(string(search.Tool.RawInputSchema), plugins.ToolConfirmationArgument) {
		t.Fatalf("tool %+v %s", search.Tool, search.Tool.RawInputSchema)
	}

	r := callTool(t, m, notesID, "notes_search", map[string]any{"query": "release", "intent": "find the plan"})
	if r.IsError || resultText(r) != "1 notes matched" || !strings.Contains(mustJSON(t, structured(t, r)), "Release plan") {
		t.Fatalf("search: %s", resultText(r))
	}
	r = callTool(t, m, notesID, "notes_create", map[string]any{"title": "Ideas"})
	if r.IsError || structured(t, r)["id"] != "n3" {
		t.Fatalf("create: %s", resultText(r))
	}

	// The operation tool's help hides operations the grants do not allow.
	r = callTool(t, m, inventoryID, "inventory", map[string]any{"operation": "help"})
	help := mustJSON(t, structured(t, r))
	if !strings.Contains(help, "items.get") || strings.Contains(help, "items.adjust") {
		t.Fatalf("help: %s", help)
	}
	r = callTool(t, m, inventoryID, "inventory", map[string]any{"operation": "help", "arguments": map[string]any{"operation": "items.get"}})
	if detail := mustJSON(t, structured(t, r)); !strings.Contains(detail, `"inputSchema"`) || !strings.Contains(detail, `"sku":"bolt"`) {
		t.Fatalf("help detail: %s", detail)
	}
	r = callTool(t, m, inventoryID, "inventory", map[string]any{"operation": "items.adjust", "arguments": map[string]any{"sku": "nut", "delta": 1}})
	if !r.IsError || !strings.Contains(resultText(r), "unknown operation items.adjust") {
		t.Fatalf("ungranted operation: %s", resultText(r))
	}
	r = callTool(t, m, inventoryID, "inventory", map[string]any{"operation": "items.get", "arguments": map[string]any{"sku": "bolt"}})
	if structured(t, r)["quantity"] != float64(120) {
		t.Fatalf("get: %s", resultText(r))
	}
	r = callTool(t, m, inventoryID, "inventory", map[string]any{"operation": "items.get", "arguments": map[string]any{"sku": "washer"}})
	if !r.IsError || !strings.Contains(resultText(r), "arguments.sku") {
		t.Fatalf("operation validation: %s", resultText(r))
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPluginAgentToolArgumentsAreValidatedBeforeDispatch(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "notes", notesID, []string{plugins.ToolAgentGrant, "notes.write"})
	for args, want := range map[string]string{
		`{}`:                          "arguments.query: is required",
		`{"query":"x","limit":99}`:    "arguments.limit: must be at most 50",
		`{"query":"x","extra":true}`:  "arguments.extra: is not an allowed property",
		`{"query":7}`:                 "arguments.query: must be of type string",
		`{"query":"x","limit":"two"}`: "arguments.limit: must be of type integer",
	} {
		var decoded map[string]any
		_ = json.Unmarshal([]byte(args), &decoded)
		r := callTool(t, m, notesID, "notes_search", decoded)
		if !r.IsError || resultText(r) != "invalid arguments: "+want {
			t.Fatalf("%s: %q", args, resultText(r))
		}
	}
	big := map[string]any{"query": strings.Repeat("x", plugins.MaxToolArgumentBytes)}
	if r := callTool(t, m, notesID, "notes_search", big); !r.IsError || !strings.Contains(resultText(r), "must be at most") {
		t.Fatalf("oversized: %s", resultText(r))
	}
}

func TestPluginAgentToolsFollowLifecycleAndGrants(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "notes", notesID, []string{plugins.ToolAgentGrant, "notes.write"})

	// Revoking a plugin-defined grant hides and refuses only the tools needing it.
	if err := db.SetPluginGrants(t.Context(), notesID, []string{plugins.ToolAgentGrant}); err != nil {
		t.Fatal(err)
	}
	if got := listedNames(t, m); !slices.Equal(got, []string{"notes_search"}) {
		t.Fatalf("after revoke %v", got)
	}
	if r := callTool(t, m, notesID, "notes_create", map[string]any{"title": "x"}); !r.IsError || !strings.Contains(resultText(r), "not permitted") {
		t.Fatalf("revoked call: %s", resultText(r))
	}
	// Without tool.agent nothing is listed or callable.
	if err := db.SetPluginGrants(t.Context(), notesID, []string{"notes.write"}); err != nil {
		t.Fatal(err)
	}
	if got := listedNames(t, m); len(got) != 0 {
		t.Fatalf("without tool.agent %v", got)
	}
	if r := callTool(t, m, notesID, "notes_search", map[string]any{"query": "x"}); !r.IsError || !strings.Contains(resultText(r), "not permitted") {
		t.Fatalf("without tool.agent: %s", resultText(r))
	}
	// A disabled plugin is neither listed nor callable.
	if err := db.SetPluginEnabled(t.Context(), notesID, false, nil); err != nil {
		t.Fatal(err)
	}
	if r := callTool(t, m, notesID, "notes_search", map[string]any{"query": "x"}); !r.IsError || !strings.Contains(resultText(r), "unavailable") {
		t.Fatalf("disabled: %s", resultText(r))
	}
	// Re-enabled but with no running process: listed as stopped, not routable.
	if err := db.SetPluginEnabled(t.Context(), notesID, true, []string{plugins.ToolAgentGrant}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	delete(m.processes, notesID)
	m.mu.Unlock()
	report := toolsReport(t, m, notesID)
	if len(report.Tools) != 3 || report.Tools[0].Status != agentToolStopped {
		t.Fatalf("report %+v", report.Tools)
	}
	if r := callTool(t, m, notesID, "notes_search", map[string]any{"query": "x"}); !r.IsError || !strings.Contains(resultText(r), "unavailable") {
		t.Fatalf("stopped: %s", resultText(r))
	}
	if r := callTool(t, m, "org.example.missing", "notes_search", map[string]any{}); !r.IsError || !strings.Contains(resultText(r), "not found") {
		t.Fatalf("unknown plugin: %s", resultText(r))
	}
}

func toolsReport(t *testing.T, m *Module, id string) agentToolsReport {
	t.Helper()
	value, err := m.manageLocalPlugin(t.Context(), id, "tools", true, pluginManagementInput{})
	if err != nil {
		t.Fatal(err)
	}
	return value.(agentToolsReport)
}

func TestPluginAgentToolNameCollisionsWithholdEveryClaimant(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	m.SetBuiltinTools([]string{"notes_create"})
	startAgentToolPlugin(t, db, m, "notes", notesID, []string{plugins.ToolAgentGrant, "notes.write"})
	if got := listedNames(t, m); !slices.Equal(got, []string{"notes_delete", "notes_search"}) {
		t.Fatalf("built-in collision %v", got)
	}
	if r := callTool(t, m, notesID, "notes_create", map[string]any{"title": "x"}); !r.IsError || !strings.Contains(resultText(r), "unavailable") {
		t.Fatalf("built-in collision call: %s", resultText(r))
	}
	startAgentToolPlugin(t, db, m, "notes", "org.example.twin", []string{plugins.ToolAgentGrant, "notes.write"})
	if got := listedNames(t, m); len(got) != 0 {
		t.Fatalf("duplicate plugin names must withhold both: %v", got)
	}
	for _, id := range []string{notesID, "org.example.twin"} {
		if report := toolsReport(t, m, id); report.Tools[0].Status != agentToolConflict {
			t.Fatalf("%s report %+v", id, report.Tools)
		}
	}
	// Disabling one claimant releases the name for the other.
	if err := db.SetPluginEnabled(t.Context(), "org.example.twin", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := listedNames(t, m); !slices.Equal(got, []string{"notes_delete", "notes_search"}) {
		t.Fatalf("after disable %v", got)
	}
}

func TestPluginAgentToolFailuresUseFixedText(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "edge", "org.example.edge", []string{plugins.ToolAgentGrant})
	for _, tc := range []struct{ tool, want string }{
		{"edge_slow", agentToolText[plugins.ErrorDeadlineExceeded]},
		{"edge_invalid", agentToolText[plugins.ErrorInternal]},
		{"edge_denied", agentToolText[plugins.ErrorPermissionDenied]},
	} {
		if r := callTool(t, m, "org.example.edge", tc.tool, map[string]any{}); !r.IsError || resultText(r) != tc.want {
			t.Fatalf("%s: %q", tc.tool, resultText(r))
		}
	}
	// Plugin text cannot request a host-rendered confirmation card.
	r := callTool(t, m, "org.example.edge", "edge_card", map[string]any{})
	if r.IsError || strings.Contains(resultText(r), "[[ocman:card") {
		t.Fatalf("card marker: %q", resultText(r))
	}
	// A chunk on the unary invoke is a protocol violation: the host refuses
	// the result, whether it sees the chunk or the resulting process failure.
	r = callTool(t, m, "org.example.edge", "edge_chunk", map[string]any{})
	if text := resultText(r); !r.IsError || (text != agentToolText[plugins.ErrorInternal] && text != agentToolText[plugins.ErrorUnavailable]) {
		t.Fatalf("edge_chunk: %q", text)
	}
}

func TestPluginAgentToolConfirmationRoutesAreLocalOnly(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	deny := func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "local only", http.StatusForbidden) }
	}
	r := moduletest.Router{Mux: http.NewServeMux(), Localhost: deny}
	m.Routes(r)
	if w := moduletest.Do(r.Mux, "POST", "/api/plugins/"+notesID+"/tools/confirm", `{"values":{"confirmationId":"tc_A","decision":"approve"}}`, ""); w.Code != http.StatusForbidden {
		t.Fatalf("confirm guard %d", w.Code)
	}
	if w := moduletest.Do(r.Mux, "GET", "/api/plugins/"+notesID+"/tools", "", ""); w.Code == http.StatusForbidden {
		t.Fatalf("read should use normal auth: %d", w.Code)
	}
}

func TestPluginAgentToolsOverTheMCPEndpoint(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "inventory", inventoryID, []string{plugins.ToolAgentGrant})
	builtin := internalmcp.ServerTools(internalmcp.Deps{Tools: m.Tools()})
	var names []string
	for _, tool := range builtin {
		names = append(names, tool.Tool.Name)
	}
	m.SetBuiltinTools(names)
	srv := httptest.NewServer(internalmcp.New(internalmcp.Deps{Tools: m.Tools(), DynamicTools: m.DynamicTools}).Handler())
	t.Cleanup(srv.Close)
	c, err := client.NewStreamableHttpClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Initialize(t.Context(), mcplib.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	listed, err := c.ListTools(t.Context(), mcplib.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var inventory *mcplib.Tool
	for i := range listed.Tools {
		if listed.Tools[i].Name == "inventory" {
			inventory = &listed.Tools[i]
		}
	}
	if inventory == nil || inventory.InputSchema.Properties["intent"] == nil || inventory.InputSchema.Properties[plugins.ToolConfirmationArgument] == nil {
		t.Fatalf("inventory tool %+v", inventory)
	}
	req := mcplib.CallToolRequest{}
	req.Params.Name = "inventory"
	req.Params.Arguments = map[string]any{"operation": "items.list", "intent": "Check stock"}
	result, err := c.CallTool(t.Context(), req)
	if err != nil || result.IsError || !strings.Contains(resultText(result), `"bolt":120`) {
		t.Fatalf("call %v %+v", err, result)
	}
	// Revocation applies to the next call on the same connection.
	if err := db.SetPluginEnabled(t.Context(), inventoryID, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(t.Context(), req); err == nil {
		t.Fatal("disabled plugin tool still routable")
	}
}

// A host deadline cancel can cross the plugin's own deadline result. That
// must not end the plugin, or every timed-out tool would restart it.
func TestPluginAgentToolTimeoutLeavesThePluginServing(t *testing.T) {
	db := statetest.Open(t, Schema)
	m := NewModule(Deps{Store: db})
	startAgentToolPlugin(t, db, m, "edge", "org.example.edge", []string{plugins.ToolAgentGrant})
	process := m.processes["org.example.edge"]
	for range 3 {
		if r := callTool(t, m, "org.example.edge", "edge_slow", map[string]any{}); resultText(r) != agentToolText[plugins.ErrorDeadlineExceeded] {
			t.Fatalf("slow: %q", resultText(r))
		}
		if r := callTool(t, m, "org.example.edge", "edge_invalid", map[string]any{}); resultText(r) != agentToolText[plugins.ErrorInternal] {
			t.Fatalf("after timeout: %q %+v", resultText(r), process.Health())
		}
	}
	if h := process.Health(); h.Status != "ready" || h.RestartCount != 0 {
		t.Fatalf("plugin restarted: %+v", h)
	}
}
