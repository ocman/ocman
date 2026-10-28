package app

import (
	"context"
	"flag"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/provider"
	"github.com/ocman/ocman/internal/platforms/claude/transport"
	codexprovider "github.com/ocman/ocman/internal/platforms/codex/provider"
	codextransport "github.com/ocman/ocman/internal/platforms/codex/transport"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	opencodeplatform "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/pricing"
	"github.com/ocman/ocman/internal/state"
)

// knownPlatforms lists the valid values for the -platforms flag.
var knownPlatforms = map[string]func(context.Context, *db.DB, *state.DB, ocapi.Auth) platforms.Integration{
	string(opencodeplatform.PlatformID): func(_ context.Context, database *db.DB, stateDB *state.DB, auth ocapi.Auth) platforms.Integration {
		return opencodeplatform.New(database, stateDB, pricing.Load(), auth)
	},
	claudeprovider.PlatformID: func(ctx context.Context, database *db.DB, stateDB *state.DB, _ ocapi.Auth) platforms.Integration {
		addr := ""
		if f := flag.Lookup("mcp-addr"); f != nil {
			addr = f.Value.String()
		}
		return claudecode.New(ctx, database, stateDB, skillCatalog, addr)
	},
	codexprovider.PlatformID: func(ctx context.Context, _ *db.DB, stateDB *state.DB, _ ocapi.Auth) platforms.Integration {
		return codextransport.New(ctx, codextransport.Config{State: stateDB, DataDir: state.DefaultDataDir(), Version: version})
	},
}
