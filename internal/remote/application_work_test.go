package remote

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func connectWorkOwner(t *testing.T, srv *Server) *RemoteConn {
	t.Helper()
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	conn := NewRemoteConn("grpc://"+ln.Addr(), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return conn
}

func TestApplicationTaskObservationsRoundTripToOwner(t *testing.T) {
	manager := tasksvc.New(statetest.Open(t, tasksvc.Schema), 1)
	if err := manager.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	if err := manager.Do(t.Context(), tasksvc.Spec{Kind: "scan", Title: "Owner scan"}, func(context.Context, *tasksvc.Reporter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	srv := NewServer(reg, localStubHost{}, "owner", "test").UseBackgroundTasks(manager)
	conn := connectWorkOwner(t, srv)
	reader := newRemoteHost(conn).BackgroundTaskReader()
	tasks, err := reader.List(t.Context(), tasksvc.Succeeded, "scan", 10)
	if err != nil || len(tasks) != 1 || tasks[0].OwnerID != "owner" || tasks[0].Title != "Owner scan" {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	task, err := reader.Get(t.Context(), tasks[0].ID)
	if err != nil || task.OwnerID != "owner" || task.ID != tasks[0].ID {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if _, err := reader.Get(t.Context(), "missing"); !errors.Is(err, tasksvc.ErrNotFound) {
		t.Fatalf("missing=%v", err)
	}
	if count, err := reader.ActiveCount(t.Context()); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	for _, q := range []applicationTaskQuery{{Operation: "execute"}, {Operation: "list", Limit: 201}, {Operation: "list", Limit: 1, Status: "invalid"}} {
		payload, _ := json.Marshal(q)
		if _, err := srv.ApplicationTasks(t.Context(), &pb.CallReq{Payload: payload}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("query=%+v err=%v", q, err)
		}
	}
	if _, err := srv.ApplicationTasks(t.Context(), &pb.CallReq{Payload: []byte("bad")}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := reader.List(t.Context(), "", "", 10); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline=%v", err)
	}
}

func TestPullRequestOperationsUseResolvedOwnerPlatform(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	srv := NewServer(reg, localStubHost{}, "owner", "test")
	srv.UsePullRequests(func(_ context.Context, platform string, req platforms.PullRequestRequest) platforms.PullRequestResponse {
		if platform != "opencode" || req.SessionID != "s1" || req.Operation != "list" {
			t.Errorf("platform=%s request=%+v", platform, req)
		}
		return platforms.PullRequestResponse{Status: 200, ContentType: "application/json", Body: []byte(`{"pullRequests":[]}`)}
	})
	conn := connectWorkOwner(t, srv)
	p := newRemotePlatform(conn, "opencode", func() string { return "Owner" })
	response, err := p.PullRequestOperation(t.Context(), platforms.PullRequestRequest{Operation: "list", SessionID: "s1"})
	if err != nil || response.Status != 200 || string(response.Body) != `{"pullRequests":[]}` {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	for _, req := range []*pb.CallReq{
		{Platform: "r-other:opencode", Payload: []byte(`{}`)},
		{Platform: "opencode", Payload: []byte(`bad`)},
	} {
		if _, err := srv.PullRequestOperation(t.Context(), req); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("request=%+v err=%v", req, err)
		}
	}
	if _, err := srv.PullRequestOperation(t.Context(), &pb.CallReq{Platform: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := p.PullRequestOperation(t.Context(), platforms.PullRequestRequest{}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline=%v", err)
	}
}

func TestApplicationWorkWithoutOwnerServicesFailsClosed(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	srv := NewServer(reg, localStubHost{}, "owner", "test")
	if _, err := srv.ApplicationTasks(t.Context(), &pb.CallReq{}); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	if _, err := srv.PullRequestOperation(t.Context(), &pb.CallReq{Platform: "opencode"}); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
}
