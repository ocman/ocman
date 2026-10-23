package owned

import (
	"context"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func physicalSource(ctx context.Context, p platforms.Platform) (platforms.HistoryIdentity, error) {
	reader, ok := p.(sourceReader)
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

func (g *IdentityGateway) catalogueParent(ctx context.Context, p platforms.Platform, nativeID string) (sessioncatalog.Session, error) {
	if g.store == nil {
		return sessioncatalog.Session{}, errors.New("session catalogue unavailable")
	}
	if canonical, ok := p.(interface {
		canonical(context.Context, string) (sessioncatalog.Session, error)
	}); ok {
		return canonical.canonical(ctx, nativeID)
	}
	return sessioncatalog.Session{}, platforms.ErrUnsupported
}

func (g *IdentityGateway) legacyPlatform(ctx context.Context, reference string) (platforms.Platform, error) {
	var selected platforms.Platform
	var lookupErr error
	for _, p := range g.registry().Platforms() {
		remote, _ := platforms.SplitPlatformID(string(p.ID()))
		if remote != "" || !CanonicalSessionIDs(p) {
			continue
		}
		if _, err := g.catalogueParent(ctx, p, reference); err != nil {
			if !errors.Is(err, platforms.ErrNotFound) && lookupErr == nil {
				lookupErr = err
			}
			continue
		}
		if selected != nil {
			return nil, sessioncatalog.ErrBindingConflict
		}
		selected = p
	}
	if lookupErr != nil {
		return nil, lookupErr
	}
	if selected == nil {
		return nil, platforms.ErrNotFound
	}
	return selected, nil
}
