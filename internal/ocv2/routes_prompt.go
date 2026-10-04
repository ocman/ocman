package ocv2

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type v1Part struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URL      string `json:"url"`
	Mime     string `json:"mime"`
	Filename string `json:"filename"`
}

type v1ModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Variant    string `json:"variant"`
}

// v2 takes agent and model as session state, not per prompt: switch
// them first, and only when they differ, since every switch is recorded
// in the transcript.
func (c *compat) selectAgentModel(ctx context.Context, sessionID, agent string, model *v1ModelRef) error {
	if agent == "" && (model == nil || model.ModelID == "") {
		return nil
	}
	s, err := c.session(ctx, sessionID)
	if err != nil {
		return err
	}
	path := "/api/session/" + url.PathEscape(sessionID)
	if agent != "" && agent != str(s, "agent") {
		if err := c.call(ctx, http.MethodPost, path+"/agent", nil, map[string]any{"agent": agent}, nil); err != nil {
			return err
		}
	}
	if model == nil || model.ModelID == "" {
		return nil
	}
	ref := map[string]any{"providerID": model.ProviderID, "id": model.ModelID}
	variant := model.Variant
	if variant == "default" {
		variant = ""
	}
	if variant != "" {
		ref["variant"] = variant
	}
	cur := obj(s, "model")
	if str(cur, "providerID") == model.ProviderID && str(cur, "id") == model.ModelID && str(cur, "variant") == variant {
		return nil
	}
	return c.call(ctx, http.MethodPost, path+"/model", nil, map[string]any{"model": ref}, nil)
}

func promptBody(parts []v1Part) map[string]any {
	var texts []string
	files := []any{}
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "file":
			f := map[string]any{"uri": p.URL}
			if p.Filename != "" {
				f["name"] = p.Filename
			}
			files = append(files, f)
		}
	}
	body := map[string]any{"text": strings.Join(texts, "\n\n")}
	if len(files) > 0 {
		body["files"] = files
	}
	return body
}

func promptAsync(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		Parts    []v1Part    `json:"parts"`
		Model    *v1ModelRef `json:"model"`
		Agent    string      `json:"agent"`
		Delivery string      `json:"delivery"` // ocman extension, see SendMessageRequest.Delivery
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	if err := c.selectAgentModel(r.Context(), m[1], in.Agent, in.Model); err != nil {
		return finish(r, 0, nil, err)
	}
	body := promptBody(in.Parts)
	if in.Delivery == "queue" || in.Delivery == "steer" {
		body["delivery"] = in.Delivery
	}
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/prompt", nil, body, nil)
	return finish(r, http.StatusNoContent, nil, err)
}

// postMessageSync emulates v1's synchronous POST /session/{id}/message:
// prompt, wait for the session to go idle, return the last reply.
func postMessageSync(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		Parts []v1Part    `json:"parts"`
		Model *v1ModelRef `json:"model"`
		Agent string      `json:"agent"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	if err := c.selectAgentModel(ctx, m[1], in.Agent, in.Model); err != nil {
		return finish(r, 0, nil, err)
	}
	path := "/api/session/" + url.PathEscape(m[1])
	if err := c.call(ctx, http.MethodPost, path+"/prompt", nil, promptBody(in.Parts), nil); err != nil {
		return finish(r, 0, nil, err)
	}
	if err := c.waitIdle(ctx, m[1]); err != nil {
		return finish(r, 0, nil, err)
	}
	msgs, err := c.messages(ctx, m[1])
	if err != nil {
		return finish(r, 0, nil, err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Info["role"] == "assistant" {
			return reply(r, http.StatusOK, msgs[i]), nil
		}
	}
	return reply(r, http.StatusOK, V1Message{Info: map[string]any{"role": "assistant"}, Parts: []map[string]any{}}), nil
}

// waitIdle blocks until the session has no active execution. The
// experimental wait endpoint is used when present; otherwise poll.
func (c *compat) waitIdle(ctx context.Context, sessionID string) error {
	if c.call(ctx, http.MethodPost, "/api/experimental/session/"+url.PathEscape(sessionID)+"/wait", nil, nil, nil) == nil {
		return nil
	}
	for {
		var resp data[map[string]any]
		if err := c.call(ctx, http.MethodGet, "/api/session/active", nil, nil, &resp); err != nil {
			return err
		}
		if _, busy := resp.Data[sessionID]; !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func runShell(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		Command string `json:"command"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/shell", nil,
		map[string]any{"command": in.Command}, nil)
	return finish(r, http.StatusOK, map[string]any{}, err)
}

func runCommand(c *compat, r *http.Request, m []string) (*http.Response, error) {
	var in struct {
		Command   string `json:"command"`
		Arguments string `json:"arguments"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
	}
	if err := readBody(r, &in); err != nil {
		return nil, err
	}
	var model *v1ModelRef
	if p, rest, ok := strings.Cut(in.Model, "/"); ok {
		id, variant, _ := strings.Cut(rest, "#")
		model = &v1ModelRef{ProviderID: p, ModelID: id, Variant: variant}
	}
	if err := c.selectAgentModel(r.Context(), m[1], in.Agent, model); err != nil {
		return finish(r, 0, nil, err)
	}
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/command", nil,
		map[string]any{"name": in.Command, "text": in.Arguments}, nil)
	return finish(r, http.StatusOK, map[string]any{}, err)
}
