package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// SessionLifecycle is a runtime-safety probe of the live integration. It is
// not a persisted read and has no historical fallback.
func (a *Adapter) SessionLifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	reader, ok := a.native.(platforms.LifecycleReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	return reader.SessionLifecycle(ctx, nativeID)
}

func (a *Adapter) RefreshPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	reader, ok := a.native.(platforms.PermissionRefresher)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	rows, err := reader.RefreshPermissions(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	return a.prompts(ctx, rows)
}

func (a *Adapter) ReplyOutcomes(ctx context.Context, id string) bool {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return false
	}
	if reporter, ok := a.native.(platforms.ReplyOutcomeReporter); ok {
		return reporter.ReplyOutcomes(ctx, nativeID)
	}
	return true
}

func (a *Adapter) NotificationPrompts(ctx context.Context, id string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	var permissions, questions []platforms.LivePrompt
	if reader, ok := a.native.(interface {
		NotificationPrompts(context.Context, string) ([]platforms.LivePrompt, []platforms.LivePrompt, error)
	}); ok {
		permissions, questions, err = reader.NotificationPrompts(ctx, nativeID)
	} else {
		permissions, err = a.native.ListPermissions(ctx, nativeID)
		if err == nil {
			questions, err = a.native.ListQuestions(ctx, nativeID)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	permissions, err = a.prompts(ctx, permissions)
	if err != nil {
		return nil, nil, err
	}
	questions, err = a.prompts(ctx, questions)
	return permissions, questions, err
}
