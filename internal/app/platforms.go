package app

import (
	"context"
	"flag"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/transport"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/state"
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
}
