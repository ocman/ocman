package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	claudecode "github.com/ocman/ocman/internal/platforms/claude/transport"
	codextransport "github.com/ocman/ocman/internal/platforms/codex/transport"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	ocv2 "github.com/ocman/ocman/internal/platforms/opencode/apiv2"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/pricing"
	"github.com/ocman/ocman/internal/state"
)

type connectionFactory struct {
	state                        *state.DB
	dataDir, mcpAddr, openCodeDB string
	legacyAuth                   ocapi.Auth
	blockedKinds                 map[string]bool
	runtimeBuilder               func(ocruntime.NativeConnectionConfig, ocapi.Auth) (ocruntime.Runtime, error)
	adapterBuilder               func(platformconnections.Connection, opencode.ConnectionConfig) (*opencode.Adapter, error)
}

func (f connectionFactory) build(ctx context.Context, c platformconnections.Connection) (platforms.Integration, error) {
	if c.LegacyKind != "" && c.Kind != "opencode" {
		p := knownPlatforms[c.Kind](ctx, nil, f.state, f.legacyAuth)
		if p == nil {
			return nil, platformconnections.ErrUnavailable
		}
		return p, nil
	}
	p, err := f.buildNamed(ctx, c)
	if err != nil {
		return nil, err
	}
	if _, ok := p.(platforms.ConnectionInspector); !ok {
		if closer, ok := p.(interface{ Close() }); ok {
			closer.Close()
		}
		return nil, fmt.Errorf("%w: named integration has no native account inspector", platforms.ErrUnsupported)
	}
	return p, nil
}

func (f connectionFactory) buildNamed(ctx context.Context, c platformconnections.Connection) (platforms.Integration, error) {
	switch c.Kind {
	case "claudecode":
		return claudecode.NewConnection(ctx, c, f.dataDir, f.state, skillCatalog, f.mcpAddr, pricing.Load())
	case "codex":
		p := codextransport.New(ctx, codextransport.Config{State: f.state, DataDir: f.dataDir, Version: version, Connection: &codextransport.ConnectionConfig{ID: c.ID, Home: c.Directory}})
		if p == nil {
			return nil, platformconnections.ErrUnavailable
		}
		return p, nil
	case "opencode":
		return f.openCode(ctx, c)
	default:
		return nil, platformconnections.ErrInvalid
	}
}

func (f connectionFactory) openCodeConfig(c platformconnections.Connection) ocruntime.NativeConnectionConfig {
	base := filepath.Join(f.dataDir, "platform-connections", c.ID, "native")
	cfg := ocruntime.NativeConnectionConfig{DBPath: filepath.Join(base, "data", "opencode", "opencode.db"), ConfigDir: c.Directory, DataDir: filepath.Join(base, "data"), StateDir: filepath.Join(base, "state"), CacheDir: filepath.Join(base, "cache"), RuntimeDir: filepath.Join(base, "runtime"), Executable: "opencode"}
	// Retain the original physical history and credential source for the migrated
	// existing account. New isolated accounts always use connection-owned paths.
	if c.LegacyKind == "opencode" && f.openCodeDB != "" {
		cfg.DBPath = f.openCodeDB
		if absolute, err := filepath.Abs(cfg.DBPath); err == nil {
			cfg.DBPath = absolute
		}
		home, _ := os.UserHomeDir()
		cfg.DataDir = os.Getenv("XDG_DATA_HOME")
		if cfg.DataDir == "" {
			cfg.DataDir = filepath.Join(home, ".local", "share")
		}
		cfg.StateDir = os.Getenv("XDG_STATE_HOME")
		if cfg.StateDir == "" {
			cfg.StateDir = filepath.Join(home, ".local", "state")
		}
		cfg.CacheDir = os.Getenv("XDG_CACHE_HOME")
		if cfg.CacheDir == "" {
			cfg.CacheDir = filepath.Join(home, ".cache")
		}
	}
	return cfg
}

func (f connectionFactory) openCode(ctx context.Context, c platformconnections.Connection) (platforms.Integration, error) {
	cfg := f.openCodeConfig(c)
	if err := os.MkdirAll(cfg.RuntimeDir, 0o700); err != nil {
		return nil, err
	}
	auth, err := f.connectionAuth(ctx, c)
	if err != nil {
		return nil, err
	}
	var runtime ocruntime.Runtime
	if f.runtimeBuilder != nil {
		runtime, err = f.runtimeBuilder(cfg, auth)
	} else {
		runtime, err = ocruntime.NewNativeConnectionRuntime(cfg, auth)
	}
	if err != nil {
		return nil, err
	}
	store, err := platformconnections.NewRuntimeStore(ctx, f.state, c)
	if err != nil {
		return nil, err
	}
	var discovery *opencode.Discovery
	deps := local.Deps{Runtime: runtime, ManagedStore: connectionManagedStore{store}, OpenCodeAuth: func() ocapi.Auth { return auth }, SetMachineServer: func(port string) {
		if discovery != nil {
			discovery.SetMachineServer(port)
		}
	}}
	var host *local.Host
	if c.LegacyKind == "opencode" && sharesServiceDB(cfg.DBPath) {
		deps.OpenCodeService = ocv2.RunningService
		deps.OpenCodeRoot = cfg.RuntimeDir
		host = local.New(deps)
	} else {
		host, err = local.NewOpenCodeConnection(deps, cfg.RuntimeDir)
	}
	if err != nil {
		return nil, err
	}
	// Bootstrapping the private native database runs only its server, never an
	// agent or model. The adapter subsequently opens that database read-only.
	result, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: cfg.RuntimeDir})
	if err != nil {
		host.Finalize()
		return nil, err
	}
	var adapter *opencode.Adapter
	var legacyDB *db.DB
	if c.LegacyKind == "opencode" {
		legacyDB, err = db.Open(cfg.DBPath)
		if err == nil {
			adapter = opencode.New(legacyDB, f.state, pricing.Load(), auth)
		}
	} else {
		config := opencode.ConnectionConfig{Native: cfg, Favorites: f.state, Pricing: pricing.Load(), Auth: auth}
		if f.adapterBuilder != nil {
			adapter, err = f.adapterBuilder(c, config)
		} else {
			adapter, err = opencode.NewConnection(c, config)
		}
	}
	if err != nil {
		host.Finalize()
		return nil, err
	}
	discovery, _ = adapter.InstancePorts().(*opencode.Discovery)
	if discovery != nil {
		discovery.SetMachineServer(fmt.Sprint(result.Port()))
	}
	return &openCodeConnection{Adapter: adapter, host: host, legacyDB: legacyDB}, nil
}

func (f connectionFactory) connectionAuth(ctx context.Context, c platformconnections.Connection) (ocapi.Auth, error) {
	if c.LegacyKind == "opencode" {
		if f.legacyAuth.Enabled() {
			return f.legacyAuth, nil
		}
		password, err := startupOpenCodePassword(ctx, f.state, "", true, sharesServiceDB(f.openCodeDB))
		return ocapi.New(password), err
	}
	key := "platform-connection." + c.ID + ".server-password"
	password, ok, err := f.state.GetSecretSetting(ctx, key)
	if err != nil {
		return ocapi.Auth{}, err
	}
	if ok {
		return ocapi.New(password), nil
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return ocapi.Auth{}, err
	}
	password = hex.EncodeToString(secret[:])
	if err := f.state.SetSecretSetting(ctx, key, password); err != nil {
		return ocapi.Auth{}, err
	}
	return ocapi.New(password), nil
}

type openCodeConnection struct {
	*opencode.Adapter
	host     *local.Host
	legacyDB *db.DB
}

func (p *openCodeConnection) Close() {
	p.host.Finalize()
	_ = p.CloseConnection()
	if p.legacyDB != nil {
		_ = p.legacyDB.Close()
	}
}
func (p *openCodeConnection) HostServices() hostsvc.PlatformServices {
	return hostsvc.PlatformServices{Runtime: p, Namer: p}
}

func (p *openCodeConnection) EnsureRuntime(ctx context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	return p.host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}

func (p *openCodeConnection) WorktreeName(ctx context.Context, dir, prompt string) (string, error) {
	runtime, err := p.EnsureRuntime(ctx, dir)
	if err != nil {
		return "", err
	}
	return p.Adapter.WorktreeName(ctx, runtime.Port(), dir, prompt)
}
