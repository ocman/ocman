package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	claudeprovider "github.com/ocman/ocman/internal/platforms/claude/provider"
	"github.com/ocman/ocman/internal/state"
)

// NewConnection builds a named Claude account using explicit owner-local paths.
// Native history IDs and platform kind remain unchanged. Legacy New still uses
// the machine's inherited configuration and managed_provider persistence.
func NewConnection(ctx context.Context, connection platformconnections.Connection, dataDir string, stateDB *state.DB, skills map[string][]byte, mcpAddr string, pricing CostCalculator) (platforms.Integration, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return newConnectionAt(ctx, connection, dataDir, exe, stateDB, skills, mcpAddr, pricing)
}

func newConnectionAt(ctx context.Context, c platformconnections.Connection, ownerDir, exe string, db *state.DB, skills map[string][]byte, mcpAddr string, pricing CostCalculator) (*claudePlatform, error) {
	id, err := uuid.Parse(strings.TrimPrefix(c.ID, "cnx_"))
	if err != nil || c.ID != "cnx_"+id.String() || c.Kind != claudeprovider.PlatformID || !filepath.IsAbs(ownerDir) {
		return nil, fmt.Errorf("named Claude requires a stable connection ID, Claude kind and absolute owner data path")
	}
	config, err := local.CanonicalProviderRoot(c.Directory)
	if err != nil || config != filepath.Clean(c.Directory) {
		return nil, fmt.Errorf("named Claude requires its owner-resolved canonical configuration directory")
	}
	store, err := platformconnections.NewRuntimeStore(ctx, db, c)
	if err != nil {
		return nil, err
	}
	dataDir, err := connectionDataDir(ownerDir, c.ID)
	if err != nil {
		return nil, err
	}
	opts := claudeprovider.DefaultOptions(dataDir)
	owner := claudeprovider.DefaultOptions(ownerDir)
	opts.ConfigDir, opts.ConfigEnv, opts.ConnectionAccount = config, &config, true
	opts.SlotDir, opts.MCPTokenPath = owner.SlotDir, owner.MCPTokenPath
	p := buildClaudePlatform(ctx, store, dataDir, ownerDir, exe, skills, mcpAddr, opts)
	if p == nil {
		return nil, fmt.Errorf("named Claude integration unavailable")
	}
	p.pricing = pricing
	return p, nil
}

func connectionIdentity(opts claudeprovider.Options, dataDir string) string {
	if opts.ConnectionAccount {
		return "\x00connection-account/v2-user-settings\x00" + dataDir
	}
	return ""
}

func connectionDataDir(owner, id string) (string, error) {
	base, err := filepath.EvalSymlinks(owner)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	parts := []string{"platform-connections", id, "runtime"}
	for _, part := range parts {
		if err := root.Mkdir(part, 0o700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := root.Lstat(part)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			return "", fmt.Errorf("named Claude runtime directory must be private and not a symlink")
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return "", err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			return "", fmt.Errorf("named Claude runtime directory changed during preparation")
		}
		_ = root.Close()
		root = next
	}
	return filepath.Join(base, "platform-connections", id, "runtime"), nil
}

func (p *claudePlatform) ensureAccountPlugin(ctx context.Context) (string, error) {
	if p.connectionAccount {
		return claudeprovider.EnsureAccountPlugin(ctx, p.cli, p.configDir, p.marketplace)
	}
	return claudeprovider.EnsurePlugin(ctx, p.cli, p.configDir, p.marketplace)
}
