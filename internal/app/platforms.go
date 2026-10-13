package app

import (
	"context"
	"flag"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claudecode"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/state"
)

// knownPlatforms lists the valid values for the -platforms flag.
var knownPlatforms = map[string]func(context.Context, *db.DB, *state.DB, ocapi.Auth) platforms.Platform{
	string(opencodeplatform.PlatformID): func(_ context.Context, database *db.DB, stateDB *state.DB, auth ocapi.Auth) platforms.Platform {
		return opencodeplatform.NewWithPricingAndAuth(database, stateDB, pricing.Load(), auth)
	},
	claudeprovider.PlatformID: func(ctx context.Context, database *db.DB, stateDB *state.DB, _ ocapi.Auth) platforms.Platform {
		addr := ""
		if f := flag.Lookup("mcp-addr"); f != nil {
			addr = f.Value.String()
		}
		return claudecode.New(ctx, database, stateDB, skillCatalog, addr)
	},
}
