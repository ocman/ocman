package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// WorktreeName uses the owner's small model in a temporary, tool-denied session.
// The caller adds a unique suffix and can fall back when naming is unavailable.
func WorktreeName(ctx context.Context, port, directory, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	query := "?directory=" + url.QueryEscape(directory)
	config, err := getJSON(ctx, port, "/config"+query)
	if err != nil {
		return "", err
	}
	var cfg struct {
		SmallModel string `json:"small_model"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return "", err
	}
	model := cfg.SmallModel
	// OpenCode v2's title agent picks a small model of the session's
	// provider itself; only v1 needs a fallback.
	if model == "" && !ocv2.IsV2(ctx, port) {
		model = "anthropic/claude-haiku-4-5"
	}
	var modelRef map[string]string
	if model != "" {
		provider, modelID, ok := strings.Cut(model, "/")
		if !ok || provider == "" || modelID == "" {
			return "", errors.New("invalid small_model")
		}
		modelRef = map[string]string{"providerID": provider, "modelID": modelID}
	}
	// The " subagent)" title suffix hides a parentless internal session from
	// every listing (db.scanSessionRow), like the auto-approve judge's.
	created, err := postJSONReturningLLM(ctx, port, "/session"+query, []byte(`{"title":"(worktree-name subagent)","permission":[{"permission":"*","pattern":"*","action":"deny"}]}`))
	if err != nil {
		return "", err
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &session); err != nil {
		return "", err
	}
	if session.ID == "" {
		return "", errors.New("naming session has no ID")
	}
	path := "/session/" + url.PathEscape(session.ID)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = postJSON(cleanup, port, path+"/abort"+query, nil)
		_ = sendJSON(cleanup, http.MethodDelete, port, path+query, nil)
	}()
	// Delimit the task so the title agent names the work, not these instructions.
	runes := []rune(prompt)
	if len(runes) > 2000 {
		runes = runes[:2000]
	}
	body := map[string]any{
		"agent": "title",
		"parts": []map[string]string{{"type": "text", "text": `Generate a short git branch name for the work described in <task>. Name only that task, not these naming instructions.
Return only a lowercase, hyphen-separated name of 2-6 words, at most 48 characters.
Do not answer the task, describe your next step, or use first-person phrasing. Never ask for more details or mention missing context. If the task is vague or only a link, name the investigation using the topic or source that is available. Do not invent details.
Examples: a vague diagnosis request -> diagnose-issue; a Slack link to investigate -> investigate-slack-thread. Never output need-more-details-to-diagnose or i-ll-check-the-slack-link-you-pasted.

<task>
` + string(runes) + "\n</task>"}},
	}
	if modelRef != nil {
		body["model"] = modelRef
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	answer, err := postJSONReturningLLM(ctx, port, path+"/message"+query, payload)
	if err != nil {
		return "", err
	}
	var message struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(answer, &message); err != nil {
		return "", err
	}
	for _, part := range message.Parts {
		if part.Type != "text" {
			continue
		}
		if name := branchSlug(part.Text); name != "" {
			return name, nil
		}
	}
	return "", errors.New("small model returned no branch name")
}

// branchSlug turns a generated title into a branch name: first non-empty
// line, lowercase alphanumerics joined by hyphens, at most 48 characters.
func branchSlug(title string) string {
	for _, line := range strings.Split(title, "\n") {
		name := strings.Join(strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		}), "-")
		if len(name) > 48 {
			name = strings.TrimRight(name[:48], "-")
		}
		if name != "" {
			return name
		}
	}
	return ""
}
