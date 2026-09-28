package server

import (
	"net/url"
	"strings"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// previewOwnerToken names a forge whose links preview with the owner
// machine's own token (env or CLI login), no viewer app needed.
type previewOwnerToken struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Host     string `json:"host"`
}

// previewSetup builds providers and resolvers for apps. GitHub and every
// https tea-login host resolve public links without any app, using the
// owner machine's token; an app adds viewer consent for private ones.
// Forgejo and GitLab hosts are exact: only those logins and app hosts.
func (s *Server) previewSetup(apps []configuredPreviewApp) ([]previewauth.Provider, []linkpreview.Resolver, []previewOwnerToken) {
	gh := linkpreview.Forge{ID: "github", Host: "github.com", APIBase: "https://api.github.com"}
	if s.integrations != nil && s.integrations.GitHub != nil {
		gh.OwnerToken = s.integrations.GitHub.Token()
	}
	forges := map[string]*linkpreview.Forge{}
	var hosts []string
	forgejo := func(host string) *linkpreview.Forge {
		if forges[host] == nil {
			forges[host] = &linkpreview.Forge{ID: "forgejo:" + host, Host: host, APIBase: "https://" + host + "/api/v1", Gitea: true}
			hosts = append(hosts, host)
		}
		return forges[host]
	}
	if s.integrations != nil && s.integrations.Forgejo != nil {
		for _, h := range s.integrations.Forgejo.Hosts() {
			c := s.integrations.Forgejo.ForHost(h)
			u, err := url.Parse(c.BaseURL())
			if err != nil || u.Scheme != "https" || u.Host == "" {
				continue // ponytail: plain-http hosts get no preview; tokens stay off the wire
			}
			f := forgejo(strings.ToLower(u.Host))
			f.APIBase, f.OwnerToken = strings.TrimRight(c.BaseURL(), "/")+"/api/v1", c.Token()
		}
	}

	var providers []previewauth.Provider
	var others []linkpreview.Resolver
	for _, a := range apps {
		id, secret := a.ClientID, a.ClientSecret
		switch a.Kind {
		case "github":
			providers = append(providers, linkpreview.GitHubOAuth(id, secret, ""))
			gh.Connectable = true
		case "forgejo":
			forgejo(a.Host).Connectable = true
			providers = append(providers, linkpreview.ForgejoOAuth(a.Host, id, secret))
		case "gitlab":
			providers = append(providers, linkpreview.GitLabOAuth(a.Host, id, secret, "", ""))
			others = append(others, linkpreview.GitLab{Host: a.Host})
		case "slack":
			// A dedicated app for viewer consent, never the conversation.v1 plugin's bot app.
			providers = append(providers, linkpreview.SlackOAuth(id, secret, ""))
			others = append(others, linkpreview.Slack{})
		case "notion":
			providers = append(providers, linkpreview.NotionOAuth(id, secret, ""))
			others = append(others, linkpreview.Notion{})
		case "linear":
			providers = append(providers, linkpreview.LinearOAuth(id, secret, ""))
			others = append(others, linkpreview.Linear{})
		case "jira":
			providers = append(providers, linkpreview.JiraOAuth(id, secret, "", ""))
			others = append(others, linkpreview.Jira{})
		}
	}

	var owners []previewOwnerToken
	if gh.OwnerToken != "" {
		owners = append(owners, previewOwnerToken{Provider: gh.ID, Name: "GitHub", Host: gh.Host})
	}
	resolvers := []linkpreview.Resolver{gh}
	for _, h := range hosts {
		f := *forges[h]
		if f.OwnerToken != "" {
			owners = append(owners, previewOwnerToken{Provider: f.ID, Name: "Forgejo", Host: f.Host})
		}
		resolvers = append(resolvers, f)
	}
	return providers, append(resolvers, others...), owners
}
