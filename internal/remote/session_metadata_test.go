package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type metadataFakePlatform struct {
	*summaryFakePlatform
	summaryCalls int
}

func (f *metadataFakePlatform) SessionMetadata(ctx context.Context, id string) (*db.Session, error) {
	return f.summaryFakePlatform.SessionSummary(ctx, id)
}

func (f *metadataFakePlatform) SessionSummary(ctx context.Context, id string) (*db.Session, error) {
	f.summaryCalls++
	return f.summaryFakePlatform.SessionSummary(ctx, id)
}

func TestRemoteAncestorMetadataAvoidsSummary(t *testing.T) {
	fp := &metadataFakePlatform{summaryFakePlatform: &summaryFakePlatform{fakePlatform: &fakePlatform{id: "opencode"}, row: db.Session{ID: "s1", ParentID: "root"}}}
	reg := platforms.NewRegistry()
	reg.Register(fp)
	conn := NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	// Exercise the same capability selection as the stream's ancestor walk.
	reader, ok := any(rp).(platforms.SessionMetadataReader)
	if !ok {
		t.Fatal("remote platform has no narrow metadata read")
	}
	row, err := reader.SessionMetadata(t.Context(), "s1")
	if err != nil || row.ParentID != "root" || row.Platform != string(rp.ID()) || row.RemoteID != rp.remoteID() || fp.summaryCalls != 0 || fp.detailCalls != 0 {
		t.Fatalf("metadata: %+v %v, summary calls %d, detail calls %d", row, err, fp.summaryCalls, fp.detailCalls)
	}
	if _, err := reader.SessionMetadata(t.Context(), "missing"); err == nil {
		t.Fatal("missing session accepted")
	}
}

func TestRemoteMetadataFailsClosedAndSupportsOldOwners(t *testing.T) {
	rp := newRemotePlatform(connectedPair(t, &fakePlatform{id: "opencode"}), "opencode", func() string { return "Box" })
	reader, ok := any(rp).(platforms.SessionMetadataReader)
	if !ok {
		t.Fatal("missing metadata capability")
	}
	if _, err := reader.SessionMetadata(t.Context(), "s1"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported: %v", err)
	}
	offline := newRemotePlatform(NewRemoteConn("grpc://127.0.0.1:1", "tok"), "opencode", func() string { return "Box" })
	if _, err := platforms.ReadSessionMetadata(t.Context(), offline, "s1"); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline: %v", err)
	}
	// An owner without metadata support can still serve an owner-local summary.
	legacy := &summaryFakePlatform{fakePlatform: &fakePlatform{id: "opencode"}, row: db.Session{ID: "s1", ParentID: "root"}}
	reg := platforms.NewRegistry()
	reg.Register(legacy)
	conn := NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldOwner := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	if row, err := platforms.ReadSessionMetadata(t.Context(), oldOwner, "s1"); err != nil || row.ParentID != "root" || legacy.detailCalls != 0 {
		t.Fatalf("legacy owner fallback: %+v %v", row, err)
	}
}
