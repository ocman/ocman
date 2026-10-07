package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func TestRemoteListWorktreesPreservesRepositoryErrors(t *testing.T) {
	for _, notRepo := range []bool{true, false} {
		name := "probe failure"
		if notRepo {
			name = "non repository"
		}
		t.Run(name, func(t *testing.T) {
			if !notRepo {
				bin := t.TempDir()
				if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf 'fatal: object read failed\\n' >&2\nexit 128\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), local.New(local.Deps{}), "rid", "v"))
			host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
			_, err := host.ListWorktrees(t.Context(), t.TempDir())
			if err == nil || errors.Is(err, git.ErrNotARepo) != notRepo {
				t.Fatalf("round-trip error = %v, want notRepo=%v", err, notRepo)
			}
		})
	}
}
