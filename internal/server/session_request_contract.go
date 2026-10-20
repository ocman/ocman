package server

import (
	"net/http"
	"strings"
)

// Only routes that consume a top-level session reference opt in. ID-shaped
// fields in a message, plugin input or a future feature are not a contract.
func sessionRequestFields(r *http.Request) []string {
	path := strings.TrimRight(r.URL.Path, "/")
	if r.Method == http.MethodPost {
		switch path {
		case "/api/session/archive", "/api/session/seen", "/api/session/pin", "/api/session/snooze":
			return []string{"sessionId"}
		case "/api/sessions", "/api/worktree/create-and-launch":
			return []string{"parentSessionId"}
		}
	}
	if r.Method == http.MethodPost && path == "/api/routines" ||
		r.Method == http.MethodPut && strings.HasPrefix(path, "/api/routines/") && !strings.Contains(strings.TrimPrefix(path, "/api/routines/"), "/") {
		return []string{"sessionId"}
	}
	return nil
}
