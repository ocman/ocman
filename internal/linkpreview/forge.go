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
)

// Forge previews GitHub or Forgejo/Gitea pull requests, issues and commits
// on one exact web host.
//
// With the viewer's own grant any resource the viewer can see resolves.
// Without one (a Fallback) the owner machine's env/CLI token is used, and
// only public repositories are previewed, except for the owner's own direct
// request (WithOwnerAccess): that owner-wide fallback never reaches another
// viewer. A private repository then asks the viewer to connect.
type Forge struct {
	ID      string // previewauth provider ID: "github" or "forgejo:<host>"
	Host    string // exact web host[:port], lowercase
	APIBase string // https://api.github.com or https://<host>/api/v1
	// Gitea: Forgejo/Gitea paths (/pulls/ web path, /git/commits/ API).
	Gitea bool
	// OwnerToken is the owner machine's env/CLI token; "" is anonymous.
	OwnerToken string
	// Connectable: a viewer OAuth app is configured, so a private resource
	// offers Connect instead of a plain link.
	Connectable bool
}

func (f Forge) Provider() string      { return f.ID }
func (f Forge) FallbackToken() string { return f.OwnerToken }

func (f Forge) APIHosts() []string {
	u, _ := url.Parse(f.APIBase)
	return []string{u.Host}
}

var (
	forgeName   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	forgeNumber = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	forgeSHA    = regexp.MustCompile(`^[0-9a-fA-F]{5,40}$`)
	forgeID     = regexp.MustCompile(`^([^/#@]+)/([^/#@]+)(#|@)([0-9A-Za-z]+)$`)
)

func validForgeName(s string) bool { return forgeName.MatchString(s) && s != "." && s != ".." }

// ParseURL accepts https://<Host>/<owner>/<repo>/(pull|pulls|issues|commit)/<id>[/...].
func (f Forge) ParseURL(u *url.URL) (Ref, bool) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Scheme != "https" || !strings.EqualFold(u.Host, f.Host) || len(parts) < 4 ||
		!validForgeName(parts[0]) || !validForgeName(parts[1]) {
		return Ref{}, false
	}
	pull := "pull"
	if f.Gitea {
		pull = "pulls"
	}
	repo := parts[0] + "/" + parts[1]
	web := "https://" + strings.ToLower(f.Host) + "/" + repo + "/"
	switch {
	case parts[2] == pull && forgeNumber.MatchString(parts[3]):
		return Ref{Kind: "pr", ID: repo + "#" + parts[3], URL: web + pull + "/" + parts[3]}, true
	case parts[2] == "issues" && forgeNumber.MatchString(parts[3]):
		return Ref{Kind: "issue", ID: repo + "#" + parts[3], URL: web + "issues/" + parts[3]}, true
	case parts[2] == "commit" && forgeSHA.MatchString(parts[3]):
		return Ref{Kind: "commit", ID: repo + "@" + parts[3], URL: web + "commit/" + parts[3]}, true
	}
	return Ref{}, false
}

func (f Forge) ParseIdentifier(string) (Ref, bool) { return Ref{}, false }

type forgeItem struct {
	Title    string `json:"title"`
	State    string `json:"state"`
	Merged   bool   `json:"merged"`
	MergedAt string `json:"merged_at"`
	Updated  string `json:"updated_at"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
	// commit
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

func (f Forge) needGrant() error {
	if f.Connectable {
		return ErrNeedsGrant
	}
	return &HTTPError{Status: http.StatusNotFound}
}

func (f Forge) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	m := forgeID.FindStringSubmatch(ref.ID)
	if m == nil || !validForgeName(m[1]) || !validForgeName(m[2]) {
		return Preview{}, ErrUnsafeRequest
	}
	owner, repo, id := m[1], m[2], m[4]
	if api.PublicOnly() {
		// Checked before the resource is read, so no private data is fetched.
		var r struct {
			Private    bool   `json:"private"`
			Internal   bool   `json:"internal"`
			Visibility string `json:"visibility"`
		}
		var he *HTTPError
		err := api.JSON(ctx, http.MethodGet, f.APIBase, []string{"repos", owner, repo}, nil, nil, &r)
		if errors.As(err, &he) && (he.Status == http.StatusNotFound || he.Status == http.StatusForbidden) {
			return Preview{}, f.needGrant() // private repos look missing without access
		}
		if err != nil {
			return Preview{}, err
		}
		if r.Private || r.Internal || (r.Visibility != "" && r.Visibility != "public") {
			return Preview{}, f.needGrant()
		}
	}
	path := map[string][]string{
		"pr":     {"repos", owner, repo, "pulls", id},
		"issue":  {"repos", owner, repo, "issues", id},
		"commit": {"repos", owner, repo, "commits", id},
	}[ref.Kind]
	if ref.Kind == "commit" && f.Gitea {
		path = []string{"repos", owner, repo, "git", "commits", id}
	}
	if path == nil {
		return Preview{}, ErrUnsafeRequest
	}
	var it forgeItem
	if err := api.JSON(ctx, http.MethodGet, f.APIBase, path, nil, nil, &it); err != nil {
		return Preview{}, err
	}
	p := Preview{Ref: Ref{URL: ref.URL}, Title: it.Title, Meta: []string{it.User.Login}}
	updated := it.Updated
	switch {
	case ref.Kind == "commit":
		p.Title, _, _ = strings.Cut(it.Commit.Message, "\n")
		p.Status, p.Icon = "Commit", "bi-braces"
		p.Meta = []string{firstNonEmpty(it.Author.Login, it.Commit.Author.Name), id[:min(7, len(id))]}
		updated = it.Commit.Author.Date
	case ref.Kind == "pr" && (it.Merged || it.MergedAt != ""):
		p.Status, p.Icon = "Merged", "bi-git"
	case it.State == "closed" && ref.Kind == "pr":
		p.Status, p.Icon = "Closed", "bi-x-circle"
	case it.State == "closed":
		p.Status, p.Icon = "Closed", "bi-check-circle"
	default:
		p.Status, p.Icon = "Open", "bi-circle"
	}
	p.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return p, nil
}

// forgeIdentify names the account behind a fresh token (GET /user).
func forgeIdentify(f Forge, workspace, name string) func(context.Context, *http.Client, previewauth.Token) ([]previewauth.Grant, error) {
	return func(ctx context.Context, client *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
		hosts := map[string]bool{}
		for _, h := range f.APIHosts() {
			hosts[strings.ToLower(h)] = true
		}
		var u struct {
			Login string `json:"login"`
		}
		api := &API{client: client, token: tok.AccessToken, hosts: hosts}
		if err := api.JSON(ctx, http.MethodGet, f.APIBase, []string{"user"}, nil, nil, &u); err != nil || u.Login == "" {
			return nil, previewauth.ErrExchange
		}
		return []previewauth.Grant{{WorkspaceID: workspace, WorkspaceName: name, AccountName: u.Login}}, nil
	}
}

// GitHubOAuth is the viewer-consent app for GitHub previews. Register a
// GitHub App (recommended: read-only Metadata, Pull requests, Issues and
// Contents permissions, expiring user tokens) and use its client ID and
// secret. Its user tokens carry the app's permissions, not OAuth scopes,
// so no scope is requested. apiBase "" is production.
func GitHubOAuth(clientID, clientSecret, apiBase string) previewauth.Provider {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	f := Forge{ID: "github", Host: "github.com", APIBase: apiBase}
	return previewauth.Provider{
		ID: f.ID, Name: "GitHub",
		Notice:   "Read-only access through the GitHub App, limited to repositories it is installed on and that you can see.",
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
		ClientID: clientID, ClientSecret: clientSecret, PKCE: true,
		// ponytail: GitHub revocation is DELETE /applications/{id}/grant;
		// Disconnect forgets the token here, revoke it under GitHub settings.
		DecodeToken: func(raw map[string]any) (map[string]any, error) {
			if raw["error"] == "bad_refresh_token" {
				return nil, previewauth.ErrRevoked
			}
			return raw, nil
		},
		Identify:  forgeIdentify(f, "github.com", "GitHub"),
		TokenHelp: "Create a fine-grained personal access token at https://github.com/settings/personal-access-tokens/new with read-only Contents, Issues and Pull requests access.",
	}
}

// ForgejoOAuth is the viewer-consent app registered on one Forgejo host
// (https only). Forgejo OAuth tokens have no granular scopes, which Notice
// states before consent.
func ForgejoOAuth(host, clientID, clientSecret string) previewauth.Provider {
	f := Forge{ID: "forgejo:" + host, Host: host, APIBase: "https://" + host + "/api/v1", Gitea: true}
	return previewauth.Provider{
		ID: f.ID, Name: "Forgejo (" + host + ")",
		Notice:   "Forgejo OAuth has no granular scopes: the grant can read and change everything your account can on " + host + ". Ocman only reads previews with it.",
		AuthURL:  "https://" + host + "/login/oauth/authorize",
		TokenURL: "https://" + host + "/login/oauth/access_token",
		ClientID: clientID, ClientSecret: clientSecret, PKCE: true,
		Identify:  forgeIdentify(f, host, host),
		TokenHelp: "Create an access token at https://" + host + "/user/settings/applications with read access to repositories and issues.",
	}
}
