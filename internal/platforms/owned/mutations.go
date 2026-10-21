package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.SendMessage(ctx, req)
}
func (a *Adapter) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.ExecuteCommand(ctx, req)
}
func (a *Adapter) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.RunShell(ctx, req)
}
func (a *Adapter) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.RespondPermission(ctx, req)
}
func (a *Adapter) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.RespondQuestion(ctx, req)
}
func (a *Adapter) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.RejectQuestion(ctx, req)
}
func (a *Adapter) Abort(ctx context.Context, req platforms.AbortRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.Abort(ctx, req)
}
func (a *Adapter) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.RenameSession(ctx, req)
}
func (a *Adapter) PermissionRules(ctx context.Context, id string) ([]platforms.PermissionRule, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.native.PermissionRules(ctx, nativeID)
}
func (a *Adapter) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.SetPermissionRules(ctx, req)
}
func (a *Adapter) Compact(ctx context.Context, req platforms.CompactRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.Compact(ctx, req)
}
func (a *Adapter) MoveSession(ctx context.Context, req platforms.MoveSessionRequest) error {
	id, err := a.nativeID(ctx, req.SessionID)
	if err != nil {
		return err
	}
	req.SessionID = id
	return a.native.MoveSession(ctx, req)
}
