package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

func writeIdentityError(w http.ResponseWriter, err error) {
	var invalid *sessionsvc.ValidationError
	switch {
	case errors.As(err, &invalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, sessioncatalog.ErrCreating), errors.Is(err, sessioncatalog.ErrCreationFailed), errors.Is(err, sessioncatalog.ErrBindingConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		httpx.WritePlatformError(w, "resolving session identity", err)
	}
}

func (s *Server) handleSessionIdentity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	info, err := s.sessions.LookupSessionIdentity(r.Context(), q.Get("platform"), platforms.SessionIdentityRequest{ID: q.Get("id"), NativeID: q.Get("nativeId")})
	if err != nil {
		writeIdentityError(w, err)
		return
	}
	public := *info
	public.SourceKey = ""
	httpx.WriteJSON(w, public)
}

// withOwnedSessionPath validates ownership before the core dispatcher and
// more-specific module routes. Canonical adapters retain the Ocman path key.
func (s *Server) withOwnedSessionPath(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/session/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			next(w, r)
			return
		}
		id, suffix, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix), "/")
		for _, route := range sessionSubRoutes {
			if !strings.Contains(route.pattern, "{") && route.pattern == strings.TrimPrefix(r.URL.Path, prefix) {
				next(w, r)
				return
			}
		}
		if identity.IsPendingReference(id) {
			s.requireAuth(func(w http.ResponseWriter, _ *http.Request) { writeIdentityError(w, platforms.ErrPlatformUnreachable) })(w, r)
			return
		}
		if !strings.HasPrefix(id, "ocm_") && !s.sessions.CanonicalSessionReferences(r.URL.Query().Get("platform")) {
			next(w, r)
			return
		}
		s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			info, err := s.sessions.LookupSessionReference(r.Context(), r.URL.Query().Get("platform"), id)
			if err != nil {
				writeIdentityError(w, err)
				return
			}
			coreID := s.sessions.CoreSessionReference(info)
			if !httpx.ValidID(coreID) {
				http.Error(w, "invalid session binding", http.StatusConflict)
				return
			}
			copy := r.Clone(r.Context())
			u := *r.URL
			copy.URL = &u
			u.Path = prefix + coreID
			if suffix != "" {
				u.Path += "/" + suffix
			}
			u.RawPath = ""
			q := u.Query()
			q.Set("platform", info.Platform)
			u.RawQuery = q.Encode()
			for _, name := range []string{"id", "sessionId", "sessionID"} {
				if copy.PathValue(name) == id {
					copy.SetPathValue(name, coreID)
				}
			}
			w.Header().Set("X-Ocman-Session-ID", info.ID)
			if view, ok := w.(*sessionViewWriter); ok {
				view.projector.Platform = info.Platform
			}
			next(w, copy)
		})(w, r)
	}
}
