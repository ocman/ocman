package server

import (
	"context"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/db"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
)

func (s sessionMCPService) createCaller(ctx context.Context, in internalmcp.CreateSessionRequest) (*platforms.SessionDetail, error) {
	if in.Caller != nil {
		row, err := s.server.sessions.LookupLocalCaller(ctx, in.Caller.Kind, in.Caller.SessionID)
		if err != nil {
			return nil, fmt.Errorf("%w: MCP caller context could not be resolved; do not retry without caller context", internalmcp.ErrInvalidSessionCreate)
		}
		if in.SessionID != "" && in.SessionID != row.ID && in.SessionID != in.Caller.SessionID {
			claimed, err := s.server.sessions.LookupLocalCaller(ctx, in.Caller.Kind, in.SessionID)
			if err != nil || claimed.ID != row.ID {
				return nil, fmt.Errorf("%w: session_id conflicts with MCP caller context", internalmcp.ErrInvalidSessionCreate)
			}
		}
		return &platforms.SessionDetail{Session: row}, nil
	}
	if in.SessionID == "" {
		return nil, nil
	}
	if !s.server.sessions.CanonicalSessionReferences(in.Platform) && !sessioncatalog.IsID(in.SessionID) {
		return s.GetSession(ctx, in.Platform, in.SessionID, 1)
	}
	info, err := s.server.sessions.LookupSessionReference(ctx, in.Platform, in.SessionID)
	if err != nil {
		return nil, err
	}
	store, err := sessioncatalog.New(s.server.stateDB)
	if err != nil {
		return nil, err
	}
	row, err := store.GetCanonical(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	platform := s.identityPlatform(ctx, info)
	if in.Directory == "" && row.Directory == "" {
		// Remote catalogue mirrors omit display metadata. Ask only that
		// authenticated owner when its directory is needed for the target.
		return s.GetSession(ctx, platform, info.ID, 1)
	}
	return &platforms.SessionDetail{Session: &db.Session{ID: row.ID, Platform: platform, Directory: row.Directory}}, nil
}

func (s sessionMCPService) recordCreator(ctx context.Context, created internalmcp.CreatedSession, caller *db.Session) (internalmcp.CreatedSession, error) {
	// Allocation already succeeded. Cancellation must not erase the child's
	// provenance; this bounded final write never retries external allocation.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if sessioncatalog.IsID(created.SessionID) {
		// The requested kind may have selected a named default account. Record
		// the child's actual immutable source route, not that mutable alias.
		scope := ""
		if owner, _ := platforms.SplitPlatformID(created.Platform); owner != "" {
			scope = created.Platform
		}
		info, err := s.server.sessions.LookupSessionReference(writeCtx, scope, created.SessionID)
		if err != nil {
			return created, fmt.Errorf("resolving created session account: %w", err)
		}
		created.Platform = s.identityPlatform(writeCtx, info)
	}
	if err := s.server.stateDB.RecordSessionCreator(writeCtx, state.SessionCreator{
		Platform: created.Platform, SessionID: created.SessionID,
		CreatorPlatform: caller.Platform, CreatorSessionID: caller.ID,
	}); err != nil {
		return created, fmt.Errorf("recording session creator: %w", err)
	}
	return created, nil
}
