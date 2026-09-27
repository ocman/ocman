// Package previewauth runs the OAuth authorization-code flow that lets one
// browser (a viewer) connect a preview provider, and keeps the resulting
// tokens server-side. Tokens are stored per viewer, owner machine, provider
// and workspace in state.db (encrypted); they are never returned to a client
// or logged. Client secrets live only in Provider, so confidential
// server-side clients work the same as public PKCE clients.
package previewauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/state"
)

var (
	ErrUnknownProvider = errors.New("unknown preview provider")
	ErrInvalidState    = errors.New("invalid or expired authorization state")
	ErrNotConnected    = errors.New("provider not connected")
	ErrExpired         = errors.New("provider authorization expired")
	ErrRevoked         = errors.New("provider authorization revoked")
	ErrExchange        = errors.New("provider token exchange failed")
)

// stateTTL bounds how long a consent screen may stay open.
const stateTTL = 10 * time.Minute

// refreshSkew refreshes tokens slightly before their reported expiry.
const refreshSkew = time.Minute

// Token is a provider token response. Raw is the decoded response body so
// Identify can read provider-specific workspace fields.
type Token struct {
	AccessToken, RefreshToken string
	ExpiresAt                 time.Time
	Raw                       map[string]any
}

// Grant is one workspace a token reaches.
type Grant struct {
	WorkspaceID, WorkspaceName, AccountName string
	Sites                                   []state.PreviewSite
}

// Provider is one OAuth application registered server-side.
type Provider struct {
	ID, Name string
	// Notice describes the access a grant gives, shown before consent.
	Notice                       string
	AuthURL, TokenURL, RevokeURL string
	ClientID, ClientSecret       string
	Scopes                       []string
	PKCE                         bool
	// BasicAuth sends client credentials as HTTP Basic instead of form fields.
	BasicAuth bool
	// AuthParams are extra fixed authorize-URL parameters.
	AuthParams map[string]string
	// JSONBody sends token and revoke requests as a JSON object instead of
	// a form (Notion). Headers are extra fixed headers on those requests.
	JSONBody bool
	Headers  map[string]string
	// DecodeToken, when set, returns the object in a token response that
	// holds access_token/refresh_token/expires_in, or a sentinel error
	// (Slack nests user tokens under authed_user and fails with ok:false).
	DecodeToken func(raw map[string]any) (map[string]any, error)
	// Identify maps a fresh token to the workspaces it grants. Nil means a
	// single "default" workspace.
	Identify func(ctx context.Context, client *http.Client, tok Token) ([]Grant, error)
}

// Manager owns the consent flow for a fixed provider set.
type Manager struct {
	db          *state.DB
	client      *http.Client
	providers   map[string]Provider
	redirectURI string
	refresh     singleflight.Group
}

// New returns a Manager. redirectURI is the exact callback URL registered
// with every provider. A nil client uses a no-redirect client with timeout.
func New(db *state.DB, redirectURI string, client *http.Client, providers ...Provider) *Manager {
	if client == nil {
		client = &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	m := &Manager{db: db, client: client, redirectURI: redirectURI, providers: map[string]Provider{}}
	for _, p := range providers {
		m.providers[p.ID] = p
	}
	return m
}

// Client is the HTTP client used for provider calls.
func (m *Manager) Client() *http.Client { return m.client }

// Provider reports a configured provider.
func (m *Manager) Provider(id string) (Provider, bool) {
	p, ok := m.providers[id]
	return p, ok
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashState(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SafeReturnPath accepts only a same-origin absolute path, never a URL
// that could redirect off-site ("//host", "/\host", schemes, controls).
func SafeReturnPath(p string) string {
	if p == "" || p[0] != '/' || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return p
}

// Begin records a one-time state bound to viewer and owner and returns the
// provider authorize URL the browser should navigate to.
func (m *Manager) Begin(ctx context.Context, viewerID, ownerID, providerID, returnTo string) (string, error) {
	p, ok := m.providers[providerID]
	if !ok {
		return "", ErrUnknownProvider
	}
	st, verifier := randomToken(), randomToken()
	if err := m.db.PutPreviewOAuthState(ctx, hashState(st), state.PreviewOAuthState{
		ViewerID: viewerID, OwnerID: ownerID, Provider: providerID, Verifier: verifier,
		ReturnTo: SafeReturnPath(returnTo), ExpiresAt: time.Now().Add(stateTTL),
	}); err != nil {
		return "", err
	}
	u, err := url.Parse(p.AuthURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range p.AuthParams {
		q.Set(k, v)
	}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", m.redirectURI)
	q.Set("state", st)
	if len(p.Scopes) > 0 {
		q.Set("scope", strings.Join(p.Scopes, " "))
	}
	if p.PKCE {
		sum := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Complete consumes the state (always, even on failure) and, when it was
// issued to this viewer, exchanges code and stores the grants for the owner
// recorded by Begin (the redirect URI is shared by every owner).
// providerErr is the callback's error parameter (e.g. access_denied).
func (m *Manager) Complete(ctx context.Context, viewerID, st, code, providerErr string) (string, error) {
	if st == "" {
		return "", ErrInvalidState
	}
	s, err := m.db.TakePreviewOAuthState(ctx, hashState(st))
	if err != nil || viewerID == "" || s.ViewerID != viewerID {
		return "", ErrInvalidState
	}
	ownerID := s.OwnerID
	p, ok := m.providers[s.Provider]
	if !ok {
		return s.ReturnTo, ErrUnknownProvider
	}
	if providerErr != "" || code == "" {
		return s.ReturnTo, ErrExchange
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {m.redirectURI}}
	if p.PKCE {
		form.Set("code_verifier", s.Verifier)
	}
	tok, err := m.tokenRequest(ctx, p, form)
	if err != nil {
		return s.ReturnTo, err
	}
	grants := []Grant{{WorkspaceID: "default"}}
	if p.Identify != nil {
		if grants, err = p.Identify(ctx, m.client, tok); err != nil || len(grants) == 0 {
			return s.ReturnTo, ErrExchange
		}
	}
	for _, g := range grants {
		if err := m.db.PutPreviewCredential(ctx, state.PreviewCredential{
			ViewerID: viewerID, OwnerID: ownerID, Provider: p.ID, WorkspaceID: g.WorkspaceID,
			WorkspaceName: g.WorkspaceName, AccountName: g.AccountName, Sites: g.Sites,
			AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: tok.ExpiresAt,
		}); err != nil {
			return s.ReturnTo, err
		}
	}
	return s.ReturnTo, nil
}

// tokenRequest posts to the token endpoint. Errors never include the body:
// a provider may echo credentials in it.
func (m *Manager) tokenRequest(ctx context.Context, p Provider, form url.Values) (Token, error) {
	if !p.BasicAuth {
		form.Set("client_id", p.ClientID)
		if p.ClientSecret != "" {
			form.Set("client_secret", p.ClientSecret)
		}
	}
	req, err := p.post(ctx, p.TokenURL, form)
	if err != nil {
		return Token{}, ErrExchange
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return Token{}, ErrExchange
	}
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return Token{}, ErrExchange
	}
	if code, _ := raw["error"].(string); code == "invalid_grant" {
		return Token{}, ErrRevoked
	}
	fields := raw
	if p.DecodeToken != nil {
		if fields, err = p.DecodeToken(raw); err != nil {
			return Token{}, err
		}
	}
	access, _ := fields["access_token"].(string)
	if resp.StatusCode != http.StatusOK || access == "" {
		return Token{}, ErrExchange
	}
	tok := Token{AccessToken: access, Raw: raw}
	tok.RefreshToken, _ = fields["refresh_token"].(string)
	if secs, ok := fields["expires_in"].(float64); ok && secs > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(secs) * time.Second)
	}
	return tok, nil
}

// post builds a client-authenticated POST of form to u, as a form or (with
// JSONBody) a flat JSON object.
func (p Provider) post(ctx context.Context, u string, form url.Values) (*http.Request, error) {
	body, ctype := form.Encode(), "application/x-www-form-urlencoded"
	if p.JSONBody {
		obj := map[string]string{}
		for k := range form {
			obj[k] = form.Get(k)
		}
		b, _ := json.Marshal(obj)
		body, ctype = string(b), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Accept", "application/json")
	if p.BasicAuth {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	}
	return req, nil
}

// AccessToken returns a usable access token for a viewer's workspace,
// refreshing it first when it is about to expire. Concurrent callers share
// one refresh so a rotating refresh token is redeemed exactly once.
func (m *Manager) AccessToken(ctx context.Context, viewerID, ownerID, providerID, workspaceID string) (string, error) {
	p, ok := m.providers[providerID]
	if !ok {
		return "", ErrUnknownProvider
	}
	key := strings.Join([]string{viewerID, ownerID, providerID, workspaceID}, "\x00")
	v, err, _ := m.refresh.Do(key, func() (any, error) {
		c, err := m.db.PreviewCredential(ctx, viewerID, ownerID, providerID, workspaceID)
		if errors.Is(err, state.ErrPreviewNotFound) {
			return "", ErrNotConnected
		}
		if err != nil {
			return "", err
		}
		if c.ExpiresAt.IsZero() || time.Now().Add(refreshSkew).Before(c.ExpiresAt) {
			return c.AccessToken, nil
		}
		if c.RefreshToken == "" {
			return "", ErrExpired
		}
		tok, err := m.tokenRequest(ctx, p, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}})
		if errors.Is(err, ErrRevoked) {
			_ = m.Revoked(ctx, viewerID, ownerID, providerID, workspaceID)
			return "", ErrRevoked
		}
		if err != nil {
			return "", err
		}
		c.AccessToken, c.ExpiresAt = tok.AccessToken, tok.ExpiresAt
		if tok.RefreshToken != "" {
			c.RefreshToken = tok.RefreshToken
		}
		if err := m.db.PutPreviewCredential(ctx, c); err != nil {
			return "", err
		}
		return c.AccessToken, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Revoked forgets a grant the provider rejected (401 or invalid_grant).
func (m *Manager) Revoked(ctx context.Context, viewerID, ownerID, providerID, workspaceID string) error {
	_, err := m.db.DeletePreviewCredentials(ctx, viewerID, ownerID, providerID, workspaceID)
	return err
}

// Disconnect deletes a viewer's grants (all workspaces when workspaceID is
// empty) and revokes them at the provider on a best-effort basis.
func (m *Manager) Disconnect(ctx context.Context, viewerID, ownerID, providerID, workspaceID string) error {
	if _, ok := m.providers[providerID]; !ok {
		return ErrUnknownProvider
	}
	gone, err := m.db.DeletePreviewCredentials(ctx, viewerID, ownerID, providerID, workspaceID)
	m.revokeAll(ctx, gone)
	return err
}

// SignOut forgets the viewer and its grants for every owner (browser
// sign-out).
func (m *Manager) SignOut(ctx context.Context, viewerID string) error {
	gone, err := m.db.DeletePreviewViewer(ctx, viewerID)
	m.revokeAll(ctx, gone)
	return err
}

func (m *Manager) revokeAll(ctx context.Context, creds []state.PreviewCredential) {
	seen := map[string]bool{}
	for _, c := range creds {
		p, ok := m.providers[c.Provider]
		if !ok || p.RevokeURL == "" || seen[c.AccessToken] {
			continue
		}
		seen[c.AccessToken] = true
		form := url.Values{"token": {c.AccessToken}}
		if !p.BasicAuth {
			form.Set("client_id", p.ClientID)
			if p.ClientSecret != "" {
				form.Set("client_secret", p.ClientSecret)
			}
		}
		req, err := p.post(ctx, p.RevokeURL, form)
		if err != nil {
			continue
		}
		if resp, err := m.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}
}

// Connection is the client-safe view of one grant: display names only.
type Connection struct {
	WorkspaceID   string              `json:"workspaceId"`
	WorkspaceName string              `json:"workspaceName"`
	AccountName   string              `json:"accountName"`
	Sites         []state.PreviewSite `json:"sites"`
	State         string              `json:"state"` // connected | expired
}

// ProviderStatus is the client-safe status of one provider for a viewer.
type ProviderStatus struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Notice      string       `json:"notice,omitempty"`
	Connections []Connection `json:"connections"`
}

// Status lists every configured provider with the viewer's connections.
func (m *Manager) Status(ctx context.Context, viewerID, ownerID string) ([]ProviderStatus, error) {
	creds, err := m.db.PreviewCredentials(ctx, viewerID, ownerID, "")
	if err != nil {
		return nil, err
	}
	byProvider := map[string][]Connection{}
	for _, c := range creds {
		st := "connected"
		if !c.Refreshable && !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt) {
			st = "expired"
		}
		byProvider[c.Provider] = append(byProvider[c.Provider], Connection{
			WorkspaceID: c.WorkspaceID, WorkspaceName: c.WorkspaceName, AccountName: c.AccountName,
			Sites: append([]state.PreviewSite{}, c.Sites...), State: st,
		})
	}
	out := make([]ProviderStatus, 0, len(m.providers))
	for id, p := range m.providers {
		conns := byProvider[id]
		if conns == nil {
			conns = []Connection{}
		}
		out = append(out, ProviderStatus{ID: id, Name: p.Name, Notice: p.Notice, Connections: conns})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// String keeps a Provider's client secret out of %v / %+v logging.
func (p Provider) String() string { return fmt.Sprintf("Provider(%s)", p.ID) }
