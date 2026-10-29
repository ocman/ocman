package prworkflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
)

type forgeOnly struct{ forge.Forge }

func TestLaunchLinkAndWorkerLifetime(t *testing.T) {
	x := newFixture(t)
	if x.m.Name() != "pr-workflow" {
		t.Fatal(x.m.Name())
	}
	target := Target{"r-box:opencode", "s1", "/repo", "box"}
	if err := x.m.LinkCreated(t.Context(), target, "origin", 42, &x.f.pr); err != nil {
		t.Fatal(err)
	}
	if x.f.reads != 0 {
		t.Fatal("launch unnecessarily fetched metadata again")
	}
	if err := x.m.LinkCreated(t.Context(), target, "missing", 42); err == nil {
		t.Fatal("missing remote accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	x.m.Run(ctx)
	New(Deps{}).Run(ctx)
	l := x.only(t)
	x.f.err = errors.New("offline")
	w := x.request(t, "pull-requests", "", 200)
	if !strings.Contains(w.Body.String(), "last saved status") {
		t.Fatal(w.Body.String())
	}
	if _, err := x.m.newLink(t.Context(), target, "origin", 42); err == nil {
		t.Fatal("forge failure ignored")
	}
	x.f.err = nil
	x.m.d.Owner = nil
	if _, err := x.m.newLink(t.Context(), target, "origin", 42); err == nil {
		t.Fatal("missing owner accepted")
	}
	if err := x.m.checkOwner(t.Context(), l); err == nil {
		t.Fatal("missing owner accepted")
	}
	x.m.d.Owner = func(context.Context, Target) (hostsvc.Host, error) {
		return fakeHost{err: errors.New("git unavailable")}, nil
	}
	if _, err := x.m.newLink(t.Context(), target, "origin", 42); err == nil {
		t.Fatal("git failure ignored")
	}
	x.m.d.Owner = func(context.Context, Target) (hostsvc.Host, error) { return fakeHost{}, nil }
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return nil, false }
	if _, err := x.m.newLink(t.Context(), target, "origin", 42); err == nil {
		t.Fatal("missing forge accepted")
	}
	if _, _, err := x.m.read(t.Context(), l); err == nil {
		t.Fatal("missing forge accepted")
	}
}

func TestFeedbackFailurePaths(t *testing.T) {
	for _, mode := range []string{"owner", "forge", "metadata", "comments", "unsupported", "checks"} {
		t.Run(mode, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			want := 502
			action := "feedback"
			switch mode {
			case "owner":
				x.ownerErr = errors.New("offline")
				want = 503
			case "forge":
				x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return nil, false }
				want = 503
			case "metadata":
				x.f.err = errors.New("unavailable")
			case "comments":
				x.f.feedbackErr = errors.New("incomplete page")
			case "unsupported":
				x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return forgeOnly{x.f}, true }
				want = 501
			case "checks":
				x.f.limited = true
				action = "checks"
			}
			x.request(t, "pull-request-feedback", `{"host":"github.com","repo":"team/repo","number":42,"action":"`+action+`"}`, want)
		})
	}
	x := newFixture(t)
	x.link(t)
	for i := 0; i < 31; i++ {
		x.f.remarks = append(x.f.remarks, forge.Remark{Body: strings.Repeat("é", 1700), Author: "reviewer"})
	}
	w := x.request(t, "pull-request-feedback", `{"host":"github.com","repo":"team/repo","number":42,"action":"feedback"}`, 200)
	if !strings.Contains(w.Body.String(), "More comments exist") {
		t.Fatal("missing truncation notice")
	}
	x.f.checks = forge.CIStatus{Checks: []forge.Check{{Name: "tests", State: forge.CIStateFailure}}}
	w = x.request(t, "pull-request-feedback", `{"host":"github.com","repo":"team/repo","number":42,"action":"checks"}`, 200)
	if !strings.Contains(w.Body.String(), "tests") {
		t.Fatal("missing check")
	}
	x.request(t, "pull-request-feedback", `{`, 400)
	x.target = false
	x.request(t, "pull-request-feedback", `{}`, 503)
}

func TestReadFailuresAndOwnRemarks(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	l := x.only(t)
	for _, kind := range []string{"checks", "feedback", "viewer"} {
		err := errors.New("unavailable")
		x.f.checkErr, x.f.feedbackErr, x.f.userErr = nil, nil, nil
		switch kind {
		case "checks":
			x.f.checkErr = err
		case "feedback":
			x.f.feedbackErr = err
		case "viewer":
			x.f.userErr = err
		}
		if _, _, err := x.m.read(t.Context(), l); err == nil {
			t.Fatal(kind)
		}
	}
	x.f.userErr = nil
	x.f.remarks = []forge.Remark{{ID: "own", Author: "ME", Body: "ignore"}, {ID: "other", Author: "reviewer", Body: "fix"}}
	snapshot, _, err := x.m.read(t.Context(), l)
	if err != nil || len(snapshot.Remarks) != 1 {
		t.Fatal(snapshot, err)
	}
	x.f.pr.HeadSHA = ""
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return forgeOnly{x.f}, true }
	if _, _, err := x.m.read(t.Context(), l); err != nil {
		t.Fatal(err)
	}
}

func TestStorageFailures(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	if err := x.m.d.DB.Close(); err != nil {
		t.Fatal(err)
	}
	x.request(t, "pull-requests", "", 500)
	x.request(t, "pull-requests", `{"action":"watch","number":42}`, 500)
	x.request(t, "pull-request-feedback", `{"action":"feedback"}`, 500)
	x.m.sweep(t.Context())
	x.m.d.DB = nil
	x.request(t, "pull-requests", `{"action":"link","number":42}`, 503)
	if err := x.m.put(t.Context(), Link{}); err == nil {
		t.Fatal("missing store accepted")
	}
	if links, err := x.m.links(t.Context(), "", ""); err != nil || len(links) != 0 {
		t.Fatal(links, err)
	}
}

func TestUnknownReadsDoNotClearCheckAndConflictState(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	conflict := false
	x.f.pr.Mergeable = &conflict
	x.f.checks = forge.CIStatus{State: forge.CIStateFailure, Checks: []forge.Check{{Name: "tests", State: forge.CIStateFailure}}}
	x.m.sweep(t.Context())
	x.f.pr.Mergeable = nil
	x.f.checks = forge.CIStatus{State: forge.CIStateUnknown}
	x.m.sweep(t.Context())
	l := x.only(t)
	if !l.Snapshot.Conflict || l.Snapshot.Checks != forge.CIStateFailure {
		t.Fatal("unknown response erased known state")
	}
}
