package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
)

type sessionLinksResponse struct {
	response platforms.PullRequestResponse
}

func (s sessionLinksResponse) PullRequestOperation(context.Context, platforms.PullRequestRequest) (platforms.PullRequestResponse, error) {
	return s.response, nil
}

func TestLinkedSessionsFailures(t *testing.T) {
	t.Run("owner resolution", func(t *testing.T) {
		x := newFixture(t)
		x.m.d.Remote = func(Target) (platforms.PullRequestWorkflow, error) { return nil, errors.New("offline") }
		if _, err := x.m.LinkedSessions(t.Context(), "r-box:opencode", "github.com", "team/repo"); err == nil {
			t.Fatal("owner failure ignored")
		}
	})
	for _, response := range []platforms.PullRequestResponse{{Status: 400}, {Status: 200, Body: []byte("invalid")}} {
		t.Run(string(response.Body), func(t *testing.T) {
			x := newFixture(t)
			x.m.d.Remote = func(Target) (platforms.PullRequestWorkflow, error) { return sessionLinksResponse{response}, nil }
			if _, err := x.m.LinkedSessions(t.Context(), "r-box:opencode", "github.com", "team/repo"); err == nil {
				t.Fatal("invalid owner result accepted")
			}
		})
	}
	t.Run("storage", func(t *testing.T) {
		x := newFixture(t)
		if err := x.m.d.DB.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := x.m.LinkedSessions(t.Context(), "opencode", "github.com", "team/repo"); err == nil {
			t.Fatal("storage failure ignored")
		}
		response := x.m.Operation(t.Context(), "opencode", platforms.PullRequestRequest{Operation: "linked-sessions", Query: url.Values{"host": {"github.com"}, "repo": {"team/repo"}}})
		if response.Status != 500 {
			t.Fatalf("storage failure status=%d", response.Status)
		}
	})
}

func TestLinkedSessionsScope(t *testing.T) {
	x := newFixture(t)
	for _, l := range []Link{
		{Platform: "opencode", SessionID: "one", Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/repo"}},
		{Platform: "opencode", SessionID: "two", Directory: "/worktree", Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/repo"}},
		{Platform: "claude", SessionID: "other-platform", Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/repo"}},
		{Platform: "opencode", SessionID: "other-host", Number: 42, Remote: forge.Remote{Host: "code.example", Repo: "team/repo"}},
		{Platform: "opencode", SessionID: "other-repo", Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/other"}},
	} {
		if err := x.m.put(t.Context(), l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := x.m.LinkedSessions(t.Context(), "opencode", "github.com", "team/repo")
	want := []SessionLink{{"opencode", "one", 42}, {"opencode", "two", 42}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("links=%+v err=%v", got, err)
	}
	if x.f.reads != 0 {
		t.Fatal("reverse lookup contacted the forge")
	}
	got, err = New(Deps{}).LinkedSessions(t.Context(), "opencode", "github.com", "team/repo")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty storage: %+v %v", got, err)
	}
}

func TestLinkedSessionsRemoteAndLegacy(t *testing.T) {
	hub, owner, bridge := bridgeFixtures(t)
	owner.link(t)
	legacy := ownerLink(t, owner)
	legacy.Platform = "r-box:opencode"
	if err := hub.m.put(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	got, err := hub.m.LinkedSessions(t.Context(), legacy.Platform, "github.com", "team/repo")
	if err != nil || !reflect.DeepEqual(got, []SessionLink{{legacy.Platform, "s1", 42}}) {
		t.Fatalf("remote identity/dedup: %+v %v", got, err)
	}
	bridge.err = errors.New("offline")
	if _, err := hub.m.LinkedSessions(t.Context(), legacy.Platform, "github.com", "team/repo"); err == nil {
		t.Fatal("disconnected owner returned partial hub state")
	}
}

func TestLinkedSessionsOperation(t *testing.T) {
	x := ownerFixture(t)
	x.link(t)
	for _, tc := range []struct {
		platform      string
		query         url.Values
		status, count int
	}{
		{"opencode", url.Values{"host": {"github.com"}, "repo": {"team/repo"}}, 200, 1},
		{"claude", url.Values{"host": {"github.com"}, "repo": {"team/repo"}}, 200, 0},
		{"opencode", nil, 400, 0},
	} {
		response := x.m.Operation(t.Context(), tc.platform, platforms.PullRequestRequest{Operation: "linked-sessions", Query: tc.query})
		if response.Status != tc.status {
			t.Fatalf("status=%d body=%s", response.Status, response.Body)
		}
		if response.Status == http.StatusOK {
			var links []SessionLink
			if err := json.Unmarshal(response.Body, &links); err != nil || len(links) != tc.count {
				t.Fatalf("links=%s err=%v", response.Body, err)
			}
		}
	}
}
