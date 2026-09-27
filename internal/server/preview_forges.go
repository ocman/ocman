package server

import (
	"net/url"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// forgePreviews wires GitHub and Forgejo previews. Every forge resolves
// public links without login using the owner machine's env/CLI token; a
// viewer OAuth app is added when configured:
//
//	OCMAN_GITHUB_PREVIEW_CLIENT_ID / OCMAN_GITHUB_PREVIEW_CLIENT_SECRET
//	OCMAN_FORGEJO_PREVIEW_APPS=host=client_id:secret[,host=client_id:secret]
//
// The allowlist is exact: github.com, the https tea-login hosts, and the
// hosts named in OCMAN_FORGEJO_PREVIEW_APPS.
func (s *Server) forgePreviews() ([]previewauth.Provider, []linkpreview.Resolver) {
	var providers []previewauth.Provider
	gh := linkpreview.Forge{ID: "github", Host: "github.com", APIBase: "https://api.github.com"}
	if s.integrations != nil && s.integrations.GitHub != nil {
		gh.OwnerToken = s.integrations.GitHub.Token()
	}
	if id, secret := os.Getenv("OCMAN_GITHUB_PREVIEW_CLIENT_ID"), os.Getenv("OCMAN_GITHUB_PREVIEW_CLIENT_SECRET"); id != "" && secret != "" {
		providers = append(providers, linkpreview.GitHubOAuth(id, secret, ""))
		gh.Connectable = true
	}
	resolvers := []linkpreview.Resolver{gh}

	forges := map[string]*linkpreview.Forge{}
	var hosts []string
	if s.integrations != nil && s.integrations.Forgejo != nil {
		for _, h := range s.integrations.Forgejo.Hosts() {
			c := s.integrations.Forgejo.ForHost(h)
			u, err := url.Parse(c.BaseURL())
			if err != nil || u.Scheme != "https" || u.Host == "" {
				continue // ponytail: plain-http hosts get no preview; tokens stay off the wire
			}
			host := strings.ToLower(u.Host)
			forges[host] = &linkpreview.Forge{ID: "forgejo:" + host, Host: host, APIBase: strings.TrimRight(c.BaseURL(), "/") + "/api/v1", Gitea: true, OwnerToken: c.Token()}
			hosts = append(hosts, host)
		}
	}
	for _, app := range strings.Split(os.Getenv("OCMAN_FORGEJO_PREVIEW_APPS"), ",") {
		host, creds, _ := strings.Cut(strings.TrimSpace(app), "=")
		id, secret, _ := strings.Cut(creds, ":")
		host = strings.ToLower(host)
		if app == "" {
			continue
		}
		if u, err := url.Parse("https://" + host); err != nil || u.Host != host || id == "" || secret == "" {
			log.WithField("entry", host).Warn("preview auth: ignoring invalid OCMAN_FORGEJO_PREVIEW_APPS entry")
			continue
		}
		f := forges[host]
		if f == nil {
			f = &linkpreview.Forge{ID: "forgejo:" + host, Host: host, APIBase: "https://" + host + "/api/v1", Gitea: true}
			forges[host] = f
			hosts = append(hosts, host)
		}
		f.Connectable = true
		providers = append(providers, linkpreview.ForgejoOAuth(host, id, secret))
	}
	for _, h := range hosts {
		resolvers = append(resolvers, *forges[h])
	}
	return providers, resolvers
}
