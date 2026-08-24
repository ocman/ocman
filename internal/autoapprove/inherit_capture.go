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
// can be persisted with the original asked snapshot. See issue #101.
type askedPermission struct {
	platformID string
	permission string
	patterns   []string
	metadata   map[string]any
	askedAt    int64
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

const approvalPersistenceTimeout = 5 * time.Second

// rememberAsked stores the asked-side data for (sessionID, permissionID)
// so a later approval can be recorded with the first observed snapshot.
// No-op on nil receiver. Bounded by askedCacheMax.
func (s *Service) rememberAsked(platformID, sessionID, permissionID, permission string, patterns []string, metadata map[string]any) askedPermission {
	if s == nil || permissionID == "" {
		return askedPermission{}
	}
	key := autoApproveKey(sessionID, permissionID)
	s.askedCacheMu.Lock()
	defer s.askedCacheMu.Unlock()
	if s.askedCache == nil {
		s.askedCache = make(map[string]askedPermission)
	}
	if existing, exists := s.askedCache[key]; exists {
		return existing
	}
	if len(s.askedCache) >= askedCacheMax {
		for k := range s.askedCache {
			delete(s.askedCache, k)
			break
		}
	}
	ap := askedPermission{
		platformID: platformID,
		permission: permission,
		patterns:   append([]string(nil), patterns...),
		metadata:   cloneMetadata(metadata),
		askedAt:    time.Now().UnixMilli(),
	}
	s.askedCache[key] = ap
	return ap
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
	s.Cancel(sessionID, permissionID)

	s.autoApproveMu.Lock()
	status := s.autoApprove[autoApproveKey(sessionID, permissionID)]
	if status != nil && status.aiResponseInFlight {
		status.pendingObservedReply = reply
		s.autoApproveMu.Unlock()
		return
	}
	if status != nil && status.aiResponseSucceeded {
		s.autoApproveMu.Unlock()
		return
	}
	s.autoApproveMu.Unlock()
	s.scheduleUserReplyCapture(ctx, sessionID, permissionID, reply)
}

// HandleDirectPermissionReply records a reply that an ocman request already
// delivered successfully. Unlike an observed event, it cannot be the AI
// response and is always eligible for user attribution.
func (s *Service) HandleDirectPermissionReply(ctx context.Context, sessionID, permissionID, reply string) {
	if s == nil {
		return
	}
	s.Cancel(sessionID, permissionID)
	if !s.claimUserReplyCapture(sessionID, permissionID) {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approvalPersistenceTimeout)
	defer cancel()
	s.captureUserReply(persistCtx, sessionID, permissionID, reply)
}

func (s *Service) scheduleUserReplyCapture(ctx context.Context, sessionID, permissionID, reply string) {
	if !s.claimUserReplyCapture(sessionID, permissionID) {
		return
	}
	go func() {
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approvalPersistenceTimeout)
		defer cancel()
		s.captureUserReply(persistCtx, sessionID, permissionID, reply)
	}()
}

func (s *Service) claimUserReplyCapture(sessionID, permissionID string) bool {
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	status := s.autoApprove[key]
	if status == nil {
		status = &autoApproveStatus{}
		s.autoApprove[key] = status
	}
	if status.userCaptureStarted {
		return false
	}
	status.userCaptureStarted = true
	return true
}

func (s *Service) captureUserReply(ctx context.Context, sessionID, permissionID, reply string) {
	ap, ok := s.takeAsked(sessionID, permissionID)
	if reply != "once" && reply != "always" {
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
				Reasoning:      "",
				ApprovedBy:     "user",
				Reply:          reply,
				Metadata:       ap.metadata,
				AskedAt:        ap.askedAt,
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
		"reply":        reply,
		"metadata":     ap.metadata,
		"askedAt":      ap.askedAt,
		"approvedAt":   approvedAt,
	})
	if err == nil {
		s.emitSessionSseEvent(sessionID, "ocman.permission.approved", payload)
	}
}

func (s *Service) beginAIResponse(sessionID, permissionID string) {
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	status := s.autoApprove[key]
	if status == nil {
		status = &autoApproveStatus{}
		s.autoApprove[key] = status
	}
	status.aiResponseInFlight = true
	status.aiResponseSucceeded = false
	s.autoApproveMu.Unlock()
}

func (s *Service) finishAIResponse(sessionID, permissionID string, succeeded bool) {
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	status := s.autoApprove[key]
	if status == nil {
		s.autoApproveMu.Unlock()
		return
	}
	status.aiResponseInFlight = false
	status.aiResponseSucceeded = succeeded
	reply := status.pendingObservedReply
	status.pendingObservedReply = ""
	s.autoApproveMu.Unlock()
	if !succeeded && reply != "" {
		// The reply could be from another client, or our request could have
		// succeeded before its transport failed. Do not guess its provenance.
		log.WithFields(log.Fields{
			"sessionID":    sessionID,
			"permissionID": permissionID,
		}).Warn("autoapprove: reply source ambiguous after AI response failure; skipping approval audit")
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return map[string]any{}
	}
	var cloned map[string]any
	if json.Unmarshal(b, &cloned) != nil {
		return map[string]any{}
	}
	return cloned
}
