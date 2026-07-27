package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
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

// TestHandleTmuxSessions_RoutesThroughHostSeam pins that the handler
// sources its rows from the hostsvc.Host seam (AD-16 R-A) rather than
// calling tmux.ListSessions directly — the bypass check-host-helpers.sh
// existed to catch but had rotted into a no-op. It also pins the wire
// shape, which the frontend keys off (resolvedPath).
func TestHandleTmuxSessions_RoutesThroughHostSeam(t *testing.T) {
	installFakeTmuxWithSessions(t)

	srv := &Server{}
	// Replace the local Host with a stub. If the handler still called
	// tmux.ListSessions directly it would return the fake binary's rows
	// instead of these.
	srv.hostRouter = hostsvc.NewRouter(&seamTmuxHost{sessions: []hostsvc.TmuxSession{
		{Name: "~/src/ocman", Path: "/home/u/src/ocman"},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/tmux/sessions", nil)
	rr := httptest.NewRecorder()
	srv.handleTmuxSessions(rr, req)

	var got struct {
		Available bool `json:"available"`
		Sessions  []struct {
			Name         string `json:"name"`
			ResolvedPath string `json:"resolvedPath"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.Available {
		t.Fatal("available = false, want true")
	}
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want exactly the seam's row", got.Sessions)
	}
	if got.Sessions[0].Name != "~/src/ocman" || got.Sessions[0].ResolvedPath != "/home/u/src/ocman" {
		t.Errorf("session = %+v, want the seam's row with resolvedPath preserved", got.Sessions[0])
	}
}

// seamTmuxHost is a hostsvc.Host that only implements TmuxSessions; the
// embedded nil interface panics on anything else, which is what we want
// — the handler must touch nothing but the seam method.
type seamTmuxHost struct {
	hostsvc.Host
	sessions []hostsvc.TmuxSession
}

func (h *seamTmuxHost) TmuxSessions(context.Context) ([]hostsvc.TmuxSession, error) {
	return h.sessions, nil
}

// installFakeTmuxWithSessions puts a tmux on PATH that reports itself
// available and lists a decoy session, so a handler that bypassed the
// seam would visibly return the decoy.
func installFakeTmuxWithSessions(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux")
	script := `#!/bin/sh
case "$1" in
  list-sessions) printf 'decoy|/tmp/decoy|1\n'; exit 0 ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
