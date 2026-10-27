package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func (a *Adapter) SetMCPAddr(addr string) {
	if sink, ok := a.native.(interface{ SetMCPAddr(string) }); ok {
		sink.SetMCPAddr(addr)
	}
}

func (a *Adapter) HostServices() hostsvc.PlatformServices {
	runtime, _ := a.native.(hostsvc.RuntimeEnsurer)
	namer, _ := a.native.(hostsvc.WorktreeNamer)
	hooks, _ := a.native.(hostsvc.PlatformHooks)
	idle, _ := a.native.(hostsvc.IdleProjectRuntime)
	return hostsvc.PlatformServices{Runtime: runtime, Namer: namer, Hooks: hooks, Idle: idle}
}

func (a *Adapter) RuntimeAvailable() bool {
	if runtime, ok := a.native.(hostsvc.RuntimeAvailability); ok {
		return runtime.RuntimeAvailable()
	}
	return true
}

// Account forwards the integration's subscription account (Settings →
// Provider accounts). Integrations without one are ErrUnsupported, which the
// list leaves out.
func (a *Adapter) Account(ctx context.Context) (*providerprotocol.Account, error) {
	if reader, ok := a.native.(interface {
		Account(context.Context) (*providerprotocol.Account, error)
	}); ok {
		return reader.Account(ctx)
	}
	return nil, platforms.ErrUnsupported
}
