package prworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/module"
)

var _ module.ToolProvider = (*Module)(nil)

func (m *Module) Tools() []server.ServerTool {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session/{id}/pull-requests", m.list)
	mux.HandleFunc("GET /api/session/{id}/pull-request-checks", m.checks)
	mux.HandleFunc("POST /api/session/{id}/pull-requests", m.change)
	mux.HandleFunc("POST /api/session/{id}/pull-request-feedback", m.feedback)
	if m.d.ProjectHandler != nil {
		mux.Handle("/api/project/", m.d.ProjectHandler)
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("pull_requests",
		mcplib.WithDescription("Browse PRs and checks, launch PR sessions, link PRs to sessions, watch changes, and prepare feedback/check/conflict task drafts. After creating a PR, use action link with host, repo and number to attach it to the current session, even when its project is another repository, then verify with action linked before reporting completion. Alternatively link by remote from the session checkout. "+prSessionIdentityGuidance+" If linking or verification fails, report the failure alongside the PR URL. Do not enable watching unless requested. Use action help for inputs and safety rules. Never merges or changes credentials."),
		mcplib.WithString("action", mcplib.Required()),
		mcplib.WithString("directory", mcplib.Description("Absolute project path, or current checkout path for automatic session lookup when identity is missing.")),
		mcplib.WithString("remote_id", mcplib.Description("Project owner, or optional owner scope for session lookup; local means this machine.")), mcplib.WithString("remote"),
		mcplib.WithString("session_id", mcplib.Description("Session target. Omit with directory to look up a unique session automatically.")),
		mcplib.WithString("platform", mcplib.Description("Exact session platform, including named-account or remote prefixes. Optional with directory; narrows automatic lookup.")),
		mcplib.WithString("host"), mcplib.WithString("repo"), mcplib.WithNumber("number"),
		mcplib.WithString("sha"), mcplib.WithString("state"), mcplib.WithString("mine"),
		mcplib.WithNumber("page"), mcplib.WithBoolean("refresh"),
		mcplib.WithString("mode"), mcplib.WithString("task"),
		mcplib.WithBoolean("detail", mcplib.Description("linked only: return full saved PR metadata instead of the compact default."))),
		Handler: func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			req, err := m.lookupToolSession(ctx, req)
			if err != nil {
				return mcplib.NewToolResultError(err.Error()), nil
			}
			return handlePRTool(ctx, mux, req), nil
		}}}
}

func handlePRTool(ctx context.Context, handler http.Handler, req mcplib.CallToolRequest) *mcplib.CallToolResult {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required")
	}
	if action == "help" {
		return prToolHelp()
	}
	method, path, body, err := prToolRequest(action, req)
	if err != nil {
		return mcplib.NewToolResultError(err.Error())
	}
	// Only fixed routes are reachable. MCP authenticates the caller; the existing
	// handlers retain session resolution, explicit ownership and REST validation.
	r, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return mcplib.NewToolResultError("invalid PR request")
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code >= http.StatusBadRequest {
		return mcplib.NewToolResultError(fmt.Sprintf("PR request failed (%d): %s", w.Code, strings.TrimSpace(w.Body.String())))
	}
	if w.Code == http.StatusNoContent {
		return internalmcp.ToolResultJSON(map[string]string{"status": "unlinked"})
	}
	if action == "linked" {
		var linked struct {
			PullRequests []Link `json:"pullRequests"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &linked); err != nil {
			return mcplib.NewToolResultError("invalid PR response")
		}
		// Older remote owners ignore the filter query; apply it here as well.
		u, _ := url.Parse(path)
		links := filterLinks(linked.PullRequests, u.Query())
		if req.GetBool("detail", false) {
			return internalmcp.ToolResultJSON(map[string]any{"pullRequests": links})
		}
		return internalmcp.ToolResultJSON(map[string]any{"pullRequests": compactLinks(links)})
	}
	var result any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		return mcplib.NewToolResultError("invalid PR response")
	}
	return internalmcp.ToolResultJSON(result)
}

type compactRemote struct {
	Host string `json:"host"`
	Repo string `json:"repo"`
	Name string `json:"name,omitempty"`
}

// compactLink is the default linked shape. Full PR metadata grows with every
// linked PR and is returned only when detail is requested.
type compactLink struct {
	Number         int           `json:"number"`
	Title          string        `json:"title,omitempty"`
	State          string        `json:"state,omitempty"`
	URL            string        `json:"url,omitempty"`
	Remote         compactRemote `json:"remote"`
	Watching       bool          `json:"watching"`
	BackgroundTask bool          `json:"backgroundTask,omitempty"`
	LastError      string        `json:"lastError,omitempty"`
}

func compactLinks(links []Link) []compactLink {
	out := make([]compactLink, 0, len(links))
	for _, l := range links {
		out = append(out, compactLink{Number: l.Number, Title: l.PR.Title, State: l.PR.Status, URL: l.PR.URL, Remote: compactRemote{Host: l.Remote.Host, Repo: l.Remote.Repo, Name: l.Remote.Name}, Watching: l.Watching, BackgroundTask: l.BackgroundTask, LastError: l.LastError})
	}
	return out
}
