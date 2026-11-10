package opencode

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// ConnectionConfig is resolved on the owning machine. Native.DBPath is also
// OpenCode v2's credential store; DataDir isolates legacy auth.json import.
// Callers must not reuse any writable directory between named accounts.
type ConnectionConfig struct {
	Native    ocruntime.NativeConnectionConfig
	Favorites FavoritesReader
	Pricing   CostCalculator
	Auth      ocapi.Auth
}

type connectionOwner struct {
	metadata platformconnections.Connection
	once     sync.Once
	closeErr error
}

// NewConnection opens an explicit account database read-only and owns private
// HTTP clients, catalog/dialect caches, discovery and observation state. It
// neither starts a server nor discovers the shared user background service.
// New retains the legacy process-default behavior.
func NewConnection(connection platformconnections.Connection, cfg ConnectionConfig) (*Adapter, error) {
	if connection.ID == "" || connection.Kind != string(PlatformID) || connection.Removed || (connection.Configuration != "isolated" && connection.Configuration != "existing") {
		return nil, fmt.Errorf("invalid OpenCode connection metadata")
	}
	if err := cfg.Native.Validate(); err != nil {
		return nil, err
	}
	if filepath.Clean(connection.Directory) != filepath.Clean(cfg.Native.ConfigDir) {
		return nil, fmt.Errorf("OpenCode connection configuration directory mismatch")
	}
	if !cfg.Auth.Enabled() {
		return nil, fmt.Errorf("OpenCode connection requires private server authentication")
	}
	database, err := db.Open(cfg.Native.DBPath)
	if err != nil {
		return nil, err
	}
	a := newAdapter(database, cfg.Favorites, cfg.Pricing, cfg.Auth, newHTTPState(cfg.Auth))
	a.connection = &connectionOwner{metadata: connection}
	return a, nil
}

// CloseConnection releases only resources created by NewConnection. The native
// runtime has a separate owner lifecycle and is not stopped here.
func (a *Adapter) CloseConnection() error {
	if a == nil || a.connection == nil {
		return nil
	}
	a.connection.once.Do(func() {
		a.connection.closeErr = a.db.Close()
	})
	return a.connection.closeErr
}

// HTTPClient returns this account's unbounded helper/event client. Callers own
// request deadlines and must not mutate the returned client.
func (a *Adapter) HTTPClient() *http.Client {
	if a.httpState != nil {
		return a.events
	}
	return a.llmClient()
}

func (a *Adapter) transport() http.RoundTripper {
	if a.connection != nil {
		return a.stream
	}
	return a.auth.Transport(http.DefaultTransport)
}
