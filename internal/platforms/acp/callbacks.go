package acpexec

import (
	"context"
	"fmt"

	acp "github.com/coder/acp-go-sdk"
)

// Handlers receive ordered session updates and concurrent permission requests.
// Update must finish recording an event before returning. Permission must honor
// ctx, which is cancelled when the request or connection ends. Neither callback
// should retain SDK-owned mutable data without copying it.
type Handlers struct {
	Update      func(context.Context, acp.SessionNotification) error
	Permission  func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	Elicitation func(context.Context, ElicitationRequest) (acp.UnstableCreateElicitationResponse, error)
}

type callbacks struct {
	handlers Handlers
	failed   func(error)
}

func (c callbacks) SessionUpdate(ctx context.Context, p acp.SessionNotification) error {
	err := c.handlers.Update(ctx, p)
	if err != nil && c.failed != nil {
		c.failed(err)
	}
	return err
}

func (c callbacks) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	cancelled := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	if ctx.Err() != nil || c.handlers.Permission == nil {
		return cancelled, nil
	}
	result, err := c.handlers.Permission(ctx, p)
	if err != nil {
		return acp.RequestPermissionResponse{}, err
	}
	if ctx.Err() != nil {
		return cancelled, nil
	}
	if err := result.Outcome.Validate(); err != nil {
		return acp.RequestPermissionResponse{}, fmt.Errorf("invalid ACP permission outcome: %w", err)
	}
	if selected := result.Outcome.Selected; selected != nil {
		for _, option := range p.Options {
			if option.OptionId == selected.OptionId {
				return result, nil
			}
		}
		return acp.RequestPermissionResponse{}, fmt.Errorf("ACP permission option was not offered")
	}
	return result, nil
}
