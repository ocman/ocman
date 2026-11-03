package server

import (
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func (s *Server) proxyPublishedResource(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.URL.Path = "/" + r.PathValue("resource")
	r.URL.RawPath = ""
	if !hostsvc.IsPublishedResource(r.Method, r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if owner, base := platforms.SplitPlatformID(q.Get("platform")); owner == r.PathValue("remoteId") {
		q.Set("platform", base)
		r.URL.RawQuery = q.Encode()
	}
	owner, ok := s.resolveOwner(w, "", r.PathValue("remoteId"))
	if !ok {
		return
	}
	resources, ok := owner.(hostsvc.PublishedResources)
	if !ok {
		http.Error(w, "owner does not support published resources", http.StatusServiceUnavailable)
		return
	}
	if err := resources.ServePublishedResource(w, r); err != nil {
		// A streaming failure must not turn partial bytes into a successful file.
		panic(http.ErrAbortHandler)
	}
}

// ServePublishedResource is the narrow owner-side handler used by authenticated
// gRPC. It never calls the general web router or accepts mutation/share routes.
func (s *Server) ServePublishedResource(w http.ResponseWriter, r *http.Request) {
	if !hostsvc.IsPublishedResource(r.Method, r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/artifacts") {
		if m := s.artifactsModule(); m != nil {
			m.ServePublishedResource(w, r)
			return
		}
		http.Error(w, "artifacts unavailable", http.StatusServiceUnavailable)
		return
	}
	s.filesystemModule().ServePublishedResource(w, r)
}
