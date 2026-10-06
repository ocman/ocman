package remote

import (
	"github.com/NoUseFreak/ocman/internal/db"
	"testing"
)

func TestCachedSessionIdentityDoesNotNeedConnection(t *testing.T) {
	p := &remotePlatform{lastSess: []db.Session{{ID: "s1", Directory: "/repo", ProjectID: "p", RemoteID: "box"}}}
	session, ok := p.CachedSession("s1")
	if !ok || session.Directory != "/repo" || session.ProjectID != "p" || session.RemoteID != "box" {
		t.Fatalf("session=%+v ok=%v", session, ok)
	}
	session.Directory = "/changed"
	unchanged, _ := p.CachedSession("s1")
	if unchanged.Directory != "/repo" {
		t.Fatal("caller mutated cached identity")
	}
	if _, ok := p.CachedSession("missing"); ok {
		t.Fatal("unknown identity found")
	}
}
