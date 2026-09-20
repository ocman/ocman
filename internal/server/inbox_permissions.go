package server

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

func (s *Server) notifyPermissionInbox(permission state.InboxPermission) {
	if s.stateDB == nil || isRemotePlatformID(permission.Platform) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.stateDB.EnsurePermissionInboxItem(ctx, permission); err != nil {
		log.WithError(err).Warn("creating permission Inbox item")
		return
	}
	s.broadcastGlobalEvent("ocman.inbox.changed", []byte(`{}`))
}

func (s *Server) resolvePermissionInbox(ctx context.Context, platform, sessionID, permissionID string) {
	if s.stateDB == nil || isRemotePlatformID(platform) || sessionID == "" || permissionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.stateDB.ResolvePermissionInboxItem(ctx, platform, sessionID, permissionID); err != nil {
		log.WithError(err).Warn("archiving resolved permission Inbox item")
		return
	}
	s.broadcastGlobalEvent("ocman.inbox.changed", []byte(`{}`))
}

// Recover missed replies, aborted turns and requests resolved while ocman was down.
// Failed owner reads leave the request intact; lack of connectivity is not resolution.
func (s *Server) reconcilePermissionInbox(ctx context.Context) {
	if s.stateDB == nil || s.registry == nil {
		return
	}
	items, err := s.stateDB.ListInboxItems(ctx)
	if err != nil {
		log.WithError(err).Warn("reconciling permission Inbox")
		return
	}
	type sessionKey struct{ platform, sessionID string }
	pending := make(map[sessionKey]map[string]bool)
	for _, item := range items {
		permission := item.Permission
		if permission == nil {
			continue
		}
		key := sessionKey{permission.Platform, permission.SessionID}
		ids, loaded := pending[key]
		if !loaded {
			pending[key] = nil
			adapter, ok := s.registry.Get(platforms.ID(key.platform))
			if !ok {
				continue
			}
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var prompts []platforms.LivePrompt
			var err error
			if live, ok := adapter.(interface {
				RefreshPermissions(context.Context, string) ([]platforms.LivePrompt, error)
			}); ok {
				prompts, err = live.RefreshPermissions(readCtx, key.sessionID)
			} else {
				prompts, err = adapter.ListPermissions(readCtx, key.sessionID)
			}
			cancel()
			if err != nil {
				continue
			}
			s.ensurePendingPermissions(adapter, key.sessionID, prompts)
			ids = make(map[string]bool)
			for _, prompt := range prompts {
				id, _ := prompt["id"].(string)
				ids[id] = true
			}
			pending[key] = ids
		}
		if ids != nil && !ids[permission.PermissionID] {
			s.resolvePermissionInbox(ctx, key.platform, key.sessionID, permission.PermissionID)
		}
	}
}

func (s *Server) runPermissionInboxReconciliation(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcilePermissionInbox(ctx)
		}
	}
}
