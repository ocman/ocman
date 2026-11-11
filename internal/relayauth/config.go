// Package relayauth owns browser sign-in and user-bound webhook enrollment.
// Provider access tokens are used only to read identity and are never retained.
package relayauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Store interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	DeletePrefix(context.Context, string) error
}

// Provider configures one login choice. ID is stable and namespaced into users.
// Kind selects the identity protocol, not an account-linking policy. Accounts
// from different providers are deliberately never linked by email address.
type Provider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"-"`
}

type Config struct {
	PublicURL  string
	Providers  []Provider
	Store      Store
	HTTP       *http.Client
	Now        func() time.Time
	TrustProxy bool
}

const connectTTL = 10 * time.Minute
const credentialTTL = 90 * 24 * time.Hour
const maxPending = 1000

type connection struct {
	id, pollHash, token string
	expires             time.Time
}

type login struct {
	connection, provider, verifier, user string
	expires                              time.Time
}

type Service struct {
	cfg         Config
	mu          sync.Mutex
	connections map[string]*connection
	logins      map[string]*login
	limits      map[string]requestWindow
	// Provider endpoints are fixed in production and replaceable in package tests.
	endpoints map[string]endpoints
}

var providerID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func New(cfg Config) (*Service, error) {
	u, err := url.Parse(cfg.PublicURL)
	if err != nil {
		return nil, errors.New("invalid relay SSO public URL")
	}
	secure := u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))
	if !secure || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return nil, errors.New("relay SSO public URL must be an HTTPS origin, or HTTP loopback for development")
	}
	if cfg.Store == nil || len(cfg.Providers) == 0 {
		return nil, errors.New("relay SSO requires storage and at least one provider")
	}
	seen := map[string]bool{}
	for _, p := range cfg.Providers {
		if !providerID.MatchString(p.ID) || seen[p.ID] || p.Name == "" || p.ClientID == "" || p.ClientSecret == "" || (p.Kind != "github" && p.Kind != "google") {
			return nil, errors.New("invalid or duplicate relay SSO provider")
		}
		seen[p.ID] = true
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	return &Service{cfg: cfg, connections: map[string]*connection{}, logins: map[string]*login{}, limits: map[string]requestWindow{}, endpoints: defaultEndpoints()}, nil
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/providers", s.limited("providers", 120, s.providers))
	mux.HandleFunc("POST /auth/connect", s.limited("connect", 10, s.connect))
	mux.HandleFunc("POST /auth/poll", s.limited("poll", 120, s.poll))
	mux.HandleFunc("GET /auth/connect/{id}", s.limited("choose", 60, s.choose))
	mux.HandleFunc("GET /auth/login/{id}/{provider}", s.limited("login", 20, s.startLogin))
	mux.HandleFunc("GET /auth/callback/{provider}", s.limited("callback", 20, s.callback))
	mux.HandleFunc("POST /auth/approve", s.limited("approve", 20, s.approve))
	mux.HandleFunc("DELETE /auth/enrollment", s.limited("revoke", 20, s.revoke))
}

func (s *Service) provider(id string) (Provider, bool) {
	for _, p := range s.cfg.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// cleanupLocked bounds memory even when abandoned connections never poll.
func (s *Service) cleanupLocked() {
	now := s.cfg.Now()
	for id, c := range s.connections {
		if !now.Before(c.expires) {
			delete(s.connections, id)
		}
	}
	for id, l := range s.logins {
		if !now.Before(l.expires) {
			delete(s.logins, id)
		}
	}
}
