package autoapprove

import (
	"strings"

	"github.com/ocman/ocman/internal/platforms"
)

// Removing an ask rule restores normal approval handling for requests already
// pending. Wait for a cancelled judge to release its slot before replacing it.
func (s *Service) resumeAfterManualRule(adapter platforms.Platform, sid, pid string, asked askedPermission) {
	recheck := func() { s.Ensure(adapter.ID(), adapter, sid, pid, asked.permission, asked.patterns, asked.metadata) }
	s.autoApproveMu.Lock()
	key := autoApproveKey(sid, pid)
	st := s.autoApprove[key]
	if st == nil || st.manualResolvedAt > 0 || st.aiResponseInFlight || st.aiResponseSucceeded || !strings.HasPrefix(st.reasoning, "Your session rule") {
		s.autoApproveMu.Unlock()
		return
	}
	st.verdict = ""
	if st.cancel != nil {
		st.policyRecheck = recheck
		s.autoApproveMu.Unlock()
		return
	}
	delete(s.autoApprove, key)
	s.autoApproveMu.Unlock()
	recheck()
}
