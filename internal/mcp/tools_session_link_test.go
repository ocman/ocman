package mcp_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/testutil"
)

func TestCreateSessionDashboardLink(t *testing.T) {
	for _, platform := range []string{"opencode", "r-machine:claudecode", "cnx_named"} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/delivery-failure=%t", platform, failed), func(t *testing.T) {
				svc := &fakeSessionCreator{created: internalmcp.CreatedSession{Platform: platform, SessionID: "new /?#", Directory: "/repo"}}
				if failed {
					svc.err = errors.New("private delivery failure")
				}
				srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{
					SessionService: svc,
					PublicURL:      func(path string) string { return "https://ocman.example/dashboard" + path },
				})...)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(srv.Close)
				result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "go", "directory": "/repo", "platform": platform, "model": "p/m"})
				text := testutil.ResultText(result)
				wantURL := "https://ocman.example/dashboard/session/new%20%2F%3F%23?platform=" + strings.ReplaceAll(platform, ":", "%3A")
				if result.IsError != failed {
					t.Fatalf("IsError = %v, text = %s", result.IsError, text)
				}
				if failed {
					if !strings.Contains(text, wantURL) || strings.Contains(text, "private delivery failure") {
						t.Fatalf("failure missing safe session link: %s", text)
					}
					return
				}
				var response struct {
					Platform, Directory string
					SessionID           string `json:"session_id"`
					URL                 string `json:"url"`
				}
				if err := json.Unmarshal([]byte(text), &response); err != nil {
					t.Fatal(err)
				}
				if response.URL != wantURL || response.SessionID != svc.created.SessionID || response.Platform != platform || response.Directory != "/repo" {
					t.Fatalf("response = %#v, want URL %s", response, wantURL)
				}
			})
		}
	}
}

func TestCreateSessionFailureWithoutSessionHasNoLink(t *testing.T) {
	svc := &fakeSessionCreator{err: errors.New("creation failed")}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{
		SessionService: svc,
		PublicURL: func(string) string {
			t.Fatal("must not link a session that was not created")
			return ""
		},
	})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "go", "directory": "/repo", "platform": "opencode", "model": "p/m"})
	if !result.IsError || testutil.ResultText(result) != "session request failed" {
		t.Fatalf("result = %s", testutil.ResultText(result))
	}
}
