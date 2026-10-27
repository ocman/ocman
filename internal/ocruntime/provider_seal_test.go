package ocruntime

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/providersupervisor"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

func TestProviderSealIdleFencesResume(t *testing.T) {
	dir := testutil.ShortTempDir(t, "seal-")
	sup, err := providersupervisor.New(filepath.Join(dir, "spool"), providersupervisor.Backend{})
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: sup}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	r := NewBackendRuntime("codex", "/binary", dir)
	inst := &Instance{Endpoint: "unix://" + socket}
	if idle, err := r.DrainIdle(t.Context(), inst, "owner + token"); err != nil || !idle {
		t.Fatalf("drain = %v, %v", idle, err)
	}
	if idle, err := r.SealIdle(t.Context(), inst, "wrong"); err != nil || idle {
		t.Fatalf("wrong seal = %v, %v", idle, err)
	}
	if idle, err := r.SealIdle(t.Context(), inst, "owner + token"); err != nil || !idle {
		t.Fatalf("seal = %v, %v", idle, err)
	}
	if err := r.Resume(t.Context(), inst, "owner + token"); err != nil {
		t.Fatal(err)
	}
	if err := sup.BeginTurn("s", nil); err == nil {
		t.Fatal("reconnect undid a committed replacement drain")
	}
}
