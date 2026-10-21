package owned

import "github.com/NoUseFreak/ocman/internal/hostsvc"

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
