package sessionlifecycle

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func (m *Module) deliverFirst(ctx context.Context, req startRequest, platform, sessionID string) error {
	hostsvc.ReportProgress(ctx, hostsvc.StepPrompt, hostsvc.StepActive)
	var err error
	switch {
	case req.Command != nil:
		err = m.d.Sessions().ExecuteCommand(ctx, platform, platforms.ExecuteCommandRequest{SessionID: sessionID, Command: req.Command.Command, Arguments: req.Command.Arguments, Model: req.Send.Model, Agent: req.Send.Agent, Reasoning: req.Send.Reasoning})
	case req.Shell != nil:
		err = m.d.Sessions().RunShell(ctx, platform, platforms.RunShellRequest{SessionID: sessionID, Command: req.Shell.Command, Agent: req.Send.Agent})
	default:
		images := make([]platforms.ImageAttachment, 0, len(req.Send.Images))
		for _, img := range req.Send.Images {
			images = append(images, platforms.ImageAttachment{URL: img.URL, Mime: img.Mime})
		}
		err = m.d.SendNow(ctx, platform, platforms.SendMessageRequest{SessionID: sessionID, Message: req.Send.Message, Images: images,
			Model: req.Send.Model, Agent: req.Send.Agent, Reasoning: req.Send.Reasoning})
	}
	hostsvc.FinishStep(ctx, hostsvc.StepPrompt, err)
	return err
}
