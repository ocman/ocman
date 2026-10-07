package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestMCPCreateSessionProductionProbeFailure(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf 'fatal: object read failed\\n' >&2\nexit 128\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	srv, reg := newSessionsTestServer(t)
	srv.hostRouter = hostsvc.NewRouter(local.New(local.Deps{Caps: func() hostsvc.HostCaps {
		return hostsvc.HostCaps{OpencodeLaunch: true}
	}}))
	created := false
	reg.Register(&fakePlatform{id: "opencode", createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		created = true
		return &platforms.CreateSessionResponse{ID: "unexpected"}, nil
	}})
	_, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: t.TempDir()})
	if err == nil || created {
		t.Fatalf("probe failure launched a session: created=%v, err=%v", created, err)
	}
}
