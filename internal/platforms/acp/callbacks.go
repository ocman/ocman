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
	Update     func(context.Context, acp.SessionNotification) error
	Permission func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

type callbacks struct {
	handlers Handlers
	failed   func(error)
}

var _ acp.Client = callbacks{}

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

func (callbacks) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
}

func (callbacks) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
}

func (callbacks) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalCreate)
}

func (callbacks) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalKill)
}

func (callbacks) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalOutput)
}

func (callbacks) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalRelease)
}

func (callbacks) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound(acp.ClientMethodTerminalWaitForExit)
}
