package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ArtifactFile is one file to publish: an absolute Path, or Name + Content.
type ArtifactFile struct {
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
	MIME    string `json:"mime,omitempty"`
}

// ArtifactLink is one http(s) link to publish.
type ArtifactLink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

// ArtifactCreateRequest is the validated create action input.
type ArtifactCreateRequest struct {
	Title, Description, Directory, Platform, SessionID string
	Files                                              []ArtifactFile
	Links                                              []ArtifactLink
}

// artifactService returns artifacts whose item URLs are already absolute.
// Errors wrapping state.ErrArtifactInvalid are reported to the agent verbatim.
type artifactService interface {
	CreateArtifact(context.Context, ArtifactCreateRequest) (state.Artifact, error)
	ListArtifacts(context.Context, state.ArtifactFilter) ([]state.Artifact, string, error)
	GetArtifact(context.Context, string) (state.Artifact, error)
	// ArtifactPageURL is the absolute ocman UI URL for artifact id.
	ArtifactPageURL(id string) string
}

type artifactTools struct{ svc artifactService }

var artifactActions = []sessionAction{
	{name: "help", description: "Describes every available artifact action.", example: `{"action":"help"}`, output: "Artifact action documentation"},
	{name: "create", description: "Publishes an immutable artifact of files and links for a project. Pass your own platform and session_id to attach it to your session.", example: `{"action":"create","directory":"/repo","platform":"opencode","session_id":"ses_1","title":"Coverage report","files":[{"path":"/repo/coverage.html"},{"name":"notes.md","content":"# Notes"}],"links":[{"url":"https://example.com/pr/1","label":"PR"}]}`, required: []string{"directory", "title"}, optional: []string{"description", "platform", "session_id", "files", "links"}, output: "{id, url, items: {kind, name?, url, label?}[], markdown}"},
	{name: "list", description: "Finds published artifacts newest first. Filter by the stored project root, not a worktree path; list without directory if unknown. Omit platform and session_id to include other sessions. Use get and fetch item URLs to read files, rather than scanning original folders.", example: `{"action":"list","directory":"/repo","limit":20}`, optional: []string{"directory", "platform", "session_id", "cursor", "limit"}, output: "{artifacts: Artifact[], next_cursor}"},
	{name: "get", description: "Gets artifact metadata and items, not file contents. Fetch items[].url to read files; resolve relative file URLs against the origin of the returned top-level url, which is the browser page.", example: `{"action":"get","artifact_id":"art_1"}`, required: []string{"artifact_id"}, output: "Artifact & {url}"},
}

func artifactServerTools(tools *artifactTools) []server.ServerTool {
	if tools == nil || tools.svc == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("artifacts",
		mcplib.WithDescription("Find, read, and publish project artifacts: reports, screenshots, generated files, and links (actions: help, create, list, get). To read earlier artifacts, list with the stored project root (omit directory if unknown), get by artifact_id, then fetch items[].url; get returns metadata, not file contents. Resolve relative file URLs against the origin of get's top-level url. Use MCP instead of scanning original folders or old worktrees. Use action help for schemas and examples."),
		mcplib.WithString("action", mcplib.Required()), mcplib.WithString("directory"), mcplib.WithString("platform"), mcplib.WithString("session_id"),
		mcplib.WithString("title"), mcplib.WithString("description"), mcplib.WithString("artifact_id"), mcplib.WithString("cursor"), mcplib.WithNumber("limit"),
		mcplib.WithArray("files", mcplib.Items(map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "mime": map[string]any{"type": "string"}}})),
		mcplib.WithArray("links", mcplib.Items(map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"}}, "required": []string{"url"}}))),
		Handler: tools.handle}}
}

func addArtifactTools(s *server.MCPServer, tools *artifactTools) {
	for _, tool := range artifactServerTools(tools) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}

func (t *artifactTools) handle(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	var registered sessionAction
	found := false
	for _, a := range artifactActions {
		if a.name == action {
			registered, found = a, true
		}
	}
	if !found {
		return mcplib.NewToolResultError("unknown action"), nil
	}
	for _, field := range registered.required {
		if strings.TrimSpace(req.GetString(field, "")) == "" {
			return mcplib.NewToolResultError(field + " is required"), nil
		}
	}
	platform, sessionID := strings.TrimSpace(req.GetString("platform", "")), strings.TrimSpace(req.GetString("session_id", ""))
	if (platform == "") != (sessionID == "") {
		return mcplib.NewToolResultError("platform and session_id must be provided together"), nil
	}
	switch action {
	case "help":
		return toolResultJSON(artifactHelp()), nil
	case "create":
		in := ArtifactCreateRequest{Title: strings.TrimSpace(req.GetString("title", "")), Description: req.GetString("description", ""), Directory: req.GetString("directory", ""), Platform: platform, SessionID: sessionID}
		args, _ := req.Params.Arguments.(map[string]any)
		if err := decodeArtifactArg(args, "files", &in.Files); err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		if err := decodeArtifactArg(args, "links", &in.Links); err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		a, err := t.svc.CreateArtifact(ctx, in)
		if err != nil {
			return artifactError(err), nil
		}
		return toolResultJSON(t.created(a)), nil
	case "list":
		limit := req.GetInt("limit", 50)
		if limit < 1 || limit > 200 {
			return mcplib.NewToolResultError("limit must be between 1 and 200"), nil
		}
		f := state.ArtifactFilter{Directory: req.GetString("directory", ""), Platform: platform, Cursor: req.GetString("cursor", ""), Limit: limit}
		if sessionID != "" {
			f.SessionIDs = []string{sessionID}
		}
		list, next, err := t.svc.ListArtifacts(ctx, f)
		if err != nil {
			return artifactError(err), nil
		}
		if list == nil {
			list = []state.Artifact{}
		}
		return toolResultJSON(map[string]any{"artifacts": list, "next_cursor": next}), nil
	default: // get
		a, err := t.svc.GetArtifact(ctx, strings.TrimSpace(req.GetString("artifact_id", "")))
		if err != nil {
			return artifactError(err), nil
		}
		return toolResultJSON(struct {
			state.Artifact
			URL string `json:"url"`
		}{a, t.svc.ArtifactPageURL(a.ID)}), nil
	}
}

func decodeArtifactArg(args map[string]any, field string, dst any) error {
	raw, ok := args[field]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(b, dst)
	}
	if err != nil {
		return fmt.Errorf("%s must be an array of objects", field)
	}
	return nil
}

func artifactError(err error) *mcplib.CallToolResult {
	switch {
	case errors.Is(err, state.ErrArtifactNotFound):
		return mcplib.NewToolResultError("artifact not found")
	case errors.Is(err, state.ErrArtifactInvalid):
		return mcplib.NewToolResultError(err.Error())
	}
	return mcplib.NewToolResultError("artifact request failed")
}

type createdArtifactItem struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

func (t *artifactTools) created(a state.Artifact) map[string]any {
	page := t.svc.ArtifactPageURL(a.ID)
	items := make([]createdArtifactItem, 0, len(a.Items))
	md := []string{fmt.Sprintf("[%s](%s)", a.Title, page)}
	for _, it := range a.Items {
		items = append(items, createdArtifactItem{Kind: it.Kind, Name: it.Name, URL: it.URL, Label: it.Label})
		switch {
		case it.Kind == state.ArtifactItemLink:
			label := it.Label
			if label == "" {
				label = it.URL
			}
			md = append(md, fmt.Sprintf("- [%s](%s)", label, it.URL))
		case strings.HasPrefix(it.MIME, "image/") && filepath.Ext(it.Name) != ".svg":
			md = append(md, fmt.Sprintf("![%s](%s)", it.Name, it.URL))
		default:
			md = append(md, fmt.Sprintf("- [%s](%s)", it.Name, it.URL))
		}
	}
	return map[string]any{"id": a.ID, "url": page, "items": items, "markdown": strings.Join(md, "\n")}
}

func artifactHelp() map[string]any {
	help := map[string]any{}
	names := make([]string, 0, len(artifactActions))
	for _, a := range artifactActions {
		names = append(names, a.name)
		help[a.name] = map[string]any{"required": append([]string{}, a.required...), "optional": append([]string{}, a.optional...), "action": a.description, "example": a.example, "output_schema": a.output}
	}
	help["actions"] = names
	help["rules"] = []string{
		"create's directory is the absolute project (or worktree) directory and is folded to the project root; list's directory is an exact filter on that stored root",
		"platform and session_id identify your own session and go together",
		"create needs at least one file or link; files are {path} (absolute) or {name, content, mime?}; links are {url, label?} with http(s) URLs",
		"artifacts are immutable; this tool cannot delete them",
		"for files already committed and pushed, prefer a link to a commit-sha forge permalink over uploading",
		"list returns at most 200 artifacts per page; pass next_cursor as cursor for the next page",
		"to find project artifacts, list with the stored project root and omit platform/session_id to include other sessions; if the root is unknown, list without directory and inspect returned directory fields; do not scan original folders, old worktrees, or blob storage",
		"get returns metadata, not file contents; fetch items[].url to read the published immutable copy, or follow the external URL for kind=link",
		"relative file URLs such as /api/artifacts/art_1/files/0 resolve against the origin of get's top-level url (the browser page), not the MCP listener's port",
		"use item name, mime, and size to select a reader; if fetching fails or needs authentication, report the error rather than guessing an original path",
		"artifacts are local to the connected ocman instance and are not routed across remotes",
	}
	help["errors"] = []string{"action is required", "unknown action", "directory is required", "title is required", "artifact_id is required", "platform and session_id must be provided together", "files must be an array of objects", "links must be an array of objects", "limit must be between 1 and 200", "invalid artifact: <reason>", "artifact not found", "artifact request failed"}
	return help
}
