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
	ErrNoOAuth         = errors.New("provider has no sign-in app")
	ErrNoTokenLogin    = errors.New("provider does not accept personal tokens")
)

// stateTTL bounds how long a consent screen may stay open.
const stateTTL = 10 * time.Minute

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
	// TokenHelp, when set, lets a viewer paste a personal access or API
	// token instead of OAuth consent, and says how to create one.
	TokenHelp string
	// IdentifyToken identifies a pasted token; nil uses Identify.
	IdentifyToken func(ctx context.Context, client *http.Client, tok Token) ([]Grant, error)
}

// OAuth reports whether a sign-in app is configured for consent.
func (p Provider) OAuth() bool { return p.AuthURL != "" && p.ClientID != "" }

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
	if !p.OAuth() {
		return "", ErrNoOAuth
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
	return s.ReturnTo, m.store(ctx, viewerID, ownerID, p, grants, tok)
}

func (m *Manager) store(ctx context.Context, viewerID, ownerID string, p Provider, grants []Grant, tok Token) error {
	for _, g := range grants {
		if err := m.db.PutPreviewCredential(ctx, state.PreviewCredential{
			ViewerID: viewerID, OwnerID: ownerID, Provider: p.ID, WorkspaceID: g.WorkspaceID,
			WorkspaceName: g.WorkspaceName, AccountName: g.AccountName, Sites: g.Sites,
			AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: tok.ExpiresAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ConnectToken stores a viewer's pasted personal token after checking it
// with the provider. It never expires locally; a provider rejection (401)
// forgets it like any other grant.
// p need not be registered yet: a token may add a new forge host.
func (m *Manager) ConnectToken(ctx context.Context, viewerID, ownerID string, p Provider, token string) error {
	identify := p.IdentifyToken
	if identify == nil {
		identify = p.Identify
	}
	if p.TokenHelp == "" || identify == nil {
		return ErrNoTokenLogin
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 4096 || strings.ContainsAny(token, " \r\n\t") {
		return ErrExchange
	}
	tok := Token{AccessToken: token}
	grants, err := identify(ctx, m.client, tok)
	if err != nil || len(grants) == 0 {
		return ErrExchange
	}
	return m.store(ctx, viewerID, ownerID, p, grants, tok)
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

// String keeps a Provider's client secret out of %v / %+v logging.
func (p Provider) String() string { return fmt.Sprintf("Provider(%s)", p.ID) }
