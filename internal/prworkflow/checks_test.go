package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/forge"
)

type linkedChecksForge struct {
	*fakeForge
	repo, sha string
}

func (f *linkedChecksForge) Checks(ctx context.Context, repo, sha string) (forge.CIStatus, forge.RateLimit, error) {
	f.repo, f.sha = repo, sha
	return f.fakeForge.Checks(ctx, repo, sha)
}

func TestLinkedChecks(t *testing.T) {
	x := newFixture(t)
	x.request(t, "pull-requests", `{"action":"link","host":"github.com","repo":"other/project","number":42}`, 200)
	f := &linkedChecksForge{fakeForge: x.f}
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return f, true }
	for _, suffix := range []string{"", "&refresh=true"} {
		w := x.request(t, "pull-request-checks?host=github.com&repo=other/project&number=42&sha=abcdef"+suffix, "", 200)
		if f.repo != "other/project" || f.sha != "abcdef" {
			t.Fatalf("wrong check target: %+v", f)
		}
		var result struct {
			State     forge.CIState   `json:"state"`
			Checks    []forge.Check   `json:"checks"`
			RateLimit forge.RateLimit `json:"rateLimit"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.State != forge.CIStatePending || result.Checks == nil {
			t.Fatalf("checks: %s %v", w.Body.String(), err)
		}
	}
	x.f.limited = true
	w := x.request(t, "pull-request-checks?host=github.com&repo=other/project&number=42&sha=abcdef", "", 200)
	var result struct {
		RateLimit forge.RateLimit `json:"rateLimit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.RateLimit.Limited {
		t.Fatalf("lost retry metadata: %s", w.Body.String())
	}
}

func TestLinkedChecksFailures(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		status      int
	}{
		{"number", "number=0&sha=abcdef", 400},
		{"missing sha", "number=42", 400},
		{"bad sha", "number=42&sha=bad/sha", 400},
		{"unknown link", "number=43&sha=abcdef", 404},
		{"other repository", "number=42&sha=abcdef&host=github.com&repo=other/project", 404},
		{"owner", "number=42&sha=abcdef&host=github.com&repo=team/repo", 503},
		{"forge", "number=42&sha=abcdef&host=github.com&repo=team/repo", 503},
		{"lookup", "number=42&sha=abcdef&host=github.com&repo=team/repo", 502},
		{"target", "number=42&sha=abcdef", 503},
		{"storage", "number=42&sha=abcdef", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			switch tc.name {
			case "owner":
				x.ownerErr = errors.New("offline")
			case "forge":
				x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return nil, false }
			case "lookup":
				x.f.checkErr = errors.New("checks unavailable")
			case "target":
				x.target = false
			case "storage":
				_ = x.m.d.DB.Close()
			}
			x.request(t, "pull-request-checks?"+tc.query, "", tc.status)
		})
	}
}
