package sessionsvc

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type connectionRouting struct {
	router    *platformconnections.Router
	catalogue *sessioncatalog.Store
	owner     string
}

// WithConnections installs owner-local named routing before serving requests.
// The composition layer must supply canonical, account-configured adapters.
// Remote scopes retain their authenticated registry route; never resolve them
// against this owner's defaults.
func (s *Service) WithConnections(router *platformconnections.Router, catalogue *sessioncatalog.Store, owner string) error {
	if router == nil || catalogue == nil || owner == "" || s.registry == nil || !s.registry.CanonicalSessionIDs() {
		return platformconnections.ErrInvalid
	}
	s.connections = &connectionRouting{router: router, catalogue: catalogue, owner: owner}
	s.WithSessionCatalogue(catalogue, owner)
	return nil
}

func localConnectionScope(scope string) bool {
	remote, _ := platforms.SplitPlatformID(scope)
	return remote == ""
}

func connectionReference(scope string) platformconnections.Reference {
	switch scope {
	case "opencode", "claudecode", "codex":
		return platformconnections.Reference{LegacyKind: scope}
	default:
		return platformconnections.Reference{ConnectionID: scope}
	}
}

// PlatformKind returns the model namespace without replacing the selected
// connection routing identity. Remote named adapters publish it separately.
func (s *Service) PlatformKind(ctx context.Context, scope string) (platforms.ID, error) {
	adapter, err := s.pickAdapter(ctx, scope)
	if err != nil {
		return "", err
	}
	if native, ok := adapter.(interface{ NativeKind() platforms.ID }); ok {
		return native.NativeKind().Base(), nil
	}
	return adapter.ID().Base(), nil
}

func (s *Service) creationAdapter(ctx context.Context, scope string, req *platforms.CreateSessionRequest) (adapter platforms.Platform, err error) {
	defer func() { err = connectionDispatchError(err) }()
	if s.connections == nil || !localConnectionScope(scope) {
		return s.pickAdapter(ctx, scope)
	}
	if req.Creation.SessionID != "" {
		// An externally supplied canonical identity cannot acquire a first
		// connection binding if it already exists in the catalogue.
		row, err := s.connections.catalogue.GetCanonical(ctx, req.Creation.SessionID)
		if err != nil && !errors.Is(err, sessioncatalog.ErrNotFound) {
			return nil, err
		}
		if err == nil {
			if row.OwnerID != s.connections.owner || row.Purged {
				return nil, platforms.ErrNotFound
			}
			if _, err := s.connections.router.ResolveSession(ctx, row.ID); err != nil {
				return nil, err
			}
		}
	}
	route, err := s.connections.router.AdmitCreation(ctx, req.Creation.RequestKey, req.Creation.SessionID, connectionReference(scope))
	if err != nil {
		return nil, err
	}
	if !platforms.CanonicalAdapter(route.Platform) {
		return nil, platforms.ErrPlatformUnreachable
	}
	req.Creation.SessionID = route.SessionID
	return route.Platform, nil
}

func (s *Service) resolveConnectionSession(ctx context.Context, id, scope string) (adapter platforms.Platform, err error) {
	defer func() { err = connectionDispatchError(err) }()
	row, err := s.connections.catalogue.GetCanonical(ctx, id)
	if errors.Is(err, sessioncatalog.ErrNotFound) {
		// Authenticate uncached canonical remote IDs through their recorded
		// owner before mirroring, as the legacy identity gateway does. Never
		// allocate a local identity or infer the current account from a miss.
		return s.identities.OwnerPlatform(ctx, id, scope)
	}
	if err != nil {
		return nil, err
	}
	if row.OwnerID != s.connections.owner {
		// The gateway enforces the recorded owner and explicit remote scope.
		return s.identities.OwnerPlatform(ctx, id, scope)
	}
	if row.Purged {
		return nil, platforms.ErrNotFound
	}
	if row.CreationState != sessioncatalog.Ready {
		return nil, sessioncatalog.ErrCreating
	}
	route, err := s.connections.router.ResolveSession(ctx, id)
	if errors.Is(err, platformconnections.ErrNotFound) {
		// Imported history acquires execution routing only from an immutable
		// owner/kind/physical-source binding, never the current default.
		source, sourceErr := s.connections.router.ResolveSource(ctx, row.Platform, row.SourceKey)
		if sourceErr != nil {
			return nil, sourceErr
		}
		route, err = s.connections.router.AdmitSession(ctx, id, platformconnections.Reference{ConnectionID: source.Connection.ID})
	}
	if err != nil {
		return nil, err
	}
	if route.Connection.Kind != row.Platform || !platforms.CanonicalAdapter(route.Platform) ||
		(scope != "" && scope != route.Connection.ID && scope != row.Platform) {
		return nil, platforms.ErrNotFound
	}
	return route.Platform, nil
}

func (s *Service) catalogAdapter(ctx context.Context, scope string) (platforms.Platform, string, error) {
	if s.connections != nil && localConnectionScope(scope) {
		route, err := s.connections.router.Resolve(ctx, connectionReference(scope))
		return route.Platform, route.Connection.ID, connectionDispatchError(err)
	}
	adapter, err := s.pickAdapter(ctx, scope)
	if err != nil {
		return nil, "", err
	}
	return adapter, string(adapter.ID()), nil
}

func (s *Service) pinForkConnection(ctx context.Context, parentID string, intent *platforms.CreateSessionRequest) error {
	if s.connections == nil {
		return nil
	}
	row, err := s.connections.catalogue.GetCanonical(ctx, parentID)
	if err != nil {
		return err
	}
	if row.OwnerID != s.connections.owner {
		return nil // The authenticated remote owner pins its own fork.
	}
	route, err := s.connections.router.ResolveSession(ctx, parentID)
	if err != nil {
		return err
	}
	_, err = s.creationAdapter(ctx, route.Connection.ID, intent)
	return err
}
