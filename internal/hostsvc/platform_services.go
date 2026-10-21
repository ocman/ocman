package hostsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// PlatformServices retains optional host capabilities across identity adapters.
// None of these interfaces resolves or executes an application session ID.
type PlatformServices struct {
	Runtime RuntimeEnsurer
	Namer   WorktreeNamer
	Hooks   PlatformHooks
	Idle    IdleProjectRuntime
}

type PlatformHooks interface {
	Hook(context.Context, providerprotocol.HookEvent) error
	HookPluginManager
}

type PlatformServiceProvider interface {
	HostServices() PlatformServices
}
