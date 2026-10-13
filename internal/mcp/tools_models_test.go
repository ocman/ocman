package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/mark3labs/mcp-go/mcptest"
)

func TestModelsTool(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    map[string]any
		catalog *platforms.DirectoryCatalog
		err     error
		wantErr string
	}{
		{name: "live remote", args: map[string]any{"directory": " /repo ", "platform": " r-owner:opencode "}, catalog: &platforms.DirectoryCatalog{
			DefaultModel: "provider/model", LiveConnection: true,
			Models: &platforms.SessionModelsResponse{HasProviders: true, ProviderDefaults: map[string]string{"provider": "model"}, Models: []platforms.SessionModel{
				{Provider: "provider", Model: "model", IsAvailable: true, Reasoning: []string{"high"}},
				{Provider: "old", Model: "historical", IsAvailable: false},
			}},
		}},
		{name: "no catalog", args: map[string]any{"directory": "/repo"}},
		{name: "no models", args: map[string]any{"directory": "/repo"}, catalog: &platforms.DirectoryCatalog{}},
		{name: "nil model list", args: map[string]any{"directory": "/repo"}, catalog: &platforms.DirectoryCatalog{Models: &platforms.SessionModelsResponse{}}},
		{name: "missing directory", args: map[string]any{}, wantErr: "directory is required"},
		{name: "blank directory", args: map[string]any{"directory": " "}, wantErr: "directory is required"},
		{name: "wrong directory type", args: map[string]any{"directory": 42}, wantErr: "directory is required"},
		{name: "relative directory", args: map[string]any{"directory": "repo"}, wantErr: "directory must be absolute"},
		{name: "wrong platform type", args: map[string]any{"directory": "/repo", "platform": true}, wantErr: "platform must be a string"},
		{name: "upstream failure", args: map[string]any{"directory": "/repo"}, err: errors.New("private upstream detail"), wantErr: "model catalog request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			deps := internalmcp.Deps{ModelCatalog: func(_ context.Context, platform string, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error) {
				called = true
				if req.Directory != "/repo" || req.Port != "" {
					t.Fatalf("request = %#v", req)
				}
				if tc.name == "live remote" && platform != "r-owner:opencode" {
					t.Fatalf("platform = %q", platform)
				}
				return tc.catalog, "r-owner:opencode", tc.err
			}}
			srv, err := mcptest.NewServer(t, internalmcp.ServerTools(deps)...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			result := callTool(t, srv, "models", tc.args)
			text := resultText(result)
			if tc.wantErr != "" {
				if !result.IsError || !strings.Contains(text, tc.wantErr) || strings.Contains(text, "private upstream detail") {
					t.Fatalf("result = %s", text)
				}
				if tc.err == nil && called {
					t.Fatal("invalid input reached catalog")
				}
				return
			}
			var out struct {
				platforms.SessionModelsResponse
				Platform       string `json:"platform"`
				DefaultModel   string `json:"defaultModel"`
				LiveConnection bool   `json:"liveConnection"`
			}
			if result.IsError || json.Unmarshal([]byte(text), &out) != nil || !called || out.Models == nil || out.Platform != "r-owner:opencode" {
				t.Fatalf("result = %s", text)
			}
			if tc.name == "live remote" && (!out.LiveConnection || !out.HasProviders || out.DefaultModel != "provider/model" || len(out.Models) != 2 || !out.Models[0].IsAvailable || out.Models[1].IsAvailable || out.Models[0].Reasoning[0] != "high" || out.ProviderDefaults["provider"] != "model") {
				t.Fatalf("catalog metadata lost: %s", text)
			}
		})
	}
}

func TestModelsToolRegistration(t *testing.T) {
	for _, tool := range internalmcp.ServerTools(internalmcp.Deps{}) {
		if tool.Tool.Name == "models" {
			t.Fatal("models registered without a catalog reader")
		}
	}
	deps := internalmcp.Deps{ModelCatalog: func(context.Context, string, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error) {
		return nil, "", nil
	}}
	for _, tool := range internalmcp.ServerTools(deps) {
		if tool.Tool.Name == "models" {
			if tool.Tool.Annotations.ReadOnlyHint == nil || !*tool.Tool.Annotations.ReadOnlyHint {
				t.Fatal("models must be read-only")
			}
			return
		}
	}
	t.Fatal("models not registered")
}
