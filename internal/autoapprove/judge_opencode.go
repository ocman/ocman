package autoapprove

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

func (j *PermissionJudge) createSession(ctx context.Context, port, directory, title string) (string, error) {
	return (opencode.HelperClient{HTTPClient: j.httpClient}).CreateSession(ctx, port, directory, title)
}

func (j *PermissionJudge) deleteSession(ctx context.Context, port, id string) error {
	return (opencode.HelperClient{HTTPClient: j.httpClient}).DeleteSession(ctx, port, id)
}

func (j *PermissionJudge) sendPrompt(ctx context.Context, port, id, permission string, patterns []string, metadata map[string]any, sections []PromptSection) error {
	provider, model := j.model()
	return (opencode.HelperClient{HTTPClient: j.httpClient}).SendPrompt(ctx, port, id, judgePrompt(permission, patterns, metadata, sections), provider, model, judgeAgent)
}

func (j *PermissionJudge) collectResponse(ctx context.Context, port, id string) (string, error) {
	return (opencode.HelperClient{HTTPClient: j.httpClient}).CollectResponse(ctx, port, id)
}
