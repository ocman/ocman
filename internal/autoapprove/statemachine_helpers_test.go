package autoapprove

// lookupJudged returns the recorded verdict for (sessionID, permissionID)
// and ok=true only once the judge has produced one.
func lookupJudged(s *Service, sessionID, permissionID string) (judgeVerdict, bool) {
	st, ok := s.lookupAutoApproveStatus(sessionID, permissionID)
	if !ok || st.verdict == "" {
		return "", false
	}
	return st.verdict, true
}
