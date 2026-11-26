package server

import (
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectforge"
)

type prSidebarSnapshotPlatform struct {
	*fakePlatform
	ready bool
}

func (p *prSidebarSnapshotPlatform) PullRequestSidebar() ([]platforms.PullRequestSidebarLink, bool) {
	return nil, p.ready
}

func TestSidebarPRLinkedSessionsDistinguishesUnavailableFromEmpty(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	p := &prSidebarSnapshotPlatform{fakePlatform: &fakePlatform{PlatformID: "r-box:fake"}}
	reg.Register(p)
	if got, err := s.sidebarPRLinkedSessions(t.Context(), "box", "github.com", "team/repo"); err == nil || got != nil {
		t.Fatalf("before sync: links=%+v err=%v", got, err)
	}
	p.ready = true
	if got, err := s.sidebarPRLinkedSessions(t.Context(), "box", "github.com", "team/repo"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("after empty sync: links=%+v err=%v", got, err)
	}
}

func TestPRLinkedSessionsOwnerScopeAndOrder(t *testing.T) {
	s, reg := newSessionsTestServer(t)
	for _, platform := range []string{"z-local", "a-local", "r-box:fake"} {
		reg.Register(&fakePlatform{PlatformID: platform})
		seedPRWatch(t, s, platform, "s2", false)
		seedPRWatch(t, s, platform, "s1", false)
	}
	got, err := s.prLinkedSessions(t.Context(), "local", "github.com", "team/repo")
	want := []projectforge.SessionRef{{Platform: "a-local", SessionID: "s1"}, {Platform: "a-local", SessionID: "s2"}, {Platform: "z-local", SessionID: "s1"}, {Platform: "z-local", SessionID: "s2"}}
	if err != nil || !reflect.DeepEqual(got[42], want) {
		t.Fatalf("links=%+v err=%v", got, err)
	}
	if _, err := s.prLinkedSessions(t.Context(), "box", "github.com", "team/repo"); err == nil {
		t.Fatal("remote without workflow support fell back to hub state")
	}
}
