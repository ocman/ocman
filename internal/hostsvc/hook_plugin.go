package hostsvc

import "context"

// HookPluginManager configures the owning machine's terminal-session hooks.
// The hook settings currently target the local machine only.
type HookPluginManager interface {
	HookPlugin() (available, installed bool)
	InstallHookPlugin(context.Context) error
	UninstallHookPlugin(context.Context) error
}
