package server

import (
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/projectforge"
)

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
