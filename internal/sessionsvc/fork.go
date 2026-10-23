package sessionsvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Fork reserves the child's identity and Ocman parent link before asking the
// native owner to copy history. Message IDs remain native and unchanged.
func (s *Service) Fork(ctx context.Context, platformID string, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	if req.SessionID == "" {
		return nil, validation("session ID is required")
	}
	p, finish, err := s.resolveTurn(ctx, req.SessionID, platformID)
	if err != nil {
		return nil, err
	}
	defer finish()
	intent := platforms.CreateSessionRequest{Creation: req.Creation}
	if err := prepareCreationIdentity(&intent, ""); err != nil {
		return nil, err
	}
	if intent.Creation.Configured || len(intent.Creation.PermissionRules) > 0 {
		return nil, validation("fork configuration is inherited from its parent")
	}
	unlock, err := s.lockCreation(ctx, p.ID(), intent.Creation.RequestKey)
	if err != nil {
		return nil, err
	}
	defer unlock()
	_, canonical := p.(platforms.SessionCreationCommitter)
	req.Creation = intent.Creation
	resp, err := p.ForkSession(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || strings.TrimSpace(resp.ID) == "" {
		return nil, fmt.Errorf("platform %s returned no forked session", p.ID())
	}
	if canonical && resp.Replayed {
		return resp, nil
	}
	if err := commitCreation(ctx, p, resp.ID); err != nil {
		return nil, err
	}
	if intent.Directory != "" {
		s.sessionDirs.Store(sessionCacheKey{p.ID(), resp.ID}, intent.Directory)
	}
	if s.hooks.SessionCreated != nil {
		s.hooks.SessionCreated(CreatedSession{ID: resp.ID, Platform: string(p.ID()), Directory: intent.Directory})
	}
	return resp, nil
}
