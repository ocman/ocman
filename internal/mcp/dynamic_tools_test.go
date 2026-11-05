package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/ocman/ocman/internal/mcp"
)

type toolSource struct {
	mu    sync.Mutex
	tools []server.ServerTool
}

func (s *toolSource) set(tools ...server.ServerTool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = tools
}

func (s *toolSource) get(context.Context) []server.ServerTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tools)
}

func rawTool(name, text string) server.ServerTool {
	return server.ServerTool{Tool: mcplib.NewToolWithRawSchema(name, "Echo", json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}`)),
		Handler: func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultText(text), nil
		}}
}

func dynamicClient(t *testing.T, source *toolSource) *client.Client {
	t.Helper()
	srv := httptest.NewServer(mcp.New(mcp.Deps{SessionService: &fakeSessionService{}, DynamicTools: source.get}).Handler())
	t.Cleanup(srv.Close)
	c, err := client.NewStreamableHttpClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(t.Context(), mcplib.InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	return c
}

func listNames(t *testing.T, c *client.Client) map[string]mcplib.Tool {
	t.Helper()
	result, err := c.ListTools(t.Context(), mcplib.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]mcplib.Tool{}
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func call(t *testing.T, c *client.Client, name string) (string, error) {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = name, map[string]any{}
	result, err := c.CallTool(t.Context(), req)
	if err != nil {
		return "", err
	}
	return result.Content[0].(mcplib.TextContent).Text, nil
}

func TestDynamicToolsFollowTheSourceAndNeverShadowBuiltins(t *testing.T) {
	source := &toolSource{}
	c := dynamicClient(t, source)
	if _, listed := listNames(t, c)["widgets"]; listed {
		t.Fatal("listed before registration")
	}

	source.set(rawTool("widgets", "v1"), rawTool("sessions", "shadow"), rawTool("twin", "a"), rawTool("twin", "b"))
	tools := listNames(t, c)
	widgets, ok := tools["widgets"]
	if _, hasIntent := widgets.InputSchema.Properties["intent"]; !ok || !hasIntent {
		t.Fatalf("widgets %+v", widgets)
	}
	if _, ok := tools["twin"]; ok {
		t.Fatal("duplicate dynamic names must both be withheld")
	}
	if text, err := call(t, c, "widgets"); err != nil || text != "v1" {
		t.Fatalf("call %q %v", text, err)
	}
	if text, err := call(t, c, "sessions"); err == nil && text == "shadow" {
		t.Fatal("dynamic tool replaced a built-in")
	}

	// Changed definitions replace the registration; removed tools disappear
	// from listing and dispatch at once.
	changed := rawTool("widgets", "v2")
	changed.Tool.Description = "Echo v2"
	source.set(changed)
	if text, _ := call(t, c, "widgets"); text != "v2" {
		t.Fatalf("changed %q", text)
	}
	source.set()
	if _, listed := listNames(t, c)["widgets"]; listed {
		t.Fatal("removed tool still listed")
	}
	if _, err := call(t, c, "widgets"); err == nil {
		t.Fatal("removed tool still callable")
	}
}

func TestDynamicToolsRerouteAnIdenticalDefinition(t *testing.T) {
	source := &toolSource{}
	c := dynamicClient(t, source)
	source.set(rawTool("widgets", "first owner"))
	if text, _ := call(t, c, "widgets"); text != "first owner" {
		t.Fatalf("first %q", text)
	}
	source.set(rawTool("widgets", "second owner"))
	if text, _ := call(t, c, "widgets"); text != "second owner" {
		t.Fatalf("identical definition kept the former handler: %q", text)
	}
}
