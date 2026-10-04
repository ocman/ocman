package ocv2

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

const sid = `([^/]+)`

func init() {
	handle(http.MethodGet, `/config`, getConfig)
	handle(http.MethodGet, `/project/current`, getProject)
	handle(http.MethodGet, `/agent`, getAgents)
	handle(http.MethodGet, `/command`, getCommands)
	handle(http.MethodGet, `/provider`, getProviders)
	handle(http.MethodGet, `/mcp`, getMCP)
	handle(http.MethodPost, `/mcp/([^/]+)/connect`, connectMCP)
	handle(http.MethodGet, `/lsp`, func(_ *compat, r *http.Request, _ []string) (*http.Response, error) {
		return reply(r, http.StatusOK, []any{}), nil // v2 exposes no LSP status
	})
	handle(http.MethodGet, `/session/status`, getStatus)
	handle(http.MethodPost, `/session`, createSession)
	handle(http.MethodGet, `/session/`+sid, getSession)
	handle(http.MethodPatch, `/session/`+sid, patchSession)
	handle(http.MethodDelete, `/session/`+sid, deleteSession)
	handle(http.MethodGet, `/session/`+sid+`/message`, getMessages)
	handle(http.MethodPost, `/session/`+sid+`/message`, postMessageSync)
	handle(http.MethodPost, `/session/`+sid+`/prompt_async`, promptAsync)
	handle(http.MethodPost, `/session/`+sid+`/shell`, runShell)
	handle(http.MethodPost, `/session/`+sid+`/command`, runCommand)
	handle(http.MethodPost, `/session/`+sid+`/abort`, abort)
	handle(http.MethodPost, `/session/`+sid+`/revert`, revert)
	handle(http.MethodPost, `/session/`+sid+`/unrevert`, unrevert)
	handle(http.MethodPost, `/session/`+sid+`/summarize`, summarize)
	handle(http.MethodPost, `/session/`+sid+`/fork`, fork)
	handle(http.MethodPost, `/experimental/control-plane/move-session`, moveSession)
}

func getConfig(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var entries []any
	err := c.call(r.Context(), http.MethodGet, "/api/config", location(requestDirectory(r)), nil, &entries)
	return finish(r, http.StatusOK, MergeConfig(entries), err)
}

func getProject(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var loc map[string]any
	err := c.call(r.Context(), http.MethodGet, "/api/location", location(requestDirectory(r)), nil, &loc)
	project := obj(loc, "project")
	return finish(r, http.StatusOK, map[string]any{
		"id": str(project, "id"), "worktree": str(project, "directory"), "directory": str(loc, "directory"),
	}, err)
}

func getAgents(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var resp data[[]any]
	err := c.call(r.Context(), http.MethodGet, "/api/agent", location(requestDirectory(r)), nil, &resp)
	return finish(r, http.StatusOK, V1Agents(resp.Data), err)
}

func getCommands(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var resp data[[]any]
	err := c.call(r.Context(), http.MethodGet, "/api/command", location(requestDirectory(r)), nil, &resp)
	return finish(r, http.StatusOK, V1Commands(resp.Data), err)
}

func getProviders(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	q := location(requestDirectory(r))
	var providers, models data[[]any]
	var def data[map[string]any]
	if err := c.call(r.Context(), http.MethodGet, "/api/provider", q, nil, &providers); err != nil {
		return finish(r, 0, nil, err)
	}
	if err := c.call(r.Context(), http.MethodGet, "/api/model", q, nil, &models); err != nil {
		return finish(r, 0, nil, err)
	}
	_ = c.call(r.Context(), http.MethodGet, "/api/model/default", q, nil, &def) // optional
	return reply(r, http.StatusOK, V1Providers(providers.Data, models.Data, def.Data)), nil
}

func getMCP(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var resp data[[]any]
	err := c.call(r.Context(), http.MethodGet, "/api/mcp", location(requestDirectory(r)), nil, &resp)
	out := map[string]any{}
	for _, s := range resp.Data {
		m, _ := s.(map[string]any)
		st := obj(m, "status")
		out[str(m, "name")] = map[string]any{"status": str(st, "status"), "error": str(st, "error")}
	}
	return finish(r, http.StatusOK, out, err)
}

func connectMCP(c *compat, r *http.Request, m []string) (*http.Response, error) {
	err := c.call(r.Context(), http.MethodPost, "/api/experimental/mcp/"+url.PathEscape(m[1])+"/connect", location(requestDirectory(r)), map[string]any{}, nil)
	return finish(r, http.StatusOK, true, err)
}

func getStatus(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var resp data[map[string]any]
	err := c.call(r.Context(), http.MethodGet, "/api/session/active", nil, nil, &resp)
	out := map[string]any{}
	for id := range resp.Data {
		out[id] = map[string]any{"type": "busy"}
	}
	return finish(r, http.StatusOK, out, err)
}

func (c *compat) session(ctx context.Context, id string) (map[string]any, error) {
	var resp data[map[string]any]
	err := c.call(ctx, http.MethodGet, "/api/session/"+url.PathEscape(id), nil, nil, &resp)
	return resp.Data, err
}

func createSession(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var in struct {
		Title      string `json:"title"`
		Directory  string `json:"directory"`
		ParentID   string `json:"parentID"`
		Permission []any  `json:"permission"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if in.Title != "" {
		body["title"] = in.Title
	}
	if len(in.Permission) > 0 {
		body["permissions"] = V2Rules(in.Permission)
	}
	if in.ParentID != "" {
		body["parentID"] = in.ParentID
	} else if dir := firstNonEmpty(in.Directory, requestDirectory(r)); dir != "" {
		body["location"] = map[string]any{"directory": dir}
	}
	var resp data[map[string]any]
	err := c.call(r.Context(), http.MethodPost, "/api/session", nil, body, &resp)
	return finish(r, http.StatusOK, V1Session(resp.Data), err)
}

func getSession(c *compat, r *http.Request, m []string) (*http.Response, error) {
	s, err := c.session(r.Context(), m[1])
	return finish(r, http.StatusOK, V1Session(s), err)
}

func patchSession(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in map[string]any
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if t, ok := in["title"].(string); ok {
		body["title"] = t
	}
	if p, ok := in["permission"].([]any); ok {
		body["permissions"] = V2Rules(p)
	}
	if err := c.call(r.Context(), http.MethodPatch, "/api/session/"+url.PathEscape(m[1]), nil, body, nil); err != nil {
		return finish(r, 0, nil, err)
	}
	return getSession(c, r, m)
}

func deleteSession(c *compat, r *http.Request, m []string) (*http.Response, error) {
	err := c.call(r.Context(), http.MethodDelete, "/api/session/"+url.PathEscape(m[1]), nil, nil, nil)
	return finish(r, http.StatusOK, true, err)
}

// messages returns a session's full history in v1 shape, oldest first.
func (c *compat) messages(ctx context.Context, sessionID string) ([]V1Message, error) {
	out := []V1Message{}
	q := url.Values{"order": {"asc"}, "limit": {"200"}}
	for {
		var resp struct {
			Data   []map[string]any `json:"data"`
			Cursor struct {
				Next string `json:"next"`
			} `json:"cursor"`
		}
		if err := c.call(ctx, http.MethodGet, "/api/session/"+url.PathEscape(sessionID)+"/message", q, nil, &resp); err != nil {
			return nil, err
		}
		for _, msg := range resp.Data {
			if v1, ok := ConvertMessage(sessionID, msg); ok {
				out = append(out, v1)
			}
		}
		if resp.Cursor.Next == "" || len(resp.Data) == 0 {
			return out, nil
		}
		q = url.Values{"cursor": {resp.Cursor.Next}}
	}
}

func getMessages(c *compat, r *http.Request, m []string) (*http.Response, error) {
	msgs, err := c.messages(r.Context(), m[1])
	return finish(r, http.StatusOK, msgs, err)
}

func abort(c *compat, r *http.Request, m []string) (*http.Response, error) {
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/interrupt", nil, nil, nil)
	return finish(r, http.StatusOK, true, err)
}

func revert(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		MessageID string `json:"messageID"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	if err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/revert/stage", nil,
		map[string]any{"messageID": in.MessageID, "files": true}, nil); err != nil {
		return finish(r, 0, nil, err)
	}
	return getSession(c, r, m)
}

func unrevert(c *compat, r *http.Request, m []string) (*http.Response, error) {
	if err := c.call(r.Context(), http.MethodDelete, "/api/session/"+url.PathEscape(m[1])+"/revert", nil, nil, nil); err != nil {
		return finish(r, 0, nil, err)
	}
	return getSession(c, r, m)
}

func summarize(c *compat, r *http.Request, m []string) (*http.Response, error) {
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/compact", nil, map[string]any{}, nil)
	return finish(r, http.StatusOK, true, err)
}

func fork(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		MessageID string `json:"messageID"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if in.MessageID != "" {
		body["before"] = in.MessageID
	}
	var resp data[map[string]any]
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/fork", nil, body, &resp)
	return finish(r, http.StatusOK, V1Session(resp.Data), err)
}

func moveSession(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	var in struct {
		SessionID   string `json:"sessionID"`
		Destination struct {
			Directory string `json:"directory"`
		} `json:"destination"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(in.SessionID)+"/move", nil,
		map[string]any{"directory": in.Destination.Directory}, nil)
	return finish(r, http.StatusOK, true, err)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
