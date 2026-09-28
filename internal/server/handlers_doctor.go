package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

// WithToolPathError records the toolpath.Ensure result so the doctor can
// report a login-shell PATH that could not be read.
func (s *Server) WithToolPathError(err error) *Server {
	s.toolPathErr = err
	return s
}

// handleDoctor reports structured prerequisite checks so the UI can
// explain what is missing and how to fix it. Host-scoped checks come
// from the local Host; process-scoped ones are added around them.
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	checks := []hostsvc.DoctorCheck{s.openCodeDBCheck()}
	checks = append(checks, s.router().Local().Doctor(r.Context())...)
	checks = append(checks, s.mcpListenerCheck(), s.loginShellPathCheck())
	writeJSON(w, map[string]any{"checks": checks, "logPath": s.logPath})
}

func (s *Server) openCodeDBCheck() hostsvc.DoctorCheck {
	c := hostsvc.DoctorCheck{ID: "opencode-db", Label: "OpenCode database", Required: true}
	for _, issue := range s.startupIssues {
		if issue.ID == "opencode-db-missing" {
			c.Detail, c.Hint = issue.Message, "Run OpenCode once so it creates its database, then restart ocman"
			return c
		}
	}
	if s.db == nil {
		c.Detail = "OpenCode platform is disabled (-platforms)"
		return c
	}
	c.OK, c.Detail = true, "opened"
	return c
}

func (s *Server) mcpListenerCheck() hostsvc.DoctorCheck {
	c := hostsvc.DoctorCheck{ID: "mcp-listener", Label: "MCP listener"}
	switch {
	case s.mcpListenErr != "":
		c.Detail, c.Hint = s.mcpListenErr, "Free the -mcp-addr port or pick another loopback address, then restart ocman"
	case s.mcpAddr == "":
		c.Detail = "dedicated listener disabled; /mcp is served on the main port"
	default:
		c.OK, c.Detail = true, s.mcpServerURL()
	}
	return c
}

func (s *Server) loginShellPathCheck() hostsvc.DoctorCheck {
	c := hostsvc.DoctorCheck{ID: "login-shell-path", Label: "Login shell PATH", OK: s.toolPathErr == nil}
	if s.toolPathErr != nil {
		c.Detail = s.toolPathErr.Error()
		c.Hint = "Launched from Finder? Your login shell PATH could not be read; install tools in /usr/local/bin or /opt/homebrew/bin"
	}
	return c
}
