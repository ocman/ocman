package server

import (
	"net/url"
	"strings"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// previewEntry is one supported provider as the frontend sees it: which
// link hosts it previews and whether it is configured. Unconfigured
// providers register no resolver, so their links are never looked up.
type previewEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Hosts are link hosts: exact, or "*.example.com" for any subdomain.
	Hosts      []string `json:"hosts"`
	Configured bool     `json:"configured"`
	// Source names the machine credential used without a saved token:
	// "cli" (gh, tea or a *_TOKEN variable) or "public" (anonymous).
	Source string `json:"source,omitempty"`
	// Token: a personal token can be pasted for this provider.
	Token bool `json:"token"`
	// OAuth: a sign-in app is configured.
	OAuth bool `json:"oauth"`
}

// previewSetup builds providers, resolvers and the catalog from sign-in
// apps and the provider IDs that have a saved grant or token. Forge and
// GitLab hosts are exact: tea logins, app hosts, saved-token hosts and
// gitlab.com; no other host is ever contacted.
func (s *Server) previewSetup(apps []configuredPreviewApp, grants map[string]bool) ([]previewauth.Provider, []linkpreview.Resolver, []previewEntry) {
	app := map[string]previewApp{}
	var hosted = map[string][]string{"forgejo": nil, "gitlab": {"gitlab.com"}}
	seenHost := map[string]bool{"gitlab:gitlab.com": true}
	addHost := func(kind, host string) {
		if host != "" && !seenHost[kind+":"+host] {
			seenHost[kind+":"+host] = true
			hosted[kind] = append(hosted[kind], host)
		}
	}
	for _, a := range apps {
		app[a.id()] = a.previewApp
		if a.Host != "" {
			addHost(a.Kind, a.Host)
		}
	}
	teaTokens := map[string]string{}
	teaAPI := map[string]string{}
	if s.integrations != nil && s.integrations.Forgejo != nil {
		for _, h := range s.integrations.Forgejo.Hosts() {
			c := s.integrations.Forgejo.ForHost(h)
			u, err := url.Parse(c.BaseURL())
			if err != nil || u.Scheme != "https" || u.Host == "" {
				continue // ponytail: plain-http hosts get no preview; tokens stay off the wire
			}
			host := strings.ToLower(u.Host)
			teaTokens[host], teaAPI[host] = c.Token(), strings.TrimRight(c.BaseURL(), "/")+"/api/v1"
			addHost("forgejo", host)
		}
	}
	for id := range grants {
		if kind, host, ok := strings.Cut(id, ":"); ok && (kind == "forgejo" || kind == "gitlab") {
			addHost(kind, host)
		}
	}

	var providers []previewauth.Provider
	var resolvers []linkpreview.Resolver
	var catalog []previewEntry
	add := func(p previewauth.Provider, e previewEntry, r linkpreview.Resolver) {
		e.ID, e.Name, e.Token, e.OAuth = p.ID, p.Name, p.TokenHelp != "", p.OAuth()
		e.Configured = e.Configured || grants[p.ID]
		providers = append(providers, p)
		catalog = append(catalog, e)
		if e.Configured {
			resolvers = append(resolvers, r)
		}
	}

	a := app["github"]
	gh := linkpreview.Forge{ID: "github", Host: "github.com", APIBase: "https://api.github.com", Connectable: true}
	ghSource := "public"
	if s.integrations != nil && s.integrations.GitHub != nil && s.integrations.GitHub.Token() != "" {
		gh.OwnerToken, ghSource = s.integrations.GitHub.Token(), "cli"
	}
	add(linkpreview.GitHubOAuth(a.ClientID, a.ClientSecret, ""), previewEntry{Hosts: []string{"github.com"}, Configured: true, Source: ghSource}, gh)

	for _, host := range hosted["forgejo"] {
		a := app["forgejo:"+host]
		f := linkpreview.Forge{ID: "forgejo:" + host, Host: host, APIBase: "https://" + host + "/api/v1", Gitea: true, Connectable: true}
		e := previewEntry{Hosts: []string{host}}
		if tok := teaTokens[host]; tok != "" {
			f.APIBase, f.OwnerToken, e.Configured, e.Source = teaAPI[host], tok, true, "cli"
		}
		add(linkpreview.ForgejoOAuth(host, a.ClientID, a.ClientSecret), e, f)
	}
	for _, host := range hosted["gitlab"] {
		a := app["gitlab:"+host]
		// Public projects preview anonymously on gitlab.com; a self-managed
		// host is only listed once it has a token or app.
		e := previewEntry{Hosts: []string{host}, Configured: host == "gitlab.com", Source: "public"}
		add(linkpreview.GitLabOAuth(host, a.ClientID, a.ClientSecret, "", ""), e, linkpreview.GitLab{Host: host})
	}
	a = app["linear"]
	add(linkpreview.LinearOAuth(a.ClientID, a.ClientSecret, ""), previewEntry{Hosts: []string{"linear.app"}}, linkpreview.Linear{})
	a = app["notion"]
	add(linkpreview.NotionOAuth(a.ClientID, a.ClientSecret, ""), previewEntry{
		Hosts: []string{"notion.so", "www.notion.so", "notion.com", "www.notion.com", "app.notion.com", "*.notion.site"},
	}, linkpreview.Notion{})
	// Slack and Jira have no usable personal token: OAuth apps only.
	if a, ok := app["slack"]; ok {
		// A dedicated app for previews, never the conversation.v1 plugin's bot app.
		add(linkpreview.SlackOAuth(a.ClientID, a.ClientSecret, ""), previewEntry{Hosts: []string{"*.slack.com"}}, linkpreview.Slack{})
	}
	if a, ok := app["jira"]; ok {
		add(linkpreview.JiraOAuth(a.ClientID, a.ClientSecret, "", ""), previewEntry{Hosts: []string{"*.atlassian.net"}}, linkpreview.Jira{})
	}
	return providers, resolvers, catalog
}
