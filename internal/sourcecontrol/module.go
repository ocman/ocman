// Package sourcecontrol owns hub-side forge setup and credential overrides.
package sourcecontrol

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
)

var Schema = state.ModuleSchema{Module: "source-control", Tables: []string{"source_control_credential"}, Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE source_control_credential (host TEXT PRIMARY KEY, provider TEXT NOT NULL, token TEXT NOT NULL)`)
	return err
}}}

// Connection contains no secret. Source describes the credential actually in use.
type Connection struct {
	Host     string           `json:"host"`
	Provider forge.RemoteType `json:"provider"`
	Source   string           `json:"source"`
	Account  string           `json:"account,omitempty"`
	Status   string           `json:"status"`
	Help     string           `json:"help"`
}

type Discovered struct {
	Connection
	Client forge.Forge
}

type Deps struct {
	DB         *state.DB
	Discover   func() []Discovered
	Rediscover func() []Discovered
	// Build is injectable so tests never contact real forges.
	Build func(host string, provider forge.RemoteType, token string) forge.Forge
}

type Module struct {
	d         Deps
	mu        sync.Mutex
	clients   map[string]Discovered
	loaded    bool
	refreshed []Discovered
}

func New(d Deps) *Module {
	if d.Build == nil {
		d.Build = buildClient
	}
	return &Module{d: d}
}

func (*Module) Name() string { return "source-control" }
func (m *Module) Routes(r module.Router) {
	r.Local("GET /api/source-control", m.list)
	r.LocalSetting("POST /api/source-control/credentials", m.save)
	r.Local("POST /api/source-control/refresh", m.refresh)
}

func buildClient(host string, provider forge.RemoteType, token string) forge.Forge {
	client := forgehttp.InstrumentClient(&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	if provider == forge.RemoteTypeGitHub {
		return github.NewWithCredentials(github.DefaultAPIBase, token, client)
	}
	return forgejo.NewWithCredentials(host, "https://"+host, token, client)
}

func validHost(host string, provider forge.RemoteType) bool {
	u, err := url.Parse("https://" + host)
	if err != nil || host == "" || u.Host != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(host, " /\\\t\r\n") {
		return false
	}
	return (provider == forge.RemoteTypeGitHub && host == "github.com") || provider == forge.RemoteTypeForgejo
}

func (m *Module) load(ctx context.Context) error {
	if m.loaded {
		return nil
	}
	clients := map[string]Discovered{}
	if m.d.DB != nil {
		rows, err := m.d.DB.SQL().QueryContext(ctx, `SELECT host, provider, token FROM source_control_credential`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var host, token string
			var provider forge.RemoteType
			if err := rows.Scan(&host, &provider, &token); err != nil {
				return err
			}
			clients[host] = Discovered{Connection: Connection{Host: host, Provider: provider, Source: "saved token"}, Client: m.d.Build(host, provider, token)}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	m.clients, m.loaded = clients, true
	return nil
}

// Resolve fails closed on credential-store errors rather than silently changing accounts.
func (m *Module) Resolve(ctx context.Context, rem forge.Remote) (forge.Forge, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.load(ctx) != nil {
		return nil, false
	}
	c, ok := m.all()[rem.Host]
	return c.Client, ok && c.Provider == rem.Type && c.Client != nil
}

// all is called with mu held. Baseline dependencies stay live until an explicit
// recheck supplies a fresh discovery snapshot; saved overrides remain separate.
func (m *Module) all() map[string]Discovered {
	base := m.refreshed
	if base == nil && m.d.Discover != nil {
		base = m.d.Discover()
	}
	result := make(map[string]Discovered, len(base)+len(m.clients))
	for _, c := range base {
		result[c.Host] = c
	}
	for host, c := range m.clients {
		result[host] = c
	}
	return result
}

func (m *Module) Knows(host string) bool {
	_, ok := m.Resolve(context.Background(), forge.Remote{Host: host, Type: forge.RemoteTypeForgejo})
	return ok
}

// Clients is server-only. HTTP responses must serialize Connection, never Client.
func (m *Module) Clients(ctx context.Context) []Discovered {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.load(ctx) != nil {
		return nil
	}
	result := make([]Discovered, 0, len(m.clients))
	for _, c := range m.all() {
		result = append(result, c)
	}
	return result
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	if err := m.load(r.Context()); err != nil {
		m.mu.Unlock()
		http.Error(w, "Credential store unavailable", http.StatusServiceUnavailable)
		return
	}
	entries := make([]Discovered, 0, len(m.clients))
	for _, c := range m.all() {
		entries = append(entries, c)
	}
	m.mu.Unlock()
	out := make([]Connection, 0, len(entries))
	for _, entry := range entries {
		c := entry.Connection
		c.Status = "unavailable"
		c.Help = "Check the token and network connection on the hub. Git fetch/push authentication is separate."
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		user, err := entry.Client.CurrentUser(ctx)
		cancel()
		if err == nil {
			c.Account, c.Status, c.Help = user.Login, "authenticated", "Forge API requests run on the hub, including for remote checkouts."
		} else if errors.Is(err, forge.ErrUnauthenticated) {
			c.Status = "not authenticated"
			c.Help = "Save a token, or sign in with gh / tea on the hub, then recheck credentials."
		}
		out = append(out, c)
	}
	httpx.WriteJSON(w, map[string]any{"connections": out})
}

func (m *Module) save(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host     string           `json:"host"`
		Provider forge.RemoteType `json:"provider"`
		Token    string           `json:"token"`
	}
	if !httpx.ReadJSON(w, r, 16<<10, &body) {
		return
	}
	body.Host, body.Token = strings.ToLower(strings.TrimSpace(body.Host)), strings.TrimSpace(body.Token)
	if !validHost(body.Host, body.Provider) || strings.ContainsAny(body.Token, "\r\n") {
		http.Error(w, "Use a hostname and a supported provider", http.StatusBadRequest)
		return
	}
	if m.d.DB == nil {
		http.Error(w, "Credential store unavailable", http.StatusServiceUnavailable)
		return
	}
	if body.Token != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if _, err := m.d.Build(body.Host, body.Provider, body.Token).CurrentUser(ctx); err != nil {
			http.Error(w, "Could not verify this token. Check its access and the host connection.", http.StatusBadRequest)
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	if body.Token == "" {
		_, err = m.d.DB.SQL().ExecContext(r.Context(), `DELETE FROM source_control_credential WHERE host=?`, body.Host)
	} else {
		_, err = m.d.DB.SQL().ExecContext(r.Context(), `INSERT INTO source_control_credential VALUES (?,?,?) ON CONFLICT(host) DO UPDATE SET provider=excluded.provider,token=excluded.token`, body.Host, body.Provider, body.Token)
	}
	if err != nil {
		http.Error(w, "Could not save credentials", http.StatusInternalServerError)
		return
	}
	m.loaded = false
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) refresh(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	if m.d.Rediscover != nil {
		m.refreshed = m.d.Rediscover()
	}
	m.loaded = false
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
