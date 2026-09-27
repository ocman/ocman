package linkpreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Linear previews issues with the viewer's own OAuth grant (authorization
// code + PKCE, read scope, rotating refresh token kept server-side). One
// grant is stored per viewer and Linear workspace (organization ID).
//
// Links https://linear.app/{workspace}/issue/{ENG-123}[/slug] resolve only
// with a grant whose organization urlKey matches the link: another
// workspace's grant answers not_found without returning anything it read.
// A configured identifier (ENG-123) resolves against the chosen grant.
// Linear reports auth and permission failures inside GraphQL errors (often
// with HTTP 400); they map onto the shared 401 (grant forgotten) / 403 / 404
// / 429 handling.
type Linear struct {
	// APIBase overrides https://api.linear.app (tests).
	APIBase string
}

const linearProvider = "linear"

func (l Linear) base() string {
	if l.APIBase != "" {
		return l.APIBase
	}
	return "https://api.linear.app"
}

func (l Linear) Provider() string { return linearProvider }

func (l Linear) APIHosts() []string {
	u, _ := url.Parse(l.base())
	return []string{u.Host}
}

var (
	linearKey   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	linearIssue = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,15}-[1-9][0-9]{0,8}$`)
)

func (l Linear) ParseURL(u *url.URL) (Ref, bool) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Scheme != "https" || u.Port() != "" || !strings.EqualFold(u.Hostname(), "linear.app") || len(parts) < 3 || parts[1] != "issue" {
		return Ref{}, false
	}
	key, id := strings.ToLower(parts[0]), strings.ToUpper(parts[2])
	if !linearKey.MatchString(key) || !linearIssue.MatchString(id) {
		return Ref{}, false
	}
	return Ref{Kind: "issue", ID: id, URL: "https://linear.app/" + key + "/issue/" + id}, true
}

func (l Linear) ParseIdentifier(id string) (Ref, bool) {
	id = strings.ToUpper(id)
	if !linearIssue.MatchString(id) {
		return Ref{}, false
	}
	return Ref{Kind: "issue", ID: id}, true
}

type linearError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code       string `json:"code"`
		StatusCode int    `json:"statusCode"`
	} `json:"extensions"`
}

// linearStatus maps GraphQL errors to the status Service handles, or 0.
func linearStatus(errs []linearError) int {
	for _, e := range errs {
		switch e.Extensions.Code {
		case "AUTHENTICATION_ERROR":
			return http.StatusUnauthorized
		case "FORBIDDEN":
			return http.StatusForbidden
		case "RATELIMITED":
			return http.StatusTooManyRequests
		}
		switch e.Extensions.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return e.Extensions.StatusCode
		}
	}
	return 0
}

// query runs one GraphQL query and decodes data into out. A null field in
// out means the entity was not found (Linear reports it as an error).
func (l Linear) query(ctx context.Context, api *API, q string, vars map[string]any, out any) error {
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []linearError   `json:"errors"`
	}
	err := api.JSON(ctx, http.MethodPost, l.base(), []string{"graphql"}, nil, map[string]any{"query": q, "variables": vars}, &res)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusBadRequest {
		var body struct {
			Errors []linearError `json:"errors"`
		}
		_ = json.Unmarshal(he.Body, &body)
		if st := linearStatus(body.Errors); st != 0 {
			return &HTTPError{Status: st}
		}
		if len(body.Errors) > 0 {
			// Entity not found / invalid identifier.
			return &HTTPError{Status: http.StatusNotFound}
		}
	}
	if err != nil {
		return err
	}
	if st := linearStatus(res.Errors); st != 0 {
		return &HTTPError{Status: st}
	}
	if len(res.Data) == 0 || string(res.Data) == "null" {
		return errors.New("linear: empty response")
	}
	return json.Unmarshal(res.Data, out)
}

const linearIssueQuery = `query($id: String!) {
  organization { id urlKey }
  issue(id: $id) {
    identifier title url updatedAt
    state { name }
    team { key name }
    assignee { displayName name }
  }
}`

func (l Linear) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	if !linearIssue.MatchString(ref.ID) {
		return Preview{}, ErrUnsafeRequest
	}
	var data struct {
		Organization struct {
			URLKey string `json:"urlKey"`
		} `json:"organization"`
		Issue *struct {
			Identifier string                     `json:"identifier"`
			Title      string                     `json:"title"`
			URL        string                     `json:"url"`
			UpdatedAt  time.Time                  `json:"updatedAt"`
			State      struct{ Name string }      `json:"state"`
			Team       struct{ Key, Name string } `json:"team"`
			Assignee   *struct {
				DisplayName string `json:"displayName"`
				Name        string `json:"name"`
			} `json:"assignee"`
		} `json:"issue"`
	}
	if err := l.query(ctx, api, linearIssueQuery, map[string]any{"id": ref.ID}, &data); err != nil {
		return Preview{}, err
	}
	if ref.URL != "" {
		// The grant must belong to the link's workspace.
		u, err := url.Parse(ref.URL)
		if err != nil {
			return Preview{}, ErrUnsafeRequest
		}
		key, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "/")
		if data.Organization.URLKey == "" || !strings.EqualFold(key, data.Organization.URLKey) {
			return Preview{}, &HTTPError{Status: http.StatusNotFound}
		}
	}
	is := data.Issue
	if is == nil || !strings.EqualFold(is.Identifier, ref.ID) {
		return Preview{}, &HTTPError{Status: http.StatusNotFound}
	}
	link := ""
	if u, err := url.Parse(is.URL); err == nil && u.Scheme == "https" && u.User == nil && strings.EqualFold(u.Host, "linear.app") {
		link = u.String()
	}
	assignee := "Unassigned"
	if is.Assignee != nil {
		assignee = firstNonEmpty(is.Assignee.DisplayName, is.Assignee.Name, assignee)
	}
	return Preview{
		Ref: Ref{URL: link}, Title: is.Title, Status: is.State.Name, Icon: "bi-kanban",
		Meta: []string{firstNonEmpty(is.Team.Name, is.Team.Key), assignee}, UpdatedAt: is.UpdatedAt,
	}, nil
}

// LinearOAuth is the viewer-consent app: read scope, PKCE, user actor.
// Linear rotates the refresh token on every refresh; previewauth stores the
// new one. apiBase "" is production.
func LinearOAuth(clientID, clientSecret, apiBase string) previewauth.Provider {
	l := Linear{APIBase: apiBase}
	return previewauth.Provider{
		ID: linearProvider, Name: "Linear",
		Notice:   "Read-only access to the Linear issues your account can see.",
		AuthURL:  "https://linear.app/oauth/authorize",
		TokenURL: l.base() + "/oauth/token", RevokeURL: l.base() + "/oauth/revoke",
		ClientID: clientID, ClientSecret: clientSecret,
		Scopes: []string{"read"}, PKCE: true,
		// prompt=consent lets a viewer connect another workspace.
		AuthParams: map[string]string{"actor": "user", "prompt": "consent"},
		Identify: func(ctx context.Context, client *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			hosts := map[string]bool{}
			for _, h := range l.APIHosts() {
				hosts[strings.ToLower(h)] = true
			}
			var data struct {
				Viewer       struct{ Name string }     `json:"viewer"`
				Organization struct{ ID, Name string } `json:"organization"`
			}
			api := &API{client: client, token: tok.AccessToken, hosts: hosts}
			if err := l.query(ctx, api, `{ viewer { name } organization { id name } }`, nil, &data); err != nil || data.Organization.ID == "" {
				return nil, previewauth.ErrExchange
			}
			return []previewauth.Grant{{WorkspaceID: data.Organization.ID, WorkspaceName: data.Organization.Name, AccountName: data.Viewer.Name}}, nil
		},
	}
}
