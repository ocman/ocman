package sessionsvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type nativeSessionKey struct {
	platform platforms.ID
	id       string
}

func physicalSource(ctx context.Context, p platforms.Platform) (platforms.HistoryIdentity, error) {
	reader, ok := p.(interface {
		HistoryIdentity(context.Context) (platforms.HistoryIdentity, error)
	})
	if !ok {
		return platforms.HistoryIdentity{}, fmt.Errorf("platform %s has no physical session source", p.ID())
	}
	source, err := reader.HistoryIdentity(ctx)
	if err != nil {
		return source, err
	}
	if source.Platform != string(p.ID()) || source.SourceKey == "" {
		return source, errors.New("invalid session source identity")
	}
	return source, nil
}

func (s *Service) catalogueParent(ctx context.Context, p platforms.Platform, nativeID string) (sessioncatalog.Session, error) {
	if s.identities.store == nil {
		return sessioncatalog.Session{}, errors.New("session catalogue unavailable")
	}
	source, err := physicalSource(ctx, p)
	if err != nil {
		return sessioncatalog.Session{}, err
	}
	b := sessioncatalog.Binding{OwnerID: s.identities.ownerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: nativeID}
	row, err := s.identities.store.FindBinding(ctx, b)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		summary, summaryErr := platforms.ReadSessionSummary(ctx, p, nativeID)
		if summaryErr != nil {
			return row, summaryErr
		}
		if summary == nil || summary.ID != nativeID {
			return row, platforms.ErrNotFound
		}
		_, err = s.identities.store.Reconcile(ctx, sessioncatalog.Source{OwnerID: b.OwnerID, Platform: b.Platform, SourceKey: b.SourceKey},
			[]sessioncatalog.NativeSession{{ID: nativeID, ParentID: summary.ParentID, Directory: summary.Directory, Title: summary.Title, CreatedAt: summary.TimeCreated}})
		if err != nil {
			return row, err
		}
		row, err = s.identities.store.FindBinding(ctx, b)
		if errors.Is(err, sessioncatalog.ErrNotFound) {
			return row, sessioncatalog.ErrCreating
		}
	}
	if err != nil {
		return row, err
	}
	if row.CreationState != sessioncatalog.Ready {
		return row, sessioncatalog.ErrCreating
	}
	return row, nil
}
