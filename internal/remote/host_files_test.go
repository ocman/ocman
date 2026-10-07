package remote

import (
	"context"
	"encoding/base64"
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

	list, err := host.ListRepoFiles(ctx, "/remote/repo", false)
	if err != nil || list.Root != "/remote/repo" || len(list.Files) != 1 || list.Files[0] != "a.go" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	file, err := host.ReadRepoFile(ctx, "/remote/repo", "a.go", false)
	if err != nil || file.Content != "package a" || file.Size != 9 {
		t.Fatalf("file = %+v, %v", file, err)
	}
	if _, err := host.ReadRepoFile(ctx, "/remote/repo", ".env", true); err != nil {
		t.Fatalf("ignored read err = %v", err)
	}
	if _, err := host.ReadRepoFile(ctx, "/remote/repo", ".env", false); !errors.Is(err, git.ErrFileNotFound) {
		t.Fatalf("missing err = %v, want ErrFileNotFound", err)
	}
	if _, err := newRemoteHost(&RemoteConn{}).ListRepoFiles(ctx, "/x", false); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("offline err = %v", err)
	}
}

// bigFilesHost returns responses past gRPC's 4 MiB default receive limit
// that the producer still allows.
type bigFilesHost struct{ localStubHost }

func (bigFilesHost) ListRepoFiles(context.Context, string, bool) (*git.FileList, error) {
	files := make([]string, git.MaxListedFiles)
	for i := range files {
		files[i] = fmt.Sprintf("some/fairly/long/directory/path/segment/file-%06d.ts", i)
	}
	return &git.FileList{Root: "/r", Files: files}, nil
}

func (bigFilesHost) ReadRepoFile(_ context.Context, _, path string, _ bool) (*git.FileContent, error) {
	if path == "image.png" {
		return &git.FileContent{Path: path, Content: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", int(git.MaxImageBytes)))), MimeType: "image/png", Binary: true, Size: git.MaxImageBytes}, nil
	}
	// '<' marshals as \u003c: 1 MiB of it is 6 MiB of JSON.
	return &git.FileContent{Path: path, Content: strings.Repeat("<", int(git.MaxFileBytes)), Size: git.MaxFileBytes}, nil
}

func TestRemoteHostRepoFilesLargeResponses(t *testing.T) {
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), bigFilesHost{}, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	ctx := context.Background()
	if list, err := host.ListRepoFiles(ctx, "/r", false); err != nil || len(list.Files) != git.MaxListedFiles {
		t.Fatalf("large listing: %v", err)
	}
	if file, err := host.ReadRepoFile(ctx, "/r", "a", false); err != nil || len(file.Content) != int(git.MaxFileBytes) {
		t.Fatalf("escape-heavy file: %v", err)
	}
	file, err := host.ReadRepoFile(ctx, "/r", "image.png", false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil || len(data) != int(git.MaxImageBytes) || file.MimeType != "image/png" || !file.Binary || file.Truncated {
		t.Fatalf("remote image metadata/bytes lost: size=%d, mime=%q, err=%v", len(data), file.MimeType, err)
	}
}
