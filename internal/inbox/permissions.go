package inbox

import (
	"context"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

// NotifyPermission raises an Inbox item for a pending local permission.
func (m *Module) NotifyPermission(permission state.InboxPermission) {
	if isRemote(permission.Platform) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), permissionCallTimeout)
	defer cancel()
	identity, err := m.permissionKey(ctx, permission.Platform, permission.SessionID, permission.PermissionID)
	if err != nil {
		permissionIdentityWarning(err, "notify", permission.Platform, permission.SessionID, permission.PermissionID)
		return
	}
	permission.ItemID = identity.key
	permission.Platform = identity.platform
	permission.ConnectionID = ""
	if identity.route != identity.platform {
		permission.ConnectionID = identity.route
	}
	if err := m.Store.EnsurePermissionInboxItem(ctx, permission); err != nil {
		log.WithError(err).Warn("creating permission Inbox item")
		return
	}
	m.changed()
}

// ResolvePermission archives the item for a permission that was answered.
func (m *Module) ResolvePermission(ctx context.Context, platform, sessionID, permissionID string) {
	if isRemote(platform) || sessionID == "" || permissionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), permissionCallTimeout)
	defer cancel()
	identity, err := m.permissionKey(ctx, platform, sessionID, permissionID)
	if err != nil {
		permissionIdentityWarning(err, "resolve", platform, sessionID, permissionID)
		return
	}
	if err := m.Store.ResolvePermissionInboxItemScopes(ctx, identity.key, identity.route, identity.platform, sessionID, permissionID); err != nil {
		log.WithError(err).Warn("archiving resolved permission Inbox item")
		return
	}
	m.changed()
}

type permissionIdentity struct{ key, platform, route string }

func (m *Module) permissionKey(ctx context.Context, scope, session, permission string) (permissionIdentity, error) {
	if m.Platform == nil {
		return permissionIdentity{}, fmt.Errorf("%w: %w", platforms.ErrHistoryUnavailable, errPermissionAdapterUnavailable)
	}
	adapter, route, ok := m.Platform(ctx, platforms.ID(scope), session)
	if !ok || adapter == nil {
		return permissionIdentity{}, fmt.Errorf("%w: %w", platforms.ErrHistoryUnavailable, errPermissionAdapterUnavailable)
	}
	// Connection keys select the account, not the notification namespace.
	// Canonical IDs distinguish accounts; using the native kind preserves
	// existing keys, including pre-cutover native-ID correlations.
	scope = string(adapter.ID())
	key, err := platforms.PermissionNotificationKey(ctx, adapter, scope, session, permission)
	return permissionIdentity{key: key, platform: scope, route: string(route)}, err
}

// Reconcile recovers missed replies, aborted turns and requests resolved while ocman was down.
// Failed owner reads leave the request intact; lack of connectivity is not resolution.
func (m *Module) Reconcile(ctx context.Context) {
	permissions, err := m.Store.PendingInboxPermissions(ctx)
	if err != nil {
		log.WithError(err).Warn("reconciling permission Inbox")
		return
	}
	type sessionKey struct{ platform, sessionID string }
	pending := make(map[sessionKey]map[string]bool)
	for _, permission := range permissions {
		scope := permission.Platform
		if permission.ConnectionID != "" {
			scope = permission.ConnectionID
		}
		key := sessionKey{scope, permission.SessionID}
		ids, loaded := pending[key]
		if !loaded {
			pending[key] = nil
			adapter, _, ok := m.Platform(ctx, platforms.ID(key.platform), key.sessionID)
			// Push-only platforms settle Inbox items from replayed reply events.
			if !ok || adapter.Capabilities().PushPrompts {
				continue
			}
			readCtx, cancel := context.WithTimeout(ctx, permissionCallTimeout)
			var prompts []platforms.LivePrompt
			var err error
			if live, ok := adapter.(platforms.PermissionRefresher); ok {
				prompts, err = live.RefreshPermissions(readCtx, key.sessionID)
			} else {
				prompts, err = adapter.ListPermissions(readCtx, key.sessionID)
			}
			cancel()
			if err != nil {
				continue
			}
			m.EnsurePending(adapter, key.sessionID, prompts)
			ids = make(map[string]bool)
			for _, prompt := range prompts {
				id, _ := prompt["id"].(string)
				ids[id] = true
			}
			pending[key] = ids
		}
		if ids != nil && !ids[permission.PermissionID] {
			m.ResolvePermission(ctx, key.platform, key.sessionID, permission.PermissionID)
		}
	}
}

// isRemote reports a compound remote platform id: the owner keeps its own
// permission items.
func isRemote(platform string) bool { return strings.HasPrefix(platform, "r-") }
