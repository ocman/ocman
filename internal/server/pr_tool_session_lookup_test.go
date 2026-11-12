package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func TestPRToolSessionLookupUsesExactConnectionAndDirectory(t *testing.T) {
	s := &Server{registry: platforms.NewRegistry(), hostRouter: hostsvc.NewRouter(nil)}
	p := &fakePlatform{PlatformID: "opencode", SessionsFn: func(ctx context.Context, dir string, since int64) ([]db.Session, error) {
		if dir != "/repo" || since != 0 {
			t.Fatalf("lookup truncated or broadened: %q %d", dir, since)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded lookup")
		}
		return []db.Session{{ID: "s1", Directory: "/repo"}, {ID: "s2", Directory: "/repo/sub"}, {Directory: "/repo"}}, nil
	}}
	if err := s.registry.RegisterConnection("cnx_work", p); err != nil {
		t.Fatal(err)
	}
	s.registry.Register(&fakePlatform{PlatformID: "other", SessionsErr: errors.New("unavailable")})
	rows, err := s.prToolSessions(t.Context(), "/repo", "cnx_work", "local")
	if err != nil || len(rows) != 1 || rows[0].Platform != "cnx_work" || rows[0].SessionID != "s1" || rows[0].RemoteID != "local" {
		t.Fatalf("lookup: %+v %v", rows, err)
	}
	// An unscoped partial read must not silently treat the first match as unique.
	if rows, err := s.prToolSessions(t.Context(), "/repo", "", ""); err == nil || rows != nil {
		t.Fatalf("partial lookup accepted: %+v %v", rows, err)
	}
	for _, scope := range []struct{ platform, owner string }{{"cnx_missing", ""}, {"", "offline"}, {"r-offline:opencode", "local"}} {
		if rows, err := s.prToolSessions(t.Context(), "/repo", scope.platform, scope.owner); err == nil || rows != nil {
			t.Fatalf("explicit owner fell back: %+v %+v %v", scope, rows, err)
		}
	}
}

func TestPRTargetPreservesLookedUpConnection(t *testing.T) {
	s := testServer(t)
	p := &fakePlatform{PlatformID: "opencode", SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/repo"}}, nil
	}}
	if err := s.registry.RegisterConnection("cnx_work", p); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/session/s1/pull-requests?platform=cnx_work", nil)
	target, ok := s.prTarget(httptest.NewRecorder(), r, "s1")
	if !ok || target.Platform != "cnx_work" {
		t.Fatalf("lost lookup connection: %+v, ok=%v", target, ok)
	}
	p.SessionFn = func(id string) (*platforms.SessionDetail, error) {
		s.registry.Unregister("cnx_work")
		return &platforms.SessionDetail{Session: &db.Session{ID: id}}, nil
	}
	w := httptest.NewRecorder()
	if _, ok := s.prTarget(w, r, "s1"); ok || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unregistered owner accepted: %d", w.Code)
	}
}

func TestPRToolSessionLookupKeepsAllMatches(t *testing.T) {
	s := &Server{registry: platforms.NewRegistry()}
	for _, key := range []string{"cnx_one", "r-box:cnx_two"} {
		if err := s.registry.RegisterConnection(platforms.ID(key), &fakePlatform{SessionList: []db.Session{{ID: key, Directory: "/repo"}}}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.prToolSessions(t.Context(), "/repo", "", "")
	if err != nil || len(rows) != 2 || rows[1].RemoteID != "box" {
		t.Fatalf("matches: %+v %v", rows, err)
	}
	rows, err = s.prToolSessions(t.Context(), "/other", "", "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("wrong-directory matches: %+v %v", rows, err)
	}
}
