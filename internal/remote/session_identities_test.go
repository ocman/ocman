package remote

import (
	"context"
	"testing"
)

// An owner without canonical history answers identity lookups with an error
// instead of rows the hub could cache as proof of ownership.
func TestRemoteSessionIdentitiesWithoutOwnerHistoryFail(t *testing.T) {
	conn := connectedPair(t, &fakePlatform{id: "opencode"})
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	if rows, err := rp.SessionIdentities(context.Background(), []string{"s1"}); err == nil || rows != nil {
		t.Fatalf("identities = %+v, %v", rows, err)
	}
	if checks := newRemoteHost(conn).Doctor(context.Background()); checks != nil {
		t.Fatalf("remote doctor checks = %+v", checks)
	}
}
