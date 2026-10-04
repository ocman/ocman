package ocv2

import (
	"context"
	"errors"
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

// errQueuedSelection: a held (delivery "queue") prompt asked for an agent
// or model the session is not on. v2 keeps agent and model as session
// state, not per inbox item, so switching now would change the running
// turn. Answered as 412 so the adapter falls back to ocman's queue,
// which applies the selection when it delivers.
var errQueuedSelection = errors.New("queued prompt needs an agent or model switch")

func normVariant(v string) string {
	if v == "default" {
		return ""
	}
	return v
}

// v2 takes agent and model as session state, not per prompt: switch
// them first, and only when they differ, since every switch is recorded
// in the transcript. With queued set nothing is switched; a needed
// switch is reported as errQueuedSelection.
func (c *compat) selectAgentModel(ctx context.Context, sessionID, agent string, model *v1ModelRef, queued bool) error {
	if agent == "" && (model == nil || model.ModelID == "") {
		return nil
	}
	s, err := c.session(ctx, sessionID)
	if err != nil {
		return err
	}
	switchAgent := agent != "" && agent != str(s, "agent")
	var ref map[string]any
	if model != nil && model.ModelID != "" {
		variant := normVariant(model.Variant)
		cur := obj(s, "model")
		if str(cur, "providerID") != model.ProviderID || str(cur, "id") != model.ModelID || normVariant(str(cur, "variant")) != variant {
			ref = map[string]any{"providerID": model.ProviderID, "id": model.ModelID}
			if variant != "" {
				ref["variant"] = variant
			}
		}
	}
	if queued && (switchAgent || ref != nil) {
		return errQueuedSelection
	}
	path := "/api/session/" + url.PathEscape(sessionID)
	if switchAgent {
		if err := c.call(ctx, http.MethodPost, path+"/agent", nil, map[string]any{"agent": agent}, nil); err != nil {
			return err
		}
	}
	if ref == nil {
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
	queued := in.Delivery == "queue"
	if err := c.selectAgentModel(r.Context(), m[1], in.Agent, in.Model, queued); err != nil {
		if errors.Is(err, errQueuedSelection) {
			return reply(r, http.StatusPreconditionFailed, map[string]any{
				"name": "QueuedSelectionError", "data": map[string]any{"message": err.Error()},
			}), nil
		}
		return finish(r, 0, nil, err)
	}
	body := promptBody(in.Parts)
	if queued || in.Delivery == "steer" {
		body["delivery"] = in.Delivery
	}
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/prompt", nil, body, nil)
	return finish(r, http.StatusNoContent, nil, err)
}

// postMessageSync emulates v1's synchronous POST /session/{id}/message:
// prompt, then wait until the turn that prompt started has finished, and
// return its last reply.
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
	if err := c.selectAgentModel(ctx, m[1], in.Agent, in.Model, false); err != nil {
		return finish(r, 0, nil, err)
	}
	path := "/api/session/" + url.PathEscape(m[1])
	var admitted data[map[string]any]
	if err := c.call(ctx, http.MethodPost, path+"/prompt", nil, promptBody(in.Parts), &admitted); err != nil {
		return finish(r, 0, nil, err)
	}
	answer, err := c.waitReply(ctx, m[1], str(admitted.Data, "id"))
	if err != nil {
		return finish(r, 0, nil, err)
	}
	return reply(r, http.StatusOK, answer), nil
}

// waitReply blocks until the session is idle and holds an assistant
// message newer than the admitted prompt (message ids are ascending), so
// an idle reading taken before v2 scheduled the turn is never mistaken
// for its end. Bounded by ctx.
func (c *compat) waitReply(ctx context.Context, sessionID, promptID string) (V1Message, error) {
	// The wait barrier saves polling while the turn runs; its answer is
	// verified below either way.
	_ = c.call(ctx, http.MethodPost, "/api/experimental/session/"+url.PathEscape(sessionID)+"/wait", nil, nil, nil)
	for {
		var active data[map[string]any]
		if err := c.call(ctx, http.MethodGet, "/api/session/active", nil, nil, &active); err != nil {
			return V1Message{}, err
		}
		if _, busy := active.Data[sessionID]; !busy {
			msgs, err := c.messages(ctx, sessionID)
			if err != nil {
				return V1Message{}, err
			}
			for i := len(msgs) - 1; i >= 0; i-- {
				id, _ := msgs[i].Info["id"].(string)
				if msgs[i].Info["role"] == "assistant" && id > promptID {
					return msgs[i], nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return V1Message{}, ctx.Err()
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
	if err := c.selectAgentModel(r.Context(), m[1], in.Agent, model, false); err != nil {
		return finish(r, 0, nil, err)
	}
	err := c.call(r.Context(), http.MethodPost, "/api/session/"+url.PathEscape(m[1])+"/command", nil,
		map[string]any{"name": in.Command, "text": in.Arguments}, nil)
	return finish(r, http.StatusOK, map[string]any{}, err)
}
