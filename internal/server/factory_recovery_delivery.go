package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (l factoryImplementationLauncher) RecoveryResponseDelivered(ctx context.Context, session factory.PlanningSession, gateID string) (bool, error) {
	platform, ok := l.server.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return false, errors.New("implementation platform is unavailable")
	}
	marker := "Factory recovery response for gate " + gateID + ":"
	detail, err := platform.Session(ctx, session.ID, 20, 0)
	if err != nil {
		return false, fmt.Errorf("check Factory recovery delivery: %w", err)
	}
	if detail == nil {
		return false, errors.New("check Factory recovery delivery: session is unavailable")
	}
	for _, part := range detail.Parts {
		if bytes.Contains(part.Data, []byte(marker)) {
			return true, nil
		}
	}
	return false, nil
}

func (l factoryImplementationLauncher) ResumeImplementationSession(ctx context.Context, session factory.PlanningSession, gateID, response string) error {
	if found, err := l.RecoveryResponseDelivered(ctx, session, gateID); found || err != nil {
		return err
	}
	marker := "Factory recovery response for gate " + gateID + ":"
	prompt := fmt.Sprintf("%s\n\n%s\n\nContinue the existing Factory Issue using this response.", marker, response)
	err := l.server.sessions.SendMessage(ctx, session.Platform, platforms.SendMessageRequest{SessionID: session.ID, Message: prompt})
	if err != nil {
		// A lost HTTP reply is not a lost prompt. Confirm acceptance without resending.
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if found, _ := l.RecoveryResponseDelivered(checkCtx, session, gateID); found {
			return nil
		}
	}
	return err
}
