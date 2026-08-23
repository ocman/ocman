package autoapprove

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/state"
)

// askedPermission is the slice of a permission.asked event retained
// until the matching permission.replied arrives, so a user approval
// can be persisted with the original permission
// text and patterns (the replied event carries neither). See issue #101.
type askedPermission struct {
	platformID string
	permission string
	patterns   []string
}

// askedCacheMax bounds the in-memory asked cache. A permission that is
// never replied to leaks one entry; the cap keeps a runaway session
// from growing the map without bound. When full, the oldest-inserted
// key wins eviction is not tracked (Go map order is undefined), so we
// simply drop a single arbitrary entry — good enough for a soft cache
// whose only job is to bridge asked -> replied within one session.
//
// ponytail: arbitrary-drop eviction, not true LRU; switch to a ring
// buffer of keys if a pathological session ever fills this.
const askedCacheMax = 2048

// rememberAsked stores the asked-side data for (sessionID, permissionID)
// so a later approval can be recorded with the right patterns.
// No-op on nil receiver. Bounded by askedCacheMax.
func (s *Service) rememberAsked(platformID, sessionID, permissionID, permission string, patterns []string) {
	if s == nil || permissionID == "" {
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.askedCacheMu.Lock()
	defer s.askedCacheMu.Unlock()
	if s.askedCache == nil {
		s.askedCache = make(map[string]askedPermission)
	}
	if _, exists := s.askedCache[key]; !exists && len(s.askedCache) >= askedCacheMax {
		for k := range s.askedCache {
			delete(s.askedCache, k)
			break
		}
	}
	s.askedCache[key] = askedPermission{
		platformID: platformID,
		permission: permission,
		patterns:   patterns,
	}
}

// takeAsked returns and removes the asked-side data for
// (sessionID, permissionID). ok=false when nothing was remembered.
func (s *Service) takeAsked(sessionID, permissionID string) (askedPermission, bool) {
	if s == nil {
		return askedPermission{}, false
	}
	key := autoApproveKey(sessionID, permissionID)
	s.askedCacheMu.Lock()
	defer s.askedCacheMu.Unlock()
	ap, ok := s.askedCache[key]
	if ok {
		delete(s.askedCache, key)
	}
	return ap, ok
}

// HandlePermissionReplied is the single handler for a permission.replied
// event. It always cancels any in-flight judge for the permission. User
// approvals are persisted for command footnotes; only "always" is later
// inherited by worktree children.
//
// platformID scopes the persisted row; it is taken from the asked-side
// cache entry (the platform that saw the original permission.asked) so
// the record lands under the right platform even when the caller only
// knows the connection's platform.
func (s *Service) HandlePermissionReplied(ctx context.Context, sessionID, permissionID, reply string) {
	if s == nil {
		return
	}
	status, judged := s.lookupAutoApproveStatus(sessionID, permissionID)
	ap, ok := s.takeAsked(sessionID, permissionID)
	s.Cancel(sessionID, permissionID)
	if (reply != "once" && reply != "always") || (judged && status.verdict == verdictSafe) {
		return
	}
	if !ok {
		// No asked-side data (e.g. ocman started after the prompt, or
		// the asked event was never observed). Nothing to persist.
		log.WithFields(log.Fields{
			"sessionID":    sessionID,
			"permissionID": permissionID,
		}).Debug("autoapprove: user approval with no cached asked data; skipping capture")
		return
	}
	approvedAt := time.Now().UnixMilli()
	if s.deps.Store != nil {
		if err := s.deps.Store.RecordApprovedPermission(
			ctx,
			ap.platformID,
			sessionID,
			state.ApprovedPermission{
				PermissionID:   permissionID,
				PermissionText: ap.permission,
				Patterns:       ap.patterns,
				JudgeSessionID: "",
				Reasoning:      state.UserApprovalReasonPrefix + reply,
				ApprovedAt:     approvedAt,
			},
		); err != nil {
			log.WithError(err).WithFields(log.Fields{
				"sessionID":    sessionID,
				"permissionID": permissionID,
			}).Warn("autoapprove: failed to persist user approval")
		}
	}

	patterns := ap.patterns
	if patterns == nil {
		patterns = []string{}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"permissionId": permissionID,
		"sessionID":    sessionID,
		"permission":   ap.permission,
		"patterns":     patterns,
		"approvedBy":   "user",
		"approvedAt":   approvedAt,
	})
	if err == nil {
		s.emitSessionSseEvent(sessionID, "ocman.permission.approved", payload)
	}
}
