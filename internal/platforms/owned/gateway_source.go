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
	source, err := physicalSource(ctx, p)
	if err != nil {
		return sessioncatalog.Session{}, err
	}
	binding := sessioncatalog.Binding{OwnerID: g.owner, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: nativeID}
	row, err := g.store.FindBinding(ctx, binding)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		summary, summaryErr := platforms.ReadSessionSummary(ctx, p, nativeID)
		if summaryErr != nil {
			return row, summaryErr
		}
		if summary == nil || summary.ID != nativeID {
			return row, platforms.ErrNotFound
		}
		_, err = g.store.Reconcile(ctx, sessioncatalog.Source{OwnerID: binding.OwnerID, Platform: binding.Platform, SourceKey: binding.SourceKey}, []sessioncatalog.NativeSession{{ID: nativeID, ParentID: summary.ParentID, Directory: summary.Directory, Title: summary.Title, CreatedAt: summary.TimeCreated}})
		if err != nil {
			return row, err
		}
		row, err = g.store.FindBinding(ctx, binding)
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

type CreationParent struct{ ID, Directory string }

func (g *IdentityGateway) legacyPlatform(ctx context.Context, reference string) (platforms.Platform, error) {
	var selected platforms.Platform
	var lookupErr error
	canonical := false
	for _, p := range g.registry().Platforms() {
		if CanonicalSessionIDs(p) {
			canonical = true
		}
		remote, _ := platforms.SplitPlatformID(string(p.ID()))
		if remote != "" || !CanonicalSessionIDs(p) {
			continue
		}
		canonical = true
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
	if canonical {
		if lookupErr != nil {
			return nil, lookupErr
		}
		if selected == nil {
			return nil, platforms.ErrNotFound
		}
		return selected, nil
	}
	p, ok := g.registry().PlatformForSession(ctx, reference)
	if !ok {
		return nil, platforms.ErrNotFound
	}
	return p, nil
}

func (g *IdentityGateway) Parent(ctx context.Context, p platforms.Platform, id string) (CreationParent, error) {
	var row sessioncatalog.Session
	var err error
	if CanonicalSessionIDs(p) {
		row, err = g.store.Get(ctx, id)
	} else {
		row, err = g.catalogueParent(ctx, p, id)
	}
	if err != nil {
		return CreationParent{}, err
	}
	return CreationParent{ID: row.ID, Directory: row.Directory}, nil
}
