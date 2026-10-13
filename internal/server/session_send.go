package server

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	log "github.com/sirupsen/logrus"
)

// sendNow is the shared direct-send seam for composer, lifecycle and queue.
func (s *Server) sendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	err := s.sessions.SendMessage(ctx, platformID, req)
	if err == nil || !errors.Is(err, platforms.ErrPlatformUnreachable) {
		return err
	}
	if !s.relaunchOpencodeForSession(ctx, platformID, req.SessionID) {
		return err
	}
	return s.sessions.SendMessage(ctx, platformID, req)
}

// Relaunch only on the adapter's owner, never inferred directory ownership.
func (s *Server) relaunchOpencodeForSession(ctx context.Context, platformID, sessionID string) bool {
	adapter, ok := s.adapterForSession(ctx, platformID, sessionID)
	if !ok {
		return false
	}
	detail, err := adapter.Session(ctx, sessionID, 0, 0)
	if err != nil || detail == nil || detail.Session == nil || detail.Session.Directory == "" {
		return false
	}
	dir := projectRootForDirectory(detail.Session.Directory)
	owner, _ := remote.SplitPlatformID(string(adapter.ID()))
	host, ok := s.router().LookupRemote(owner)
	if !ok {
		log.WithFields(log.Fields{"sessionID": sessionID, "remoteId": owner}).Warn("not relaunching opencode: session owner is not connected")
		return false
	}
	_, base := remote.SplitPlatformID(platformID)
	res, err := host.EnsureRuntime(ctx, base, dir)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"sessionID": sessionID, "directory": dir}).Warn("relaunching opencode for unreachable session")
		return false
	}
	if res.Launched {
		log.WithFields(log.Fields{"sessionID": sessionID, "directory": dir, "endpoint": res.Endpoint}).Info("relaunched opencode for unreachable session")
	}
	return true
}

func (s *Server) adapterForSession(ctx context.Context, platformID, sessionID string) (platforms.Platform, bool) {
	if s.registry == nil {
		return nil, false
	}
	if platformID != "" {
		return s.registry.Get(platforms.ID(platformID))
	}
	return s.registry.PlatformForSession(ctx, sessionID)
}
