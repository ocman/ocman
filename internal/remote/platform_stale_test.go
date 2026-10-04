package remote

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"google.golang.org/grpc"

	"github.com/NoUseFreak/ocman/internal/db"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// filteringSessionsClient answers Sessions like a remote ocman would
// (filtered by dir/since on the owner) and fails on demand.
type filteringSessionsClient struct {
	pb.OcmanClient

	mu       sync.Mutex
	sessions []db.Session
	fail     bool
}

func (c *filteringSessionsClient) Sessions(_ context.Context, in *pb.SessionsReq, _ ...grpc.CallOption) (*pb.JsonResp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return nil, errors.New("transport broken")
	}
	var out []db.Session
	for _, s := range c.sessions {
		if (in.Dir == "" || s.Directory == in.Dir) && (in.Since == 0 || s.TimeUpdated >= in.Since) {
			out = append(out, s)
		}
	}
	b, err := marshalJSON(out)
	return &pb.JsonResp{Payload: b}, err
}

func sessionIDs(sessions []db.Session) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestRemotePlatform_StaleFallbackHonoursFilters guards the offline
// fallback: a failed request must get last-known rows matching *its own*
// dir/since, a filtered success must not replace the broad snapshot, and
// ownership must never shrink because of a filtered listing.
func TestRemotePlatform_StaleFallbackHonoursFilters(t *testing.T) {
	ctx := context.Background()
	client := &filteringSessionsClient{sessions: []db.Session{
		{ID: "a-old", Directory: "/a", TimeUpdated: 100},
		{ID: "a-new", Directory: "/a", TimeUpdated: 300},
		{ID: "b-old", Directory: "/b", TimeUpdated: 100},
		{ID: "b-new", Directory: "/b", TimeUpdated: 300},
	}}
	conn := &RemoteConn{client: client, health: HealthConnected, remoteID: "rid"}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })

	list := func(dir string, since int64) []db.Session {
		t.Helper()
		got, err := rp.Sessions(ctx, dir, since)
		if err != nil {
			t.Fatalf("Sessions(%q, %d): %v", dir, since, err)
		}
		return got
	}
	expect := func(name string, got []db.Session, stale bool, want ...string) {
		t.Helper()
		if ids := sessionIDs(got); !slices.Equal(ids, want) {
			t.Fatalf("%s: ids = %v, want %v", name, ids, want)
		}
		for _, s := range got {
			if s.Stale != stale || s.RemoteID != "rid" || s.RemoteName != "Box" || s.Platform != "r-rid:opencode" {
				t.Fatalf("%s: bad stamping %+v (want stale=%v)", name, s, stale)
			}
		}
	}

	expect("broad", list("", 0), false, "a-new", "a-old", "b-new", "b-old")

	// A session created after the broad listing shows up in a filtered one.
	client.mu.Lock()
	client.sessions = append(client.sessions, db.Session{ID: "a-newer", Directory: "/a", TimeUpdated: 400})
	client.mu.Unlock()
	expect("filtered A", list("/a", 200), false, "a-new", "a-newer")

	// The remote fails while the adapter is still admitted.
	client.mu.Lock()
	client.fail = true
	client.mu.Unlock()

	expect("stale B since", list("/b", 200), true, "b-new")
	expect("stale B", list("/b", 0), true, "b-new", "b-old")
	expect("stale since", list("", 200), true, "a-new", "b-new")
	expect("stale broad", list("", 0), true, "a-new", "a-old", "b-new", "b-old")

	for _, id := range []string{"a-old", "a-new", "a-newer", "b-old", "b-new"} {
		if !rp.Owns(ctx, id) {
			t.Errorf("Owns(%q) = false after filtered listing, want true", id)
		}
	}

	// No client at all (disconnected) takes the same filtered path.
	conn.mu.Lock()
	conn.client = nil
	conn.mu.Unlock()
	expect("nil client A", list("/a", 0), true, "a-new", "a-old")

	// A broad success is authoritative: ownership shrinks to what it lists.
	conn.mu.Lock()
	conn.client = client
	conn.mu.Unlock()
	client.mu.Lock()
	client.fail = false
	client.sessions = client.sessions[:1]
	client.mu.Unlock()
	expect("broad after delete", list("", 0), false, "a-old")
	if rp.Owns(ctx, "b-new") {
		t.Error("Owns(b-new) = true after a broad listing dropped it")
	}
}
