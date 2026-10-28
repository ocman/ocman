package pluginsmod

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// Read only lifecycle and the durable receipt before fetching a transcript.
// Failed turns still need their attention notice even if a partial reply exists.
func (m *Module) conversationReplyAlreadyRecorded(ctx context.Context, adapter platforms.Platform, key state.PluginConversationKey, sessionID string) (bool, error) {
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
	prefix, err := pluginSessionReference(ctx, adapter, sessionID)
	if err != nil {
		return false, err
	}
	return m.store.HasPluginConversationReply(ctx, key, prefix+":"+lifecycle.LatestMessageID)
}
