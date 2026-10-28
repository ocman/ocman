package server

import (
	"context"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
)

const sendPreparationTimeout = 10 * time.Second

// sendNow is the shared direct-send seam for composer, lifecycle and queue.
func (s *Server) sendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	send, err := s.prepareSendNow(ctx, platformID, req)
	if err != nil {
		return err
	}
	return send(ctx)
}

// Read/model preparation uses the caller's cancellable context; the returned
// delivery may use an uncancelled receipt context once a queue admits it.
func (s *Server) prepareSendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) (func(context.Context) error, error) {
	read, cancel := context.WithTimeout(ctx, sendPreparationTimeout)
	send, err := s.sessions.PrepareSendMessage(read, platformID, req)
	cancel()
	if err != nil {
		return nil, err
	}
	return func(delivery context.Context) error {
		err := send(delivery)
		// Native inbox admission is never replayed on an uncertain response.
		if err == nil || req.Delivery == "queue" || !errors.Is(err, platforms.ErrPlatformUnreachable) {
			return err
		}
		// Recovery reads and launches still use the preparation lifetime.
		read, cancel := context.WithTimeout(ctx, sendPreparationTimeout)
		defer cancel()
		if read.Err() != nil || !s.relaunchOpencodeForSession(read, platformID, req.SessionID) {
			return err
		}
		retry, prepareErr := s.sessions.PrepareSendMessage(read, platformID, req)
		if prepareErr != nil {
			return prepareErr
		}
		return retry(delivery)
	}, nil
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
	owner, _ := platforms.SplitPlatformID(string(adapter.ID()))
	host, ok := s.router().LookupRemote(owner)
	if !ok {
		log.WithFields(log.Fields{"sessionID": sessionID, "remoteId": owner}).Warn("not relaunching opencode: session owner is not connected")
		return false
	}
	_, base := platforms.SplitPlatformID(platformID)
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
