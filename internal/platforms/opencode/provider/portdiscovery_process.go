package opencode

import (
	"context"
	"time"
)

type openCodeServer struct {
	directory string
	port      string
}

func copyOpenCodeServers(servers []openCodeServer) []openCodeServer {
	return append([]openCodeServer(nil), servers...)
}

const stalePortGrace = time.Minute

func (d *Discovery) discoverOpenCodePorts() map[string]string {
	if ports := d.DiscoverOpenCodePortsContext(context.Background()); ports != nil {
		return ports
	}
	d.portCache.mu.Lock()
	defer d.portCache.mu.Unlock()
	if time.Since(d.portCache.lastSuccessfulAt) > stalePortGrace {
		return map[string]string{}
	}
	return copyMap(d.portCache.lastSuccessful)
}

func (d *Discovery) discoverOpenCodeServersUncached() []openCodeServer {
	return d.discoverOpenCodeServersUncachedContext(context.Background())
}

func (d *Discovery) discoverOpenCodeServersUncachedContext(ctx context.Context) []openCodeServer {
	if ctx.Err() != nil {
		return nil
	}
	return append([]openCodeServer{}, d.machineServers()...)
}

func (d *Discovery) discoverOpenCodeServers() []openCodeServer {
	if cached, ok := d.readCachedServers(); ok {
		return cached
	}
	const flightKey = "discoverOpenCodeServers"
	v, _, _ := d.serverFlight.Do(flightKey, func() (interface{}, error) {
		if cached, ok := d.readCachedServers(); ok {
			return cached, nil
		}
		result := d.discoverServersImpl()
		d.serverCache.mu.Lock()
		d.serverCache.servers = copyOpenCodeServers(result)
		d.serverCache.updated = time.Now()
		d.serverCache.mu.Unlock()
		return copyOpenCodeServers(result), nil
	})
	if servers, ok := v.([]openCodeServer); ok {
		return servers
	}
	return nil
}

func (d *Discovery) readCachedServers() ([]openCodeServer, bool) {
	d.serverCache.mu.Lock()
	defer d.serverCache.mu.Unlock()
	if time.Since(d.serverCache.updated) < portCacheTTL && d.serverCache.servers != nil {
		return copyOpenCodeServers(d.serverCache.servers), true
	}
	return nil, false
}

func (d *Discovery) discoverOpenCodePortsUncachedContext(ctx context.Context) map[string]string {
	servers := d.discoverOpenCodeServersUncachedContext(ctx)
	if servers == nil {
		return nil
	}
	result := make(map[string]string)
	for _, server := range servers {
		result[server.directory] = server.port
	}
	return result
}
