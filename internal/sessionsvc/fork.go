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
	var attempt *creationAttempt
	if remoteID, _ := platforms.SplitPlatformID(string(p.ID())); s.identities != nil && remoteID == "" {
		parent, err := s.catalogueParent(ctx, p, req.SessionID)
		if err != nil {
			return nil, err
		}
		if intent.Creation.ParentID != "" && intent.Creation.ParentID != parent.ID {
			return nil, validation("fork parent identity mismatch")
		}
		intent.Directory, intent.Creation.ParentID = parent.Directory, parent.ID
		req.Creation = intent.Creation
		var replay *platforms.CreateSessionResponse
		attempt, replay, err = s.reserveCreation(ctx, p, intent, nil, req)
		if err != nil || replay != nil {
			return replay, err
		}
	}
	req.Creation = intent.Creation
	resp, err := p.ForkSession(ctx, req)
	if err != nil {
		return nil, attempt.nativeFailure(ctx, err)
	}
	if resp == nil || strings.TrimSpace(resp.ID) == "" {
		return nil, attempt.uncertain("", fmt.Errorf("platform %s returned no forked session", p.ID()))
	}
	if err := attempt.attach(ctx, resp.ID); err != nil {
		return nil, err
	}
	if err := attempt.bind(ctx, resp.ID); err != nil {
		return nil, err
	}
	if intent.Directory != "" {
		s.sessionDirs.Store(nativeSessionKey{p.ID(), resp.ID}, intent.Directory)
	}
	if s.hooks.SessionCreated != nil {
		s.hooks.SessionCreated(CreatedSession{ID: resp.ID, Platform: string(p.ID()), Directory: intent.Directory})
	}
	return resp, nil
}
