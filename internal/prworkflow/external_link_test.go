package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
)

type externalForge struct {
	*fakeForge
	repo   string
	number int
}

func (f *externalForge) LookupPR(ctx context.Context, repo string, number int) (forge.PR, error) {
	f.repo, f.number = repo, number
	return f.fakeForge.LookupPR(ctx, repo, number)
}

func TestExternalRepositoryLink(t *testing.T) {
	for _, host := range []string{"github.com", "forge.example.com"} {
		t.Run(host, func(t *testing.T) {
			x := newFixture(t)
			f := &externalForge{fakeForge: x.f}
			x.m.d.ResolveForge = func(rem forge.Remote) (forge.Forge, bool) {
				wantType := forge.RemoteTypeForgejo
				if host == "github.com" {
					wantType = forge.RemoteTypeGitHub
				}
				if rem.Host != host || rem.Repo != "other/project" || rem.Type != wantType {
					t.Fatalf("wrong forge target: %+v", rem)
				}
				return f, true
			}
			body := `{"action":"link","host":"` + host + `","repo":"other/project","number":42}`
			x.request(t, "pull-requests", body, 200)
			l := x.only(t)
			if l.Directory != "/repo" || l.Platform != "r-box:opencode" || l.SessionID != "s1" || l.RemoteID != "box" || l.Remote.Repo != "other/project" || l.Watching {
				t.Fatalf("incorrect relationship: %+v", l)
			}
			if f.repo != "other/project" || f.number != 42 {
				t.Fatalf("lookup: %+v", f)
			}
			x.request(t, "pull-requests", `{"action":"watch","host":"`+host+`","repo":"other/project","number":42}`, 200)
			x.request(t, "pull-requests", body, 200)
			if !x.only(t).Watching {
				t.Fatal("relink reset watch")
			}
			x.m = New(x.m.d)
			w := x.request(t, "pull-requests", "", 200)
			var result struct {
				PullRequests []Link `json:"pullRequests"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.PullRequests) != 1 || result.PullRequests[0].Remote.Repo != "other/project" {
				t.Fatalf("persisted links: %s (%v)", w.Body.String(), err)
			}
			x.request(t, "pull-requests", `{"action":"unlink","host":"`+host+`","repo":"other/project","number":42}`, 204)
		})
	}
}

func TestExternalLinkFailures(t *testing.T) {
	for _, body := range []string{
		`{"host":"github.com"}`, `{"repo":"other/project"}`,
		`{"host":"github.com","repo":"../project"}`,
		`{"host":"https://github.com","repo":"other/project"}`,
		`{"remote":"origin","host":"github.com","repo":"other/project"}`,
	} {
		t.Run(body, func(t *testing.T) {
			x := newFixture(t)
			x.request(t, "pull-requests", `{"action":"link","number":42,`+body[1:], 400)
			if x.f.reads != 0 {
				t.Fatal("invalid target reached forge")
			}
		})
	}
	for _, failure := range []string{"owner", "forge", "lookup"} {
		t.Run(failure, func(t *testing.T) {
			x := newFixture(t)
			switch failure {
			case "owner":
				x.ownerErr = errors.New("offline")
			case "forge":
				x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return nil, false }
			case "lookup":
				x.f.err = errors.New("not found")
			}
			x.request(t, "pull-requests", `{"action":"link","host":"github.com","repo":"other/project","number":42}`, 502)
			links, err := x.m.links(t.Context(), "r-box:opencode", "s1")
			if err != nil || len(links) != 0 {
				t.Fatalf("failed link persisted: %v %v", links, err)
			}
		})
	}
}

func TestExternalLinkMCP(t *testing.T) {
	x := newFixture(t)
	result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(map[string]any{
		"action": "link", "session_id": "s1", "platform": "r-box:opencode",
		"host": "github.com", "repo": "other/project", "number": float64(42),
	}))
	if err != nil || result.IsError {
		t.Fatalf("link: %+v %v", result, err)
	}
	if x.only(t).Remote.Repo != "other/project" {
		t.Fatal("linked session repository instead")
	}
}

func TestExternalLinkValidationAndMissingOwner(t *testing.T) {
	x := newFixture(t)
	target := Target{"opencode", "s1", "/not-a-checkout", "local"}
	if _, err := x.m.externalLink(t.Context(), target, changeRequest{Host: "github.com", Repo: "invalid"}); err == nil {
		t.Fatal("invalid repository accepted")
	}
	x.m.d.Owner = func(context.Context, Target) (hostsvc.Host, error) {
		return fakeHost{err: errors.New("not a Git checkout")}, nil
	}
	l, err := x.m.externalLink(t.Context(), target, changeRequest{Host: "github.com", Repo: "other/project", Number: 42})
	if err != nil || l.Directory != target.Directory || l.Platform != "opencode" || l.RemoteID != "local" {
		t.Fatalf("link incorrectly depends on session checkout: %+v %v", l, err)
	}
	x.m.d.Owner = nil
	if _, err := x.m.externalLink(t.Context(), target, changeRequest{Host: "github.com", Repo: "other/project", Number: 42}); err == nil {
		t.Fatal("missing owner accepted")
	}
}

func TestExternalLinksDoNotCollideWithSessionRepository(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.request(t, "pull-requests", `{"action":"link","host":"github.com","repo":"other/project","number":42}`, 200)
	x.request(t, "pull-requests", `{"action":"link","host":"github.com","repo":"another/project","number":42}`, 200)
	links, err := x.m.links(t.Context(), "r-box:opencode", "s1")
	if err != nil || len(links) != 3 {
		t.Fatalf("same-number PR relationships collided: %v %v", links, err)
	}
	x.request(t, "pull-requests", `{"action":"unlink","host":"github.com","repo":"other/project","number":42}`, 204)
	links, err = x.m.links(t.Context(), "r-box:opencode", "s1")
	if err != nil || len(links) != 2 {
		t.Fatalf("unlink changed other relationships: %v %v", links, err)
	}
	for _, link := range links {
		if link.Remote.Repo == "other/project" {
			t.Fatal("unlink retained the selected PR")
		}
	}
}
