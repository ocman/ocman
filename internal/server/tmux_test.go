package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

func TestHandleTmuxStatus_NoServerReportsAvailable(t *testing.T) {
	installFakeTmux(t, "error connecting to /private/tmp/tmux-501/default (No such file or directory)")

	srv := &Server{}

	t.Run("sessions", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/tmux/sessions", nil)
		rr := httptest.NewRecorder()
		srv.handleTmuxSessions(rr, req)

		var got struct {
			Available bool           `json:"available"`
			Sessions  []tmux.Session `json:"sessions"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if !got.Available {
			t.Fatalf("available = false, want true")
		}
		if len(got.Sessions) != 0 {
			t.Fatalf("sessions = %v, want empty", got.Sessions)
		}
	})

	t.Run("clients", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/tmux/clients", nil)
		rr := httptest.NewRecorder()
		srv.handleTmuxClients(rr, req)

		var got struct {
			Available bool          `json:"available"`
			Clients   []tmux.Client `json:"clients"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if !got.Available {
			t.Fatalf("available = false, want true")
		}
		if len(got.Clients) != 0 {
			t.Fatalf("clients = %v, want empty", got.Clients)
		}
	})
}

func TestHandleTmuxStatus_ConnectionPermissionErrorReportsUnavailable(t *testing.T) {
	installFakeTmux(t, "error connecting to /private/tmp/tmux-501/default (Permission denied)")

	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/tmux/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleTmuxSessions(rr, req)

	var got struct {
		Available bool `json:"available"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Available {
		t.Fatalf("available = true, want false")
	}
}

func installFakeTmux(t *testing.T, stderr string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  list-sessions|list-clients|list-windows)
    printf '%%s\n' %q >&2
    exit 1
    ;;
  *)
    exit 0
    ;;
esac
`, stderr)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
