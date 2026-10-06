package server

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Read only lifecycle and the durable receipt before fetching a transcript.
// Failed turns still need their attention notice even if a partial reply exists.
func (s *Server) conversationReplyAlreadyRecorded(ctx context.Context, adapter platforms.Platform, key state.PluginConversationKey, sessionID string) (bool, error) {
	reader, ok := adapter.(platforms.LifecycleReader)
	if !ok {
		return false, nil
	}
	lifecycle, err := reader.SessionLifecycle(ctx, sessionID)
	if errors.Is(err, platforms.ErrUnsupported) {
		return false, nil
	}
	if err != nil || lifecycle == nil {
		return false, err
	}
	if !conversationReplyReady(lifecycle.Status) {
		return true, nil
	}
	if lifecycle.Status == db.StatusError || lifecycle.LatestMessageRole != "assistant" || lifecycle.LatestMessageID == "" {
		return false, nil
	}
	return s.stateDB.HasPluginConversationReply(ctx, key, sessionID+":"+lifecycle.LatestMessageID)
}
