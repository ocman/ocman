package linkpreview

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// GitLab previews merge requests, issues and commits on one exact GitLab
// host: gitlab.com or an operator-allowlisted self-managed instance. Each
// host is its own provider ("gitlab:<host>") with its own OAuth app, so a
// grant for one instance is never sent to another.
//
// Without the viewer's grant public projects preview anonymously; anything
// else asks the viewer to connect. Every API call dials through a pinned,
// address-checked dialer (see gitlabDial), so a rebound DNS name cannot
// steer the request to loopback, link-local/metadata addresses or, for
// gitlab.com, a private network. Redirects are never followed (Service).
type GitLab struct {
	Host string // exact web host[:port], lowercase
	// APIBase overrides https://<Host>/api/v4 (tests).
	APIBase string
}

// gitlabAllow overrides the dial address policy (tests only: httptest
// servers listen on loopback).
var gitlabAllow func(netip.Addr) bool

const gitlabCom = "gitlab.com"

func (g GitLab) Provider() string { return "gitlab:" + g.Host }

// FallbackToken is anonymous: GitLab has no owner-wide token here.
func (g GitLab) FallbackToken() string { return "" }

func (g GitLab) base() string {
	if g.APIBase != "" {
		return g.APIBase
	}
	return "https://" + g.Host + "/api/v4"
}

func (g GitLab) APIHosts() []string {
	u, _ := url.Parse(g.base())
	return []string{u.Host}
}

var gitlabID = regexp.MustCompile(`^([^!#@]+)([!#@])([0-9A-Za-z]+)$`)

// ParseURL accepts https://<Host>/<group>[/<subgroup>...]/<project>/-/
// (merge_requests|issues|commit)/<id>[/...].
func (g GitLab) ParseURL(u *url.URL) (Ref, bool) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	dash := -1
	for i, p := range parts {
		if p == "-" {
			dash = i
			break
		}
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, g.Host) || dash < 2 || dash > 20 || len(parts) < dash+3 {
		return Ref{}, false
	}
	for _, p := range parts[:dash] {
		if !validForgeName(p) {
			return Ref{}, false
		}
	}
	project, kind, id := strings.Join(parts[:dash], "/"), parts[dash+1], parts[dash+2]
	web := "https://" + strings.ToLower(g.Host) + "/" + project + "/-/"
	switch {
	case kind == "merge_requests" && forgeNumber.MatchString(id):
		return Ref{Kind: "pr", ID: project + "!" + id, URL: web + "merge_requests/" + id}, true
	case kind == "issues" && forgeNumber.MatchString(id):
		return Ref{Kind: "issue", ID: project + "#" + id, URL: web + "issues/" + id}, true
	case kind == "commit" && forgeSHA.MatchString(id):
		return Ref{Kind: "commit", ID: project + "@" + id, URL: web + "commit/" + id}, true
	}
	return Ref{}, false
}

func (g GitLab) ParseIdentifier(string) (Ref, bool) { return Ref{}, false }

type gitlabItem struct {
	Title     string `json:"title"`
	State     string `json:"state"`
	UpdatedAt string `json:"updated_at"`
	Author    struct {
		Username string `json:"username"`
	} `json:"author"`
	// commit
	AuthorName    string `json:"author_name"`
	CommittedDate string `json:"committed_date"`
}

func (g GitLab) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	m := gitlabID.FindStringSubmatch(ref.ID)
	if m == nil {
		return Preview{}, ErrUnsafeRequest
	}
	project, id := m[1], m[3]
	api = g.guard(api).WithEncodedSlashes()
	if api.PublicOnly() {
		// Checked before the resource is read, so no private data is fetched.
		var p struct {
			Visibility string `json:"visibility"`
		}
		var he *HTTPError
		err := api.JSON(ctx, http.MethodGet, g.base(), []string{"projects", project}, nil, nil, &p)
		if errors.As(err, &he) && (he.Status == http.StatusNotFound || he.Status == http.StatusForbidden || he.Status == http.StatusUnauthorized) {
			return Preview{}, ErrNeedsGrant // private and internal projects look missing anonymously
		}
		if err != nil {
			return Preview{}, err
		}
		if p.Visibility != "public" {
			return Preview{}, ErrNeedsGrant
		}
	}
	path := map[string][]string{
		"pr":     {"projects", project, "merge_requests", id},
		"issue":  {"projects", project, "issues", id},
		"commit": {"projects", project, "repository", "commits", id},
	}[ref.Kind]
	if path == nil {
		return Preview{}, ErrUnsafeRequest
	}
	var it gitlabItem
	if err := api.JSON(ctx, http.MethodGet, g.base(), path, nil, nil, &it); err != nil {
		return Preview{}, err
	}
	p := Preview{Ref: Ref{URL: ref.URL}, Title: it.Title, Meta: []string{it.Author.Username}}
	updated := it.UpdatedAt
	switch {
	case ref.Kind == "commit":
		p.Status, p.Icon = "Commit", "bi-braces"
		p.Meta = []string{it.AuthorName, id[:min(8, len(id))]}
		updated = it.CommittedDate
	case it.State == "merged":
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

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// gitlabAddrOK: never loopback, link-local (cloud metadata), unspecified or
// multicast; private and CGNAT ranges only for a self-managed host, which
// commonly lives on an intranet or tailnet.
func gitlabAddrOK(a netip.Addr, allowPrivate bool) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	return allowPrivate || (!a.IsPrivate() && !cgnat.Contains(a))
}

// gitlabDial resolves once, refuses the dial if any address fails ok, and
// connects to the checked address, so no second lookup can rebind it. TLS
// still verifies the certificate against the URL host.
func gitlabDial(ok func(netip.Addr) bool) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !ok(ip) {
				return nil, ErrUnsafeRequest
			}
		}
		if len(ips) == 0 {
			return nil, ErrUnsafeRequest
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
}

// guardClient returns a copy of client whose transport dials through
// gitlabDial, direct (no proxy). ponytail: one transport per call, no
// keep-alive; cache per host if preview volume makes TLS setup matter.
func (g GitLab) guardClient(client *http.Client) *http.Client {
	ok := gitlabAllow
	if ok == nil {
		private := !strings.EqualFold(g.Host, gitlabCom)
		ok = func(a netip.Addr) bool { return gitlabAddrOK(a, private) }
	}
	base, _ := client.Transport.(*http.Transport)
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	t := base.Clone()
	t.Proxy, t.DialContext, t.DialTLSContext, t.DisableKeepAlives = nil, gitlabDial(ok), nil, true
	c := *client
	c.Transport = t
	return &c
}

func (g GitLab) guard(api *API) *API {
	c := *api
	c.client = g.guardClient(api.client)
	return &c
}

// GitLabOAuth is the viewer-consent app on one GitLab host: authorization
// code + PKCE with the read_api scope. GitLab access tokens expire after two
// hours; previewauth refreshes them and stores the rotated refresh token.
// clientSecret may be empty for a non-confidential app. apiBase and
// authBase "" are https://<host>/api/v4 and https://<host>.
func GitLabOAuth(host, clientID, clientSecret, apiBase, authBase string) previewauth.Provider {
	g := GitLab{Host: host, APIBase: apiBase}
	if authBase == "" {
		authBase = "https://" + host
	}
	return previewauth.Provider{
		ID: g.Provider(), Name: "GitLab (" + host + ")",
		Notice:   "Read-only API access (read_api) to the GitLab projects your account can see on " + host + ".",
		AuthURL:  authBase + "/oauth/authorize",
		TokenURL: authBase + "/oauth/token",
		// Revoke takes client_id, client_secret and token as a form.
		RevokeURL: authBase + "/oauth/revoke",
		ClientID:  clientID, ClientSecret: clientSecret, PKCE: true,
		Scopes: []string{"read_api"},
		Identify: func(ctx context.Context, client *http.Client, tok previewauth.Token) ([]previewauth.Grant, error) {
			api := g.guard(&API{client: client, token: tok.AccessToken, hosts: map[string]bool{strings.ToLower(g.APIHosts()[0]): true}})
			var u struct {
				Username string `json:"username"`
			}
			if err := api.JSON(ctx, http.MethodGet, g.base(), []string{"user"}, nil, nil, &u); err != nil || u.Username == "" {
				return nil, previewauth.ErrExchange
			}
			return []previewauth.Grant{{WorkspaceID: host, WorkspaceName: host, AccountName: u.Username}}, nil
		},
		TokenHelp: "Create a personal access token with the read_api scope at https://" + host + "/-/user_settings/personal_access_tokens.",
	}
}
