package server

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Analytics names the physical history instance, not just an active platform.
// Retained inactive-source identities must not resolve through today's source.
func (s *Server) historySessionPublicID(ctx context.Context, instance, nativeID string) (string, error) {
	if s.sessionCatalogue == nil {
		return nativeID, nil
	}
	if s.historyAnalytics == nil || s.historyAnalytics.Store == nil {
		return "", platforms.ErrHistoryUnavailable
	}
	metadata, err := s.historyAnalytics.Store.Metadata(ctx, statev2.Key{InstanceID: instance, SessionID: nativeID})
	if err != nil {
		return "", err
	}
	store, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		return "", err
	}
	binding := sessioncatalog.Binding{OwnerID: metadata.Instance.OwnerID, Platform: metadata.Instance.Platform, SourceKey: metadata.Instance.SourceKey, NativeID: nativeID}
	row, err := store.FindBinding(ctx, binding)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		source := sessioncatalog.Source{OwnerID: binding.OwnerID, Platform: binding.Platform, SourceKey: binding.SourceKey}
		if metadata.Purged {
			err = store.Scrub(ctx, source, nativeID)
		} else {
			_, err = store.Reconcile(ctx, source, []sessioncatalog.NativeSession{{ID: nativeID, Title: metadata.Session.Title, Directory: metadata.Session.Directory, ParentID: metadata.Session.ParentID, CreatedAt: metadata.Session.CreatedAt}})
		}
		if err == nil {
			row, err = store.FindBinding(ctx, binding)
		}
	}
	if err != nil {
		return "", err
	}
	return row.ID, nil
}
