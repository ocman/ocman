package prworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
)

func TestExplicitRefreshProcessesWatch(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "checks", true: "merged"}[terminal], func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			x.watch(t)
			l := x.only(t)
			l.NextAt = time.Now().Add(time.Hour).UnixMilli()
			if err := x.m.put(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			x.f.checks.State = forge.CIStateSuccess
			if terminal {
				x.f.pr.Status = "merged"
			}
			reads, checks := x.f.reads, x.f.checkReads
			w := x.request(t, "pull-requests?refresh=true", "", 200)
			if len(x.sent) != 1 {
				t.Fatalf("refresh did not process watch: sent=%v", x.sent)
			}
			if x.f.reads != reads+1 {
				t.Fatal("refreshed PR metadata was fetched twice")
			}
			wantChecks := checks + 1
			if terminal {
				wantChecks = checks
			}
			if x.f.checkReads != wantChecks {
				t.Fatal("unexpected check reads")
			}
			var result struct {
				PullRequests []Link `json:"pullRequests"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.PullRequests[0].Watching == terminal || x.only(t).Watching == terminal {
				t.Fatal("response or stored watch state is stale")
			}
			x.request(t, "pull-requests?refresh=true", "", 200)
			if len(x.sent) != 1 {
				t.Fatal("refresh replayed update")
			}
		})
	}
}

func TestRefreshWatchGuards(t *testing.T) {
	for _, guard := range []string{"passive", "unwatched", "metadata failure", "owner offline", "rate limited"} {
		t.Run(guard, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			if guard != "unwatched" {
				x.watch(t)
			}
			x.f.checks.State = forge.CIStateSuccess
			path := "pull-requests?refresh=true"
			switch guard {
			case "passive":
				path = "pull-requests"
			case "metadata failure":
				x.f.err = errors.New("offline")
			case "owner offline":
				x.ownerErr = errors.New("disconnected")
			case "rate limited":
				x.m.paused["github/github.com"] = time.Now().Add(time.Hour)
			}
			checks := x.f.checkReads
			x.request(t, path, "", 200)
			if len(x.sent) != 0 || x.f.checkReads != checks {
				t.Fatal("refresh ignored watch guard")
			}
		})
	}
}

func TestRefreshRetainsFeedbackUntilSessionReady(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	// A refresh is scoped to the requested session, not every due watch.
	other := x.only(t)
	other.SessionID = "s2"
	if err := x.m.put(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	x.ready = false
	x.f.remarks = []forge.Remark{{ID: "review", Author: "other", Body: "Please fix"}}
	x.request(t, "pull-requests?refresh=true", "", 200)
	if len(x.sent) != 0 || x.only(t).Pending == "" {
		t.Fatal("refresh lost feedback for busy session")
	}
	links, err := x.m.links(t.Context(), other.Platform, other.SessionID)
	if err != nil || len(links) != 1 || links[0].Pending != "" || len(links[0].Snapshot.Remarks) != 0 {
		t.Fatal("refresh processed another session", links, err)
	}
	x.ready = true
	x.request(t, "pull-requests?refresh=true", "", 200)
	if len(x.sent) != 1 || x.only(t).Pending != "" {
		t.Fatal("refresh did not deliver pending feedback")
	}
}

func TestRateLimitDoesNotExhaustWatch(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.f.limited = true
	for range 10 {
		x.m.sweep(t.Context())
		l := x.only(t)
		if !l.Watching || l.Failures != 0 || l.NextAt <= time.Now().UnixMilli() {
			t.Fatalf("rate limit exhausted watch: %+v", l)
		}
		l.NextAt = 0
		if err := x.m.put(t.Context(), l); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectiveWatchFeedback(t *testing.T) {
	x := newFixture(t)
	now := time.Now().UTC()
	x.m.d.Now = func() time.Time { return now }
	x.f.pr.UpdatedAt = now
	x.link(t)
	x.watch(t)
	now = now.Add(2 * time.Minute)
	x.f.checks.State = forge.CIStateSuccess
	x.m.sweep(t.Context())
	if x.f.feedbackReads != 1 || x.f.userReads != 1 || x.f.checkReads != 2 || len(x.sent) != 1 {
		t.Fatalf("unchanged metadata reread comments or missed CI: %+v, sent=%v", x.f, x.sent)
	}
	// Provider updatedAt can miss edits. The periodic safety read catches them.
	x.f.remarks = []forge.Remark{{ID: "review", Author: "other", Body: "edited"}}
	now = now.Add(8 * time.Minute)
	x.m.sweep(t.Context())
	if x.f.feedbackReads != 2 || len(x.sent) != 2 {
		t.Fatal("periodic feedback read missed edit")
	}
	x.f.pr.UpdatedAt = now.Add(time.Minute)
	x.f.remarks[0].Body = "edited again"
	x.m.sweep(t.Context())
	if x.f.feedbackReads != 3 || len(x.sent) != 3 {
		t.Fatal("changed metadata did not refresh comments")
	}
	x.f.pr.HeadSHA = "new"
	x.m.sweep(t.Context())
	if x.f.feedbackReads != 4 {
		t.Fatal("new head did not refresh comments")
	}
	// Restarting preserves the feedback watermark, without replaying comments.
	x.m = New(x.m.d)
	x.m.sweep(t.Context())
	if x.f.feedbackReads != 4 || len(x.sent) != 4 {
		t.Fatal("restart lost feedback watermark", x.sent)
	}
}

func TestQuietWatchSchedule(t *testing.T) {
	x := newFixture(t)
	now := time.Now().UTC()
	x.m.d.Now = func() time.Time { return now }
	x.f.pr.UpdatedAt = now.Add(-time.Hour)
	mergeable := true
	x.f.pr.Mergeable = &mergeable
	x.f.checks = forge.CIStatus{State: forge.CIStateSuccess}
	x.link(t)
	x.watch(t)
	x.m.sweep(t.Context())
	if x.only(t).NextAt != now.Add(5*time.Minute).UnixMilli() {
		t.Fatal("quiet PR did not slow down")
	}
	reads := x.f.reads
	now = now.Add(2 * time.Minute)
	x.m.sweep(t.Context())
	if x.f.reads != reads {
		t.Fatal("ignored quiet deadline")
	}
	now = now.Add(3 * time.Minute)
	x.f.checks = forge.CIStatus{State: forge.CIStateFailure, Checks: []forge.Check{{Name: "still running", State: forge.CIStatePending}}}
	x.m.sweep(t.Context())
	if x.only(t).NextAt != 0 {
		t.Fatal("running checks slowed down behind a failure")
	}
	x.f.pr.Status = "merged"
	x.f.feedbackErr, x.f.checkErr = errors.New("must not read"), errors.New("must not read")
	x.m.sweep(t.Context())
	if x.only(t).Watching {
		t.Fatal("unavailable checks prevented a merged watch from stopping")
	}
}

func TestFeedbackReadPlan(t *testing.T) {
	now := time.Now()
	pr := forge.PR{HeadSHA: "head", UpdatedAt: now}
	previous := Snapshot{SHA: "head", UpdatedAt: now, RemarksReadAt: now}
	if feedbackDue(&previous, pr, now) {
		t.Fatal("fresh feedback is due")
	}
	for _, snapshot := range []*Snapshot{nil, {}, {SHA: "other"}, {SHA: "head", UpdatedAt: now}, {SHA: "head", UpdatedAt: now, RemarksReadAt: now.Add(time.Hour)}} {
		if !feedbackDue(snapshot, pr, now) {
			t.Fatalf("missing or invalid watermark accepted: %+v", snapshot)
		}
	}
	pr.UpdatedAt = time.Time{}
	if !feedbackDue(&previous, pr, now) || quietPR(pr, &Snapshot{}, now) {
		t.Fatal("missing timestamp treated as stable")
	}
}

func TestRateLimitDeadlinesAndOtherFailures(t *testing.T) {
	for _, stage := range []string{"metadata", "checks", "feedback", "viewer"} {
		t.Run(stage, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			x.watch(t)
			reset := time.Now().Add(time.Hour).Truncate(time.Millisecond)
			err := &forgehttp.ResponseError{Status: 429, RateLimit: forge.RateLimit{Limited: true, ResetAt: reset}}
			switch stage {
			case "metadata":
				x.f.err = err
			case "checks":
				x.f.checkErr = err
			case "feedback":
				x.f.feedbackErr = err
			case "viewer":
				x.f.userErr = err
			}
			x.m.sweep(t.Context())
			l := x.only(t)
			if !l.Watching || l.Failures != 0 || l.NextAt != reset.UnixMilli() {
				t.Fatalf("lost %s retry deadline: %+v", stage, l)
			}
		})
	}
	// Genuine failures still use the existing bounded failure policy.
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.f.err = errors.New("offline")
	x.m.sweep(t.Context())
	if x.only(t).Failures != 1 {
		t.Fatal("failure not counted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	x.m.sweep(ctx)
	if x.only(t).Failures != 1 {
		t.Fatal("cancellation counted as a failure")
	}
}

func TestWatchHostPauseAndRecovery(t *testing.T) {
	x := newFixture(t)
	now := time.Now().UTC()
	x.m.d.Now = func() time.Time { return now }
	x.link(t)
	x.watch(t)
	l := x.only(t)
	l.SessionID = "s2"
	if err := x.m.put(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	x.f.limited = true
	reads := x.f.reads
	x.m.sweep(t.Context())
	if x.f.reads != reads+1 {
		t.Fatal("same host was read again after a rate limit")
	}
	links, err := x.m.links(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.NextAt != now.Add(2*time.Minute).UnixMilli() || l.Failures != 0 || l.LastError == "" {
			t.Fatalf("host pause not shared: %+v", l)
		}
		l.NextAt = 0
		if err := x.m.put(t.Context(), l); err != nil {
			t.Fatal(err)
		}
	}
	x.m.sweep(t.Context())
	if x.f.reads != reads+1 {
		t.Fatal("next sweep lost the host pause")
	}
	now = now.Add(3 * time.Minute)
	x.f.limited = false
	x.m.sweep(t.Context())
	if x.f.reads != reads+2 || len(x.m.paused) != 0 || x.only(t).NextAt != 0 {
		t.Fatal("expired pause did not resume a shared read")
	}
}

func TestWatchReadOwnerScope(t *testing.T) {
	for _, scope := range []string{"same", "owner", "directory"} {
		t.Run(scope, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			x.watch(t)
			l := x.only(t)
			l.SessionID = "s2"
			want := 1
			if scope == "owner" {
				l.RemoteID = "another-owner"
				want = 2
			}
			if scope == "directory" {
				l.Directory = "/another-checkout"
				want = 2
			}
			if err := x.m.put(t.Context(), l); err != nil {
				t.Fatal(err)
			}
			reads := x.f.reads
			x.m.sweep(t.Context())
			if x.f.reads-reads != want {
				t.Fatalf("scope %s: got %d reads, want %d", scope, x.f.reads-reads, want)
			}
		})
	}
}

func TestSharedStatusReadKeepsEachFeedbackWatermark(t *testing.T) {
	x := newFixture(t)
	now := time.Now().UTC()
	x.m.d.Now = func() time.Time { return now }
	x.f.pr.UpdatedAt = now
	x.link(t)
	x.watch(t)
	l := x.only(t)
	l.SessionID = "s2"
	l.Snapshot.Remarks = map[string]string{"other": "newer baseline"}
	if err := x.m.put(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	x.m.sweep(t.Context())
	if len(x.sent) != 0 || x.f.feedbackReads != 1 {
		t.Fatal("shared status read replaced another watch's feedback baseline", x.sent)
	}
	links, err := x.m.links(t.Context(), "", "")
	if err != nil || len(links) != 2 || links[1].Snapshot.Remarks["other"] != "newer baseline" {
		t.Fatal("feedback baseline was not preserved", links, err)
	}
}

func TestQuietReadDeadlineDoesNotDelayPendingDelivery(t *testing.T) {
	x := newFixture(t)
	now := time.Now().UTC()
	x.m.d.Now = func() time.Time { return now }
	x.f.pr.UpdatedAt = now.Add(-time.Hour)
	mergeable := true
	x.f.pr.Mergeable = &mergeable
	x.link(t)
	x.watch(t)
	x.f.checks.State = forge.CIStateSuccess
	x.sendErr = errors.New("queue offline")
	x.m.sweep(t.Context())
	if x.only(t).Pending == "" {
		t.Fatal("missing pending update")
	}
	reads := x.f.reads
	now = now.Add(2 * time.Minute)
	x.sendErr = nil
	x.m.sweep(t.Context())
	if len(x.sent) != 1 || x.only(t).Pending != "" || x.f.reads != reads {
		t.Fatal("quiet metadata deadline delayed outbox delivery")
	}
}
