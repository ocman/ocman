package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sessionsvc"
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
// more-specific module routes. Canonical adapters retain the Ocman path key;
// handlers read the resolved ID from the route's {id} path value.
func (s *Server) withOwnedSessionPath(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		switch strings.TrimPrefix(r.URL.Path, "/api/session/") {
		case "archive", "seen", "pin":
			id = ""
		}
		if id == "" {
			next(w, r)
			return
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
			resolved := r.Clone(r.Context())
			q := resolved.URL.Query()
			// Identity provenance carries the native kind, not a named
			// connection's routing key. Resolve its authenticated physical
			// source before dispatch instead of restoring the native kind.
			route := info.Platform
			_, kind := platforms.SplitPlatformID(info.Platform)
			if _, key, ok := s.registry.PlatformForCanonicalSource(r.Context(), info.OwnerID, kind, info.SourceKey); ok {
				route = string(key)
			}
			q.Set("platform", route)
			resolved.URL.RawQuery = q.Encode()
			resolved.SetPathValue("id", coreID)
			w.Header().Set("X-Ocman-Session-ID", info.ID)
			next(w, resolved)
		})(w, r)
	}
}
