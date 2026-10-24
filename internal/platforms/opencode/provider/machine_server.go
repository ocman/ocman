package opencode

import "os"

// v2 runs one server for all projects, so per-project runtime lookup does
// not apply: the host that launched it publishes its port to the
// Discovery (see Discovery.machineServer).

// SetMachineServer publishes (or, with "", clears) the v2 machine server.
func (d *Discovery) SetMachineServer(port string) {
	if prev, _ := d.machineServer.Load().(string); prev == port {
		return
	}
	d.machineServer.Store(port)
	d.resetPortCache()
}

// MachineServerPort returns the v2 machine server's port, or "".
func (d *Discovery) MachineServerPort() string {
	if d == nil {
		return ""
	}
	p, _ := d.machineServer.Load().(string)
	return p
}

// machineServers is discovery on a v2 machine: just the published
// server. Every directory resolves to it through the lookup fallback;
// it is listed once (under the home directory) so per-port consumers
// such as the auto-approve watcher subscribe to its event stream.
func (d *Discovery) machineServers() []openCodeServer {
	port := d.MachineServerPort()
	if port == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	return []openCodeServer{{directory: normalizePortDirectory(home), port: port}}
}
