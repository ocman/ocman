package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func TestParentOnlyWorktreeKeepsExactNamedConnection(t *testing.T) {
	s, registry := newSessionsTestServer(t)
	for _, key := range []string{"cnx_personal", "cnx_work"} {
		p := &platformtest.Fake{PlatformID: "claudecode"}
		if key == "cnx_work" {
			p.SessionList = []db.Session{{ID: "parent"}}
		}
		if err := registry.RegisterConnection(platforms.ID(key), p); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", nil)
	id, base, err := s.worktreePlatform(r, &createPathHost{}, "", "", "parent")
	if err != nil || id != "cnx_work" || base != "cnx_work" {
		t.Fatalf("parent account became a legacy kind: id=%q base=%q error=%v", id, base, err)
	}
	if _, _, err := s.worktreePlatform(r, &createPathHost{}, "", "", "missing"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("unknown parent borrowed a default account: %v", err)
	}
}

type unregisteringParent struct {
	*platformtest.Fake
	registry *platforms.Registry
}

func (p *unregisteringParent) Owns(context.Context, string) bool {
	p.registry.Unregister("cnx_parent")
	return true
}

func TestParentWorktreeFailsClosedWhenRegistrationDisappears(t *testing.T) {
	s, registry := newSessionsTestServer(t)
	p := &unregisteringParent{Fake: &platformtest.Fake{PlatformID: "claudecode"}, registry: registry}
	if err := registry.RegisterConnection("cnx_parent", p); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", nil)
	if _, _, err := s.worktreePlatform(r, &createPathHost{}, "", "", "parent"); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("unregistered parent borrowed a legacy account: %v", err)
	}
}

func TestParentWorktreeRetainsOwnerValidation(t *testing.T) {
	for _, tc := range []struct {
		key, owner string
		valid      bool
	}{{"r-box:cnx_parent", "other", false}, {"cnx_parent", "box", false}, {"r-box:cnx_parent", "", false}, {"r-box:cnx_parent", "box", true}} {
		t.Run(tc.key+"/"+tc.owner, func(t *testing.T) {
			s, registry := newSessionsTestServer(t)
			p := &createNamedRemote{Fake: &platformtest.Fake{PlatformID: tc.key, SessionList: []db.Session{{ID: "parent"}}}, kind: "claudecode"}
			if err := registry.RegisterConnection(platforms.ID(tc.key), p); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", nil)
			id, _, err := s.worktreePlatform(r, &createPathHost{owner: tc.owner}, "", "", "parent")
			if tc.valid && (err != nil || id != tc.key) {
				t.Fatalf("valid parent owner lost: %s %v", id, err)
			}
			if !tc.valid && !errors.Is(err, platforms.ErrNotFound) {
				t.Fatalf("parent routing bypassed explicit owner: %s %v", id, err)
			}
		})
	}
}

func TestParentWorktreeWithoutRegistryFailsClosed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", nil)
	if _, _, err := (&Server{}).worktreePlatform(r, &createPathHost{}, "", "", "parent"); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
}
