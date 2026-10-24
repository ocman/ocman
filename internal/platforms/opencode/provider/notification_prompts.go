package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// NotificationPrompts reads observed prompts, including descendants, without
// starting an authoritative upstream reconciliation on every notify poll.
func (a *Adapter) NotificationPrompts(ctx context.Context, id string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	permissions, err := a.listObservedPromptsChecked(ctx, "permission", id)
	if err != nil {
		return nil, nil, err
	}
	questions, err := a.listObservedPromptsChecked(ctx, "question", id)
	return permissions, questions, err
}
