package conversationread

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// initialPage separates import readiness from terminal absence. Only readiness
// failures may wait for ingestion; purged and missing sources remain not found.
func (s *Service) initialPage(ctx context.Context, head *platforms.ConversationHead, id string, limit, offset int, sourceMissing bool) (*statev2.Detail, string, error) {
	instance, err := s.Store.FindInstance(ctx, s.OwnerID, head.Identity.Platform, head.Identity.SourceKey)
	if err != nil {
		if sourceMissing && errors.Is(err, statev2.ErrNotFound) {
			return nil, "", platforms.ErrNotFound
		}
		return nil, "source import is pending", nil
	}
	key := statev2.Key{InstanceID: instance.ID, SessionID: id}
	stored, purged, err := s.Store.SessionState(ctx, key)
	if err != nil {
		return nil, "stored metadata is unavailable", nil
	}
	if purged || (sourceMissing && !stored.SourceDeleted) {
		return nil, "", platforms.ErrNotFound
	}
	page, err := s.Store.ReadConversation(ctx, key, limit, offset)
	if err != nil {
		return nil, "conversation import is pending", nil
	}
	if page.Session.ReadFormat != statev2.ConversationReadFormat {
		return nil, "snapshot needs a background metadata refresh", nil
	}
	if page.ReadyVersion == "" {
		return nil, "snapshot has not passed validation", nil
	}
	return page, "", nil
}
