package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/previews"
)

// previewsModule reads auth, remotes, the public URL and forge credentials
// at call time, so options applied after New still take effect.
func (s *Server) previewsModule() *previews.Module {
	return s.previewsMod.get(func() *previews.Module {
		return previews.NewModule(s.previewDeps())
	})
}

func (s *Server) previewDeps() previews.Deps {
	return previews.Deps{
		Store:  s.stateDB,
		Access: s.previewAccessAllowed,
		RemoteConnected: func(id string) bool {
			_, ok := s.router().LookupRemote(id)
			return ok
		},
		PublicURL: s.publicURL,
		Forges:    s.previewForges,
	}
}

// previewForges hands the previews module the hub's CLI/env forge
// credentials, never the clients projectforge and factory share.
func (s *Server) previewForges() previews.Forges {
	var f previews.Forges
	if s.integrations == nil {
		return f
	}
	if s.integrations.GitHub != nil {
		f.GitHubToken = s.integrations.GitHub.Token()
	}
	if reg := s.integrations.Forgejo; reg != nil {
		for _, h := range reg.Hosts() {
			if c := reg.ForHost(h); c != nil {
				f.Forgejo = append(f.Forgejo, previews.ForgejoLogin{Host: h, BaseURL: c.BaseURL(), Token: c.Token()})
			}
		}
	}
	return f
}

// directLocalRequest is a loopback peer that did not come through a proxy:
// behind a reverse proxy every request has a loopback RemoteAddr.
func directLocalRequest(r *http.Request) bool {
	if !isLoopback(r) || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-Ip") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	return isLoopbackHostname(strings.Trim(host, "[]"))
}

// previewAccessAllowed: the request may use this machine's credentials.
func (s *Server) previewAccessAllowed(r *http.Request) bool {
	if s.auth == nil {
		return directLocalRequest(r)
	}
	return s.auth.hasValidCookie(r) || (s.auth.trustLocalhost && directLocalRequest(r))
}
