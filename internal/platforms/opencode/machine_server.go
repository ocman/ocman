package opencode

import (
	"os"
	"sync/atomic"
)

// machineServer is the port of the OpenCode v2 server that serves every
// directory on this machine ("" on v1 machines). v2 runs one server for
// all projects, so lsof + working-directory discovery does not apply:
// the host that launched it publishes the port here.
var machineServer atomic.Value // string

// SetMachineServer publishes (or, with "", clears) the v2 machine server.
func SetMachineServer(port string) {
	if prev, _ := machineServer.Load().(string); prev == port {
		return
	}
	machineServer.Store(port)
	resetPortCache()
}

// MachineServerPort returns the v2 machine server's port, or "".
func MachineServerPort() string {
	p, _ := machineServer.Load().(string)
	return p
}

// machineServers is discovery on a v2 machine: just the published
// server. Every directory resolves to it through the lookup fallback;
// it is listed once (under the home directory) so per-port consumers
// such as the auto-approve watcher subscribe to its event stream.
func machineServers() []openCodeServer {
	port := MachineServerPort()
	if port == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	return []openCodeServer{{directory: normalizePortDirectory(home), port: port}}
}
