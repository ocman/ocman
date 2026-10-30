package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func enableFactoryBeta(t *testing.T, s *Server) {
	t.Helper()
	if err := s.stateDB.SetSetting(t.Context(), factoryBetaKey, "true"); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryBetaFailsClosedWithoutState(t *testing.T) {
	s := &Server{}
	tools := s.filterBetaTools(t.Context(), []mcplib.Tool{{Name: "factory"}, {Name: "sessions"}})
	if len(tools) != 1 || tools[0].Name != "sessions" {
		t.Fatalf("tools without state = %#v", tools)
	}
	rec := httptest.NewRecorder()
	serveSetting(s.betaFeaturesSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/beta-features", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable setting = %d", rec.Code)
	}
}

func TestDisabledFactoryHiddenFromMCP(t *testing.T) {
	s := testServer(t)
	handler := s.mcpHandler()
	request := func(method, params string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	for _, enabled := range []bool{false, true, false} {
		value := "false"
		if enabled {
			value = "true"
		}
		if err := s.stateDB.SetSetting(t.Context(), "beta.factory", value); err != nil {
			t.Fatal(err)
		}
		body := request("tools/list", `{}`)
		if !enabled && strings.Contains(strings.ToLower(body), "factory") {
			t.Fatalf("disabled feature leaked into tool schemas: %s", body)
		}
		help := request("tools/call", `{"name":"inbox","arguments":{"action":"help"}}`)
		if !enabled && strings.Contains(help, "factory") {
			t.Fatalf("disabled category leaked into help: %s", help)
		}
		if !enabled {
			sent := request("tools/call", `{"name":"inbox","arguments":{"action":"send","title":"Review","body":"Ready","category":"factory"}}`)
			if !strings.Contains(sent, "category is unavailable") {
				t.Fatalf("disabled category accepted: %s", sent)
			}
		} else if !strings.Contains(body, `"enum":["general","factory","routine"]`) {
			t.Fatalf("enabled category missing from schema: %s", body)
		}
		for _, name := range []string{"factory", "factory_control"} {
			if strings.Contains(body, `"name":"`+name+`"`) != enabled {
				t.Fatalf("enabled=%v, tools = %s", enabled, body)
			}
			if !enabled {
				called := request("tools/call", `{"name":"`+name+`","arguments":{"action":"help"}}`)
				if !strings.Contains(called, `"error"`) && !strings.Contains(called, `"isError":true`) {
					t.Fatalf("disabled tool was callable: %s", called)
				}
			}
		}
		if !strings.Contains(body, `"name":"sessions"`) || !strings.Contains(body, `"name":"inbox"`) {
			t.Fatalf("unrelated tools disappeared: %s", body)
		}
	}
}

func TestFactoryBetaSetting(t *testing.T) {
	s := testServer(t)
	handler := mustRoutes(t, s)
	request := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/settings/beta-features", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(http.MethodGet, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"factory":false`) {
		t.Fatalf("default = %d %s", rec.Code, rec.Body.String())
	}
	for _, enabled := range []string{"true", "false"} {
		if rec := request(http.MethodPost, `{"factory":`+enabled+`}`); rec.Code != 200 {
			t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
		}
		if rec := request(http.MethodGet, ""); !strings.Contains(rec.Body.String(), `"factory":`+enabled) {
			t.Fatalf("read = %s", rec.Body.String())
		}
	}
	if rec := request(http.MethodPost, `{"factory":"yes"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d", rec.Code)
	}
	s.auth = newTestAuth(t, "secret")
	handler = mustRoutes(t, s)
	if rec := request(http.MethodPost, `{"factory":true}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated save = %d", rec.Code)
	}
}
