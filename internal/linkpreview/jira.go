package linkpreview

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Jira previews Jira Cloud issues with the viewer's Atlassian 3LO grant
// (authorization code, read:jira-work + read:me + offline_access, rotating
// refresh token). One grant is stored per viewer and Atlassian account; a
// single token may reach several Jira sites.
//
// The link host is never an API endpoint: every Fetch lists the token's
// accessible resources and calls api.atlassian.com/ex/jira/{cloudid} for the
// site whose URL matches the link. A *.atlassian.net link to a site the token
// cannot reach is not_found. A configured identifier (ABC-42) is looked up on
// every accessible site; found on several, it becomes a chooser.
type Jira struct {
	// APIBase overrides https://api.atlassian.com (tests).
	APIBase string
}

const jiraProvider = "jira"

func (j Jira) base() string {
	if j.APIBase != "" {
		return j.APIBase
	}
	return "https://api.atlassian.com"
}

func (j Jira) Provider() string { return jiraProvider }

func (j Jira) APIHosts() []string {
	u, _ := url.Parse(j.base())
	return []string{u.Host}
}

var (
	jiraSite  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.atlassian\.net$`)
	jiraIssue = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}-[1-9][0-9]{0,9}$`)
	jiraCloud = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)
)

func (j Jira) ParseURL(u *url.URL) (Ref, bool) {
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Scheme != "https" || u.Port() != "" || !jiraSite.MatchString(host) || len(parts) != 2 || parts[0] != "browse" {
		return Ref{}, false
	}
	id := strings.ToUpper(parts[1])
	if !jiraIssue.MatchString(id) {
		return Ref{}, false
	}
	return Ref{Kind: "issue", ID: id, URL: "https://" + host + "/browse/" + id}, true
}

func (j Jira) ParseIdentifier(id string) (Ref, bool) {
	id = strings.ToUpper(id)
	if !jiraIssue.MatchString(id) {
		return Ref{}, false
	}
	return Ref{Kind: "issue", ID: id}, true
}

type jiraResource struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Scopes []string `json:"scopes"`
}

// sites lists the Jira sites the token reaches, with a validated cloud ID
// and https *.atlassian.net URL.
func (j Jira) sites(ctx context.Context, api *API) ([]jiraResource, error) {
	var res []jiraResource
	if err := api.JSON(ctx, http.MethodGet, j.base(), []string{"oauth", "token", "accessible-resources"}, nil, nil, &res); err != nil {
		return nil, err
	}
	var out []jiraResource
	for _, r := range res {
		u, err := url.Parse(r.URL)
		jira := false
		for _, s := range r.Scopes {
			jira = jira || s == "read:jira-work"
		}
		if err == nil && jira && jiraCloud.MatchString(r.ID) && u.Scheme == "https" && u.Port() == "" && jiraSite.MatchString(strings.ToLower(u.Hostname())) {
			r.URL = "https://" + strings.ToLower(u.Hostname())
			out = append(out, r)
		}
	}
	return out, nil
}

type jiraFields struct {
	Summary   string                `json:"summary"`
	Updated   string                `json:"updated"`
	Status    struct{ Name string } `json:"status"`
	IssueType struct{ Name string } `json:"issuetype"`
	Assignee  *struct {
		DisplayName string `json:"displayName"`
	} `json:"assignee"`
}

func (j Jira) issue(ctx context.Context, api *API, site jiraResource, key string) (Preview, error) {
	var is struct {
		Key    string     `json:"key"`
		Fields jiraFields `json:"fields"`
	}
	q := url.Values{"fields": {"summary,status,issuetype,assignee,updated"}}
	if err := api.JSON(ctx, http.MethodGet, j.base(), []string{"ex", "jira", site.ID, "rest", "api", "3", "issue", key}, q, nil, &is); err != nil {
		return Preview{}, err
	}
	if !strings.EqualFold(is.Key, key) {
		return Preview{}, &HTTPError{Status: http.StatusNotFound}
	}
	f := is.Fields
	assignee := "Unassigned"
	if f.Assignee != nil && f.Assignee.DisplayName != "" {
		assignee = f.Assignee.DisplayName
	}
	updated, _ := time.Parse("2006-01-02T15:04:05.000-0700", f.Updated)
	return Preview{
		Ref: Ref{URL: site.URL + "/browse/" + key}, Title: f.Summary, Status: f.Status.Name, Icon: "bi-kanban",
		Meta: []string{firstNonEmpty(f.IssueType.Name, "Issue"), assignee, site.Name}, UpdatedAt: updated,
	}, nil
}

func (j Jira) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	if !jiraIssue.MatchString(ref.ID) {
		return Preview{}, ErrUnsafeRequest
	}
	sites, err := j.sites(ctx, api)
	if err != nil {
		return Preview{}, err
	}
	if ref.URL != "" {
		u, err := url.Parse(ref.URL)
		if err != nil {
			return Preview{}, ErrUnsafeRequest
		}
		for _, s := range sites {
			if s.URL == "https://"+strings.ToLower(u.Hostname()) {
				return j.issue(ctx, api, s, ref.ID)
			}
		}
		return Preview{}, &HTTPError{Status: http.StatusNotFound}
	}
	// Identifier: try every site; one hit is the issue, several a chooser.
	var found []Preview
	err = &HTTPError{Status: http.StatusNotFound}
	for _, s := range sites[:min(len(sites), maxChoices)] {
		p, e := j.issue(ctx, api, s, ref.ID)
		var he *HTTPError
		switch {
		case e == nil:
			found = append(found, p)
		case errors.As(e, &he) && he.Status == http.StatusNotFound:
		default:
			err = e
		}
	}
	switch len(found) {
	case 0:
		return Preview{}, err
	case 1:
		return found[0], nil
	}
	var p Preview
	for _, f := range found {
		p.Choices = append(p.Choices, Choice{Title: f.Meta[2] + ": " + f.Title, URL: f.URL})
	}
	return p, nil
}

// JiraOAuth is the Atlassian 3LO consent app. Atlassian rotates the refresh
// token on every refresh; previewauth stores the new one. apiBase and
// authBase "" are production.
func JiraOAuth(clientID, clientSecret, apiBase, authBase string) previewauth.Provider {
	j := Jira{APIBase: apiBase}
	if authBase == "" {
		authBase = "https://auth.atlassian.com"
	}
	return previewauth.Provider{
		ID: jiraProvider, Name: "Jira",
		Notice:   "Read-only access to the Jira issues your Atlassian account can see on the sites you choose.",
		AuthURL:  authBase + "/authorize",
		TokenURL: authBase + "/oauth/token",
		ClientID: clientID, ClientSecret: clientSecret,
		// Atlassian has no token revocation endpoint for 3LO apps; users
		// revoke in their Atlassian account settings.
		Scopes:     []string{"read:jira-work", "read:me", "offline_access"},
		JSONBody:   true,
		AuthParams: map[string]string{"audience": "api.atlassian.com", "prompt": "consent"},
		Identify: func(ctx context.Context, client *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			hosts := map[string]bool{}
			for _, h := range j.APIHosts() {
				hosts[strings.ToLower(h)] = true
			}
			api := &API{client: client, token: tok.AccessToken, hosts: hosts}
			var me struct {
				AccountID string `json:"account_id"`
				Name      string `json:"name"`
			}
			if err := api.JSON(ctx, http.MethodGet, j.base(), []string{"me"}, nil, nil, &me); err != nil || me.AccountID == "" {
				return nil, previewauth.ErrExchange
			}
			sites, err := j.sites(ctx, api)
			if err != nil || len(sites) == 0 {
				return nil, previewauth.ErrExchange
			}
			g := previewauth.Grant{WorkspaceID: me.AccountID, WorkspaceName: "Atlassian", AccountName: me.Name}
			for _, s := range sites {
				g.Sites = append(g.Sites, state.PreviewSite{ID: s.ID, Name: firstNonEmpty(s.Name, s.URL)})
			}
			return []previewauth.Grant{g}, nil
		},
	}
}
