package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func TestRemoteHostRepoFilesRoundTrip(t *testing.T) {
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), localStubHost{}, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	ctx := context.Background()

	list, err := host.ListRepoFiles(ctx, "/remote/repo")
	if err != nil || list.Root != "/remote/repo" || len(list.Files) != 1 || list.Files[0] != "a.go" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	file, err := host.ReadRepoFile(ctx, "/remote/repo", "a.go")
	if err != nil || file.Content != "package a" || file.Size != 9 {
		t.Fatalf("file = %+v, %v", file, err)
	}
	if _, err := host.ReadRepoFile(ctx, "/remote/repo", ".env"); !errors.Is(err, git.ErrFileNotFound) {
		t.Fatalf("missing err = %v, want ErrFileNotFound", err)
	}
	if _, err := newRemoteHost(&RemoteConn{}).ListRepoFiles(ctx, "/x"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("offline err = %v", err)
	}
}
