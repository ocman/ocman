package remote

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func TestProjectUpstreamsRPCDoesNotSerializeRemoteURLs(t *testing.T) {
	srv := NewServer(platforms.NewRegistry(), &forgeRoundTripHost{}, "rid", "v")
	resp, err := srv.ProjectUpstreams(context.Background(), &pb.JsonReq{Payload: []byte(`{"dir":"/owner/repo"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(resp.Payload, []byte("secret")) || bytes.Contains(resp.Payload, []byte(`"url"`)) {
		t.Fatalf("ProjectUpstreams serialized a remote URL: %s", resp.Payload)
	}
}

type forgeRoundTripHost struct {
	localStubHost
	projectDir  string
	fetch       hostsvc.FetchPRHeadRequest
	upstreamErr error
}

func (h *forgeRoundTripHost) ProjectUpstreams(_ context.Context, dir string) (*hostsvc.ProjectUpstreams, error) {
	h.projectDir = dir
	return &hostsvc.ProjectUpstreams{RepoRoot: "/owner/repo", Remotes: []forge.Remote{{Name: "origin", URL: "https://user:secret@github.com/owner/repo.git", Type: forge.RemoteTypeGitHub, Host: "github.com", Repo: "owner/repo"}}}, h.upstreamErr
}

func TestRemoteHostProjectUpstreamsPreservesNotARepo(t *testing.T) {
	owner := &forgeRoundTripHost{upstreamErr: git.ErrNotARepo}
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), owner, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})

	_, err := host.ProjectUpstreams(context.Background(), "/not/repo")
	if !errors.Is(err, git.ErrNotARepo) {
		t.Fatalf("error = %v, want git.ErrNotARepo", err)
	}
}

func (h *forgeRoundTripHost) FetchPRHead(_ context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
	h.fetch = req
	return "ocman/pr-42", nil
}

func TestRemoteHostProjectForgeRoundTrips(t *testing.T) {
	owner := &forgeRoundTripHost{}
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), owner, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})

	upstreams, err := host.ProjectUpstreams(context.Background(), "/owner/repo/subdir")
	if err != nil {
		t.Fatal(err)
	}
	wantRemotes := []forge.Remote{{Name: "origin", Type: forge.RemoteTypeGitHub, Host: "github.com", Repo: "owner/repo"}}
	if owner.projectDir != "/owner/repo/subdir" || upstreams.RepoRoot != "/owner/repo" || !reflect.DeepEqual(upstreams.Remotes, wantRemotes) {
		t.Fatalf("owner dir = %q, upstreams = %+v", owner.projectDir, upstreams)
	}

	req := hostsvc.FetchPRHeadRequest{RepoRoot: "/owner/repo", Remote: "origin", Number: 42}
	branch, err := host.FetchPRHead(context.Background(), req)
	if err != nil || branch != "ocman/pr-42" || owner.fetch != req {
		t.Fatalf("FetchPRHead() = %q, %v; owner request = %+v", branch, err, owner.fetch)
	}
}
