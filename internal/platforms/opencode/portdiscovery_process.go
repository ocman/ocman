package opencode

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

type openCodeServer struct {
	directory string
	port      string
}

var managedPortSource atomic.Pointer[func(context.Context) map[string]string]

// SetManagedPortSource wires the owner's verified managed runtime inventory.
func SetManagedPortSource(source func(context.Context) map[string]string) {
	managedPortSource.Store(&source)
	resetPortCache()
}

func copyOpenCodeServers(servers []openCodeServer) []openCodeServer {
	return append([]openCodeServer(nil), servers...)
}

const stalePortGrace = time.Minute

func discoverOpenCodePorts() map[string]string {
	if ports := DiscoverOpenCodePortsContext(context.Background()); ports != nil {
		return ports
	}
	portCache.mu.Lock()
	defer portCache.mu.Unlock()
	if time.Since(portCache.lastSuccessfulAt) > stalePortGrace {
		return map[string]string{}
	}
	return copyMap(portCache.lastSuccessful)
}

func discoverOpenCodeServersUncached() []openCodeServer {
	return discoverOpenCodeServersUncachedContext(context.Background())
}

func discoverOpenCodeServersUncachedContext(ctx context.Context) []openCodeServer {
	if ctx.Err() != nil {
		return nil
	}
	if ocv2.InstalledV2() {
		return append([]openCodeServer{}, machineServers()...)
	}
	servers := []openCodeServer{}
	if source := managedPortSource.Load(); source != nil {
		ports := (*source)(ctx)
		if ports == nil {
			return nil
		}
		for directory, port := range ports {
			servers = append(servers, openCodeServer{directory: normalizePortDirectory(directory), port: port})
		}
	}
	return servers
}

func discoverOpenCodeServers() []openCodeServer {
	if cached, ok := readCachedServers(); ok {
		return cached
	}
	const flightKey = "discoverOpenCodeServers"
	v, _, _ := serverFlight.Do(flightKey, func() (interface{}, error) {
		if cached, ok := readCachedServers(); ok {
			return cached, nil
		}
		result := (*discoverServersImpl.Load())()
		serverCache.mu.Lock()
		serverCache.servers = copyOpenCodeServers(result)
		serverCache.updated = time.Now()
		serverCache.mu.Unlock()
		return copyOpenCodeServers(result), nil
	})
	if servers, ok := v.([]openCodeServer); ok {
		return servers
	}
	return nil
}

func readCachedServers() ([]openCodeServer, bool) {
	serverCache.mu.Lock()
	defer serverCache.mu.Unlock()
	if time.Since(serverCache.updated) < portCacheTTL && serverCache.servers != nil {
		return copyOpenCodeServers(serverCache.servers), true
	}
	return nil, false
}

func discoverOpenCodePortsUncachedContext(ctx context.Context) map[string]string {
	servers := discoverOpenCodeServersUncachedContext(ctx)
	if servers == nil {
		return nil
	}
	result := make(map[string]string)
	for _, server := range servers {
		result[server.directory] = server.port
	}
	return result
}
