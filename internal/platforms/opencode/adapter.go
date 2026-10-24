// Package opencode implements the platforms.Platform interface for OpenCode.
//
// It wraps the read-only SQLite reader in internal/db and exposes the
// HTTP-proxy behavior (port discovery, composer, etc.) that the server
// package uses today. Handlers are being migrated to go through this
// adapter as part of the multi-platform work; Phase 1 establishes the
// interface and identity, later phases move behavior here.
package opencode

import (
	"context"
	"sync"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// PlatformID is the stable identifier used in URLs and state.db rows.
const PlatformID platforms.ID = platforms.OpenCodeID

// FavoritesReader is the subset of the ocman state DB the adapter
// needs to read model favorites. Kept as an interface so tests can
// pass a stub and so the adapter doesn't force a writable *state.DB
// on callers that only want the read side.
type FavoritesReader interface {
	ModelFavorites(context.Context, string) ([]state.ModelFavorite, error)
}

// CostCalculator computes API-equivalent cost from token counts and a
// model id. Mirrors the same-named interface in internal/db/stats.go
// so the adapter can be tested without pulling in the real pricing
// table. A nil CostCalculator is permitted; the adapter falls back
// to whatever cost values the upstream messages carry, which is
// often zero for subscription-plan sessions.
type CostCalculator interface {
	CalcCost(modelID string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) float64
}

// Adapter implements platforms.Platform for OpenCode.
type Adapter struct {
	db        *db.DB
	favorites FavoritesReader
	pricing   CostCalculator
	auth      ocapi.Auth
	prompts   *livePromptRegistry
	// turns is the live view of which sessions are running a turn, fed
	// from each instance's /session/status snapshot and session.status
	// events. See live_status.go.
	turns *liveStatusRegistry
	// rt is this adapter's own port discovery (see portdiscovery.go).
	rt       *Discovery
	sendLock [64]sync.Mutex // serializes V2 selection check + prompt; collisions only reduce concurrency
}

// New returns a new OpenCode adapter backed by the given read-only DB.
// A nil DB is permitted so Available() can report absence cleanly.
// A nil favorites reader is also permitted; SessionModels will then
// skip the favorites merge step. The pricing table estimates cost for
// assistant messages whose upstream `cost` field is zero (typical of
// subscription-plan sessions); auth is the host-local OpenCode API credential.
func New(database *db.DB, favorites FavoritesReader, pricing CostCalculator, auth ocapi.Auth) *Adapter {
	configureHTTPAuth(auth)
	prompts := newLivePromptRegistry()
	prompts.haltStore, _ = favorites.(*state.DB)
	rt := newAdapterDiscovery()
	turns := newLiveStatusRegistry()
	turns.disc = rt
	return &Adapter{db: database, favorites: favorites, pricing: pricing, auth: auth, prompts: prompts, turns: turns, rt: rt}
}

// newAdapterDiscovery builds each adapter's discovery. A seam so this
// package's tests can share one discovery between adapters and helpers.
var newAdapterDiscovery = NewDiscovery

// Discovery returns the adapter's port discovery. The local Host publishes
// its managed inventory and v2 machine server to it.
func (a *Adapter) Discovery() *Discovery { return a.rt }

// ID returns the OpenCode platform identifier.
func (a *Adapter) ID() platforms.ID { return PlatformID }

// DisplayName returns the user-facing name.
func (a *Adapter) DisplayName() string { return platforms.DisplayName(platforms.OpenCodeID) }

// Available reports whether the underlying OpenCode database is usable.
// Returns false when the adapter has no DB (OpenCode not installed).
func (a *Adapter) Available(context.Context) bool {
	return a.db != nil
}

// Capabilities returns OpenCode's capability set. OpenCode supports every
// feature ocman exposes today.
func (a *Adapter) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{
		Composer:           true,
		RespondPermission:  true,
		RespondQuestion:    true,
		Abort:              true,
		BackgroundTaskStop: ocv2.InstalledV2(),
		Compact:            true,
		Fork:               true,
		ForkAtMessage:      true,
		Move:               true,
		Events:             true,
		AgentCatalog:       true,
		ModelCatalog:       true,
		SlashCommands:      true,
		ShellExec:          true,
		FileChanges:        true,
		SessionInfo:        true,
		AutoApprove:        true,
		PermissionRules:    true,
		Revert:             true,
		RuntimeRestart:     true,
		// ocman launches and manages the OpenCode instance for each
		// project itself (EnsureProjectOpencode), so no live connection
		// means the managed instance isn't running yet.
		LiveConnectionHint: "Launch a session to start OpenCode for this project.",
	}
}

// directoryHasLivePort reports whether a running OpenCode instance
// serves the given session directory. It first tries an exact match,
// then folds a worktree directory back to its project root — worktree
// sessions run on the project's shared instance (rooted at the main
// checkout), so their own directory is never a key in the port map.
// Without the fold, worktree sessions report LiveConnection=false,
// which disables the composer and the question-prompt UI for them.
func (d *Discovery) directoryHasLivePort(ports map[string]string, directory string) bool {
	return d.portForDirectory(ports, directory) != ""
}

// LiveStatus returns nil: OpenCode overlays live connection and prompt
// state directly in Sessions.
func (a *Adapter) LiveStatus(string) *platforms.LiveState { return nil }
