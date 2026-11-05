package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type worktreeInventoryPlatform struct {
	*fakePlatform
	rows []db.Session
	err  error
}

func (p *worktreeInventoryPlatform) WorktreeSessions(context.Context) ([]db.Session, error) {
	return p.rows, p.err
}

func TestRemoteMaintenanceInventoryPreservesNativeChildren(t *testing.T) {
	p := &worktreeInventoryPlatform{fakePlatform: &fakePlatform{id: "opencode"}, rows: []db.Session{{ID: "child", ParentID: "root", Directory: "/worktree", SourceChild: true, TimeUpdated: 123}}}
	registry := platforms.NewRegistry()
	registry.Register(p)
	conn := NewRemoteConn(startRealRemote(t, "tok", "rid", registry), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	rows, err := rp.WorktreeSessions(t.Context())
	if err != nil || len(rows) != 1 || !rows[0].SourceChild || rows[0].ParentID != "root" || rows[0].TimeUpdated != 123 {
		t.Fatalf("incomplete inventory: %+v %v", rows, err)
	}
	p.err = errors.New("incomplete history")
	if _, err := rp.WorktreeSessions(t.Context()); err == nil {
		t.Fatal("ignored owner inventory error")
	}
	unsupported := newRemotePlatform(connectedPair(t, &fakePlatform{id: "opencode"}), "opencode", func() string { return "Box" })
	if _, err := unsupported.WorktreeSessions(t.Context()); err == nil {
		t.Fatal("older owner returned an apparently complete display list")
	}
	offline := newRemotePlatform(&RemoteConn{}, "opencode", func() string { return "Box" })
	if _, err := offline.WorktreeSessions(t.Context()); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
}
