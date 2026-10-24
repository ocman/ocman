package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
)

func TestManagedDiscoveryDoesNotRunExternalScanner(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	script := fmt.Sprintf("#!/bin/sh\n: > %q\nprintf 'opencode %d user 1u IPv4 0t0 TCP 127.0.0.1:1001 (LISTEN)\\n'\n", marker, os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	servers := td.discoverOpenCodeServersUncachedContext(t.Context())
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("managed lookup invoked external-instance scanner")
	}
	if len(servers) != 0 {
		t.Fatalf("external servers were adopted: %+v", servers)
	}
}

func TestManagedDiscoveryReadsOwnerInventory(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	previous := td.managedPortSource.Load()
	t.Cleanup(func() { td.managedPortSource.Store(previous); td.resetPortCache() })
	td.SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{"/repo": "1001"} })
	if ports := td.DiscoverOpenCodePortsContext(t.Context()); ports["/repo"] != "1001" {
		t.Fatal(ports)
	}
	td.SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{} })
	if ports := td.DiscoverOpenCodePortsContext(t.Context()); len(ports) != 0 {
		t.Fatalf("stopped managed instance is still listed: %v", ports)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if servers := td.discoverOpenCodeServersUncachedContext(ctx); servers != nil {
		t.Fatal(servers)
	}
}
