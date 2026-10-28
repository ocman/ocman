package owned

import (
	"context"
	"maps"

	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) prompts(ctx context.Context, rows []platforms.LivePrompt) ([]platforms.LivePrompt, error) {
	if rows == nil {
		return nil, nil
	}
	out := make([]platforms.LivePrompt, len(rows))
	for i, row := range rows {
		out[i] = maps.Clone(row)
		for _, field := range []string{"sessionID", "sessionId", "session_id"} {
			if id, ok := row[field].(string); ok && id != "" {
				identity, err := a.canonical(ctx, id)
				if err != nil {
					return nil, err
				}
				out[i][field] = identity.ID
			}
		}
	}
	return out, nil
}

func (a *Adapter) ListPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := a.native.ListPermissions(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	return a.prompts(ctx, rows)
}

func (a *Adapter) ListQuestions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := a.native.ListQuestions(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	return a.prompts(ctx, rows)
}
