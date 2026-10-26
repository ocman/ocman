package server

import (
	"context"
	"io"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// ProxyRemoteSessionEvents runs the owner-side autoapprove tee and synthetic
// event sink before bytes enter the gRPC tunnel. The hub forwards them only.
func (s *Server) ProxyRemoteSessionEvents(ctx context.Context, _ string, sessionID string, adapter platforms.Platform, rawWriter, syntheticWriter io.Writer, flush func()) error {
	return s.proxyOwnerSessionEvents(ctx, sessionID, adapter, rawWriter, syntheticWriter, flush)
}

func (s *Server) proxyOwnerSessionEvents(ctx context.Context, sessionID string, adapter platforms.Platform, rawWriter, syntheticWriter io.Writer, flush func()) error {
	var sinkMu sync.Mutex
	sinks := make(map[string]*autoapprove.Sink)
	registerSink := func(id string) {
		if id == "" {
			return
		}
		sinkMu.Lock()
		defer sinkMu.Unlock()
		if sinks[id] == nil {
			sinks[id] = s.aaSvc().RegisterSink(id, syntheticWriter, flush)
		}
	}
	registerSink(sessionID)
	// Stored history trails this stream; tell its viewers when it catches up.
	defer s.historyCommits.forward(ctx, sessionID, syntheticWriter, flush)()
	defer func() {
		sinkMu.Lock()
		defer sinkMu.Unlock()
		for id, sink := range sinks {
			s.aaSvc().UnregisterSink(id, sink)
		}
	}()
	if entries, err := adapter.ListPermissions(ctx, sessionID); err != nil {
		log.WithError(err).WithField("sessionID", sessionID).Debug("SSE proxy: listing pending permissions for replay")
	} else {
		for _, entry := range entries {
			registerSink(autoapprove.PromptSessionID(entry, sessionID))
		}
		s.autoapproveModule().EnsurePending(adapter, sessionID, entries)
	}

	tee := &autoapprove.Tee{
		W:     rawWriter,
		Flush: flush,
		OnPromptAsked: func(_ string, kind string, prompt platforms.LivePrompt) {
			if kind == "permission" {
				s.aaSvc().ObservePermissionPrompt(adapter.ID(), sessionID, prompt)
			}
		},
		OnPermission: func(evtSessionID, permissionID, permission string, patterns []string, metadata map[string]any) {
			registerSink(evtSessionID)
			s.aaSvc().Ensure(adapter.ID(), adapter, evtSessionID, permissionID, permission, patterns, metadata)
		},
		OnPermissionReplied: func(evtSessionID, permissionID, reply string) {
			registerSink(evtSessionID)
			s.aaSvc().HandlePermissionReplied(ctx, evtSessionID, permissionID, reply)
			s.resolvePermissionInbox(ctx, string(adapter.ID()), evtSessionID, permissionID)
		},
	}
	return adapter.ProxyEvents(ctx, sessionID, tee, flush)
}
