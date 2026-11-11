package relay

import "net/http"

func (s *Server) authorizeEnrollment(r *http.Request) bool {
	token := bearerToken(r)
	if tokenAuthorises(hashToken(s.cfg.EnrollmentToken), token) {
		return true
	}
	return s.cfg.EnrollmentAuth != nil && s.cfg.EnrollmentAuth.Authorize(r.Context(), token)
}
