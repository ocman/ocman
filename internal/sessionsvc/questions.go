package sessionsvc

import (
	"context"
	"github.com/ocman/ocman/internal/platforms"
)

// RespondQuestion answers a pending question.
func (s *Service) RespondQuestion(ctx context.Context, platformID string, req platforms.RespondQuestionRequest) error {
	if req.RequestID == "" {
		return validation("question ID is required")
	}
	ctx, p, finish, err := s.resolveActivity(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	defer finish()
	return p.RespondQuestion(ctx, req)
}

// RejectQuestion rejects a pending question.
func (s *Service) RejectQuestion(ctx context.Context, platformID string, req platforms.RejectQuestionRequest) error {
	if req.RequestID == "" {
		return validation("question ID is required")
	}
	ctx, p, finish, err := s.resolveActivity(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	defer finish()
	return p.RejectQuestion(ctx, req)
}
