package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/mcp"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestHandlerNegotiatesProtocolVersion(t *testing.T) {
	handler := mcp.New(mcp.Deps{}).Handler()

	t.Run("modern", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  mcplib.MethodServerDiscover,
			"params": map[string]any{"_meta": map[string]any{
				mcplib.MetaKeyProtocolVersion:    mcplib.ProtocolVersion20260728,
				mcplib.MetaKeyClientInfo:         map[string]any{"name": "test", "version": "0"},
				mcplib.MetaKeyClientCapabilities: map[string]any{},
			}},
		})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set(mcplib.HeaderProtocolVersion, mcplib.ProtocolVersion20260728)
		req.Header.Set(mcplib.HeaderMethod, string(mcplib.MethodServerDiscover))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Result struct {
				SupportedVersions []string `json:"supportedVersions"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Result.SupportedVersions) == 0 || resp.Result.SupportedVersions[0] != mcplib.ProtocolVersion20260728 {
			t.Fatalf("supportedVersions = %v", resp.Result.SupportedVersions)
		}

		body, _ = json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  mcplib.MethodToolsList,
			"params": map[string]any{"_meta": map[string]any{
				mcplib.MetaKeyProtocolVersion:    mcplib.ProtocolVersion20260728,
				mcplib.MetaKeyClientInfo:         map[string]any{"name": "test", "version": "0"},
				mcplib.MetaKeyClientCapabilities: map[string]any{},
			}},
		})
		req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set(mcplib.HeaderProtocolVersion, mcplib.ProtocolVersion20260728)
		req.Header.Set(mcplib.HeaderMethod, string(mcplib.MethodToolsList))
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("tools/list status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	for _, version := range mcplib.LegacyProtocolVersions() {
		t.Run("legacy "+version, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "initialize",
				"params": map[string]any{"protocolVersion": version, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "0"}},
			})
			req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
			}
			if resp.Result.ProtocolVersion != version {
				t.Fatalf("protocolVersion = %q, want %q", resp.Result.ProtocolVersion, version)
			}
		})
	}
}
