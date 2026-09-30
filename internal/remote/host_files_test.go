package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// bigFilesHost returns responses past gRPC's 4 MiB default receive limit
// that the producer still allows.
type bigFilesHost struct{ localStubHost }

func (bigFilesHost) ListRepoFiles(context.Context, string) (*git.FileList, error) {
	files := make([]string, git.MaxListedFiles)
	for i := range files {
		files[i] = fmt.Sprintf("some/fairly/long/directory/path/segment/file-%06d.ts", i)
	}
	return &git.FileList{Root: "/r", Files: files}, nil
}

func (bigFilesHost) ReadRepoFile(_ context.Context, _, path string) (*git.FileContent, error) {
	// '<' marshals as \u003c: 1 MiB of it is 6 MiB of JSON.
	return &git.FileContent{Path: path, Content: strings.Repeat("<", int(git.MaxFileBytes)), Size: git.MaxFileBytes}, nil
}

func TestRemoteHostRepoFilesLargeResponses(t *testing.T) {
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), bigFilesHost{}, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	ctx := context.Background()
	if list, err := host.ListRepoFiles(ctx, "/r"); err != nil || len(list.Files) != git.MaxListedFiles {
		t.Fatalf("large listing: %v", err)
	}
	if file, err := host.ReadRepoFile(ctx, "/r", "a"); err != nil || len(file.Content) != int(git.MaxFileBytes) {
		t.Fatalf("escape-heavy file: %v", err)
	}
}
