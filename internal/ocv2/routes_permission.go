package ocv2

import (
	"context"
	"net/http"
	"net/url"
	"sync"
)

func init() {
	handle(http.MethodGet, `/permission`, listPermissions)
	handle(http.MethodPost, `/permission/([^/]+)/reply`, replyPermission)
	handle(http.MethodGet, `/question`, listQuestions)
	handle(http.MethodPost, `/question/([^/]+)/reply`, replyQuestion)
	handle(http.MethodPost, `/question/([^/]+)/reject`, rejectQuestion)
}

// v1 replies address a prompt by request id alone; v2 routes are
// session-scoped. Remember each prompt's session as it is listed or
// streamed. ponytail: unbounded, entries are small and prompts rare.
var promptSessions sync.Map // request id -> session id

func rememberPrompt(id, sessionID string) {
	if id != "" && sessionID != "" {
		promptSessions.Store(id, sessionID)
	}
}

func (c *compat) permissionRequests(ctx context.Context, dir string) ([]any, error) {
	var resp data[[]any]
	err := c.call(ctx, http.MethodGet, "/api/permission/request", location(dir), nil, &resp)
	for _, p := range resp.Data {
		m, _ := p.(map[string]any)
		rememberPrompt(str(m, "id"), str(m, "sessionID"))
	}
	return resp.Data, err
}

func (c *compat) questionForms(ctx context.Context, dir string) ([]map[string]any, error) {
	var resp data[[]any]
	err := c.call(ctx, http.MethodGet, "/api/form", location(dir), nil, &resp)
	var out []map[string]any
	for _, f := range resp.Data {
		m, _ := f.(map[string]any)
		if IsQuestionForm(m) {
			rememberPrompt(str(m, "id"), str(m, "sessionID"))
			out = append(out, m)
		}
	}
	return out, err
}

func listPermissions(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	reqs, err := c.permissionRequests(r.Context(), requestDirectory(r))
	out := make([]any, 0, len(reqs))
	for _, p := range reqs {
		m, _ := p.(map[string]any)
		out = append(out, V1Permission(m))
	}
	return finish(r, http.StatusOK, out, err)
}

func listQuestions(c *compat, r *http.Request, _ []string) (*http.Response, error) {
	forms, err := c.questionForms(r.Context(), requestDirectory(r))
	out := make([]any, 0, len(forms))
	for _, f := range forms {
		out = append(out, V1Question(f))
	}
	return finish(r, http.StatusOK, out, err)
}

// promptSession resolves the session owning a prompt, refreshing the
// directory's list on a miss. "" means the prompt is already gone.
func (c *compat) promptSession(ctx context.Context, id, dir string, question bool) string {
	if s, ok := promptSessions.Load(id); ok {
		return s.(string)
	}
	if question {
		_, _ = c.questionForms(ctx, dir)
	} else {
		_, _ = c.permissionRequests(ctx, dir)
	}
	if s, ok := promptSessions.Load(id); ok {
		return s.(string)
	}
	return ""
}

func gone(r *http.Request) (*http.Response, error) {
	return reply(r, http.StatusNotFound, map[string]any{"name": "NotFoundError", "data": map[string]any{"message": "prompt not found"}}), nil
}

func replyPermission(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		Reply   string `json:"reply"`
		Message string `json:"message"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	session := c.promptSession(r.Context(), m[1], requestDirectory(r), false)
	if session == "" {
		return gone(r)
	}
	body := map[string]any{"decision": in.Reply}
	if in.Message != "" {
		body["message"] = in.Message
	}
	err := c.call(r.Context(), http.MethodPost,
		"/api/session/"+url.PathEscape(session)+"/permission/"+url.PathEscape(m[1])+"/reply", nil, body, nil)
	return finish(r, http.StatusOK, true, err)
}

func (c *compat) questionForm(ctx context.Context, id, dir string) (map[string]any, string) {
	session := c.promptSession(ctx, id, dir, true)
	if session == "" {
		return nil, ""
	}
	var resp data[map[string]any]
	if c.call(ctx, http.MethodGet, "/api/session/"+url.PathEscape(session)+"/form/"+url.PathEscape(id), nil, nil, &resp) != nil {
		return nil, session
	}
	return resp.Data, session
}

func replyQuestion(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		Answers [][]string `json:"answers"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	form, session := c.questionForm(r.Context(), m[1], requestDirectory(r))
	if form == nil {
		return gone(r)
	}
	err := c.call(r.Context(), http.MethodPost,
		"/api/session/"+url.PathEscape(session)+"/form/"+url.PathEscape(m[1])+"/reply", nil,
		map[string]any{"answer": FormAnswer(form, in.Answers)}, nil)
	return finish(r, http.StatusOK, true, err)
}

func rejectQuestion(c *compat, r *http.Request, m []string) (*http.Response, error) {
	session := c.promptSession(r.Context(), m[1], requestDirectory(r), true)
	if session == "" {
		return gone(r)
	}
	err := c.call(r.Context(), http.MethodDelete,
		"/api/session/"+url.PathEscape(session)+"/form/"+url.PathEscape(m[1]), nil, nil, nil)
	return finish(r, http.StatusOK, true, err)
}
