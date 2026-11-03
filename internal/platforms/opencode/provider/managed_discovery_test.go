package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestManagedDiscoveryDoesNotRunExternalScanner(t *testing.T) {
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

func TestDiscoveryStopsOnCanceledContext(t *testing.T) {
	withMachineServer(t, "1001")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if servers := td.discoverOpenCodeServersUncachedContext(ctx); servers != nil {
		t.Fatal(servers)
	}
}
