package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

type summaryFakePlatform struct {
	*fakePlatform
	row         db.Session
	detailCalls int
}

func (f *summaryFakePlatform) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	if id != f.row.ID {
		return nil, platforms.ErrNotFound
	}
	return &f.row, nil
}

func (f *summaryFakePlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	f.detailCalls++
	return nil, errors.New("detail must not be read")
}

func TestRemoteSessionSummary(t *testing.T) {
	fp := &summaryFakePlatform{fakePlatform: &fakePlatform{id: "opencode"}, row: db.Session{ID: "s1", Title: "Old pinned", Status: db.StatusInterrupted}}
	reg := platforms.NewRegistry()
	reg.Register(fp)
	conn := NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	row, err := platforms.ReadSessionSummary(t.Context(), rp, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != fp.row.Title || row.Status != fp.row.Status || row.Platform != string(rp.ID()) || row.RemoteID != rp.remoteID() || row.RemoteName != "Box" || fp.detailCalls != 0 {
		t.Fatalf("summary = %+v; detail calls=%d", row, fp.detailCalls)
	}
	if _, err := rp.SessionSummary(t.Context(), "deleted"); err == nil {
		t.Fatal("missing session returned a row")
	}
}

func TestRemoteSessionSummaryUnsupportedAndOffline(t *testing.T) {
	rp := newRemotePlatform(connectedPair(t, &fakePlatform{id: "opencode"}), "opencode", func() string { return "Box" })
	if _, err := rp.SessionSummary(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported: %v", err)
	}
	offline := newRemotePlatform(NewRemoteConn("grpc://127.0.0.1:1", "tok"), "opencode", func() string { return "Box" })
	if _, err := offline.SessionSummary(t.Context(), "s1"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("offline: %v", err)
	}
	srv := NewServer(platforms.NewRegistry(), nil, "rid", "test")
	if _, err := srv.SessionSummary(t.Context(), &pb.SessionRef{Platform: "absent"}); err == nil {
		t.Fatal("unknown platform accepted")
	}
}
