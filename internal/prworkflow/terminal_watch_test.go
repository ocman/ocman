package prworkflow

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/state"
)

func TestTerminalWatchStopsWithoutSessionReadiness(t *testing.T) {
	for _, status := range []string{"merged", "closed"} {
		for _, pending := range []bool{false, true} {
			t.Run(status+map[bool]string{false: "/watching", true: "/pending"}[pending], func(t *testing.T) {
				x := newFixture(t)
				x.link(t)
				x.watch(t)
				if pending {
					x.f.checks.State = forge.CIStateSuccess
					x.sendErr = errors.New("queue unavailable")
					x.m.sweep(t.Context())
					if x.only(t).Pending == "" {
						t.Fatal("missing pending update")
					}
				}
				x.ready = false
				x.f.pr.Status = status
				x.m.sweep(t.Context())
				if l := x.only(t); l.Watching || l.PR.Status != status {
					t.Fatalf("terminal PR still watched while session unavailable: %+v", l)
				}
				counts, err := BackgroundCounts(t.Context(), x.m.d.DB)
				if err != nil || counts[state.Key{Platform: "r-box:opencode", SessionID: "s1"}] != 0 {
					t.Fatalf("terminal PR keeps session in progress: counts=%v err=%v", counts, err)
				}
				if len(x.sent) != 0 {
					t.Fatal("unready session received notification")
				}
				x.ready, x.sendErr = true, nil
				x.m.sweep(t.Context())
				if len(x.sent) != 1 || x.only(t).Pending != "" {
					t.Fatal("terminal notification not delivered once session ready")
				}
			})
		}
	}
}

func TestPendingWatchDeliverySurvivesForgeFailures(t *testing.T) {
	for _, failure := range []string{"read", "rate-limit", "paused"} {
		t.Run(failure, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			x.watch(t)
			x.ready = false
			x.f.checks.State = forge.CIStateSuccess
			x.m.sweep(t.Context())
			if x.only(t).Pending == "" {
				t.Fatal("missing pending update")
			}
			switch failure {
			case "read":
				x.f.err = errors.New("forge unavailable")
			case "rate-limit":
				x.f.limited = true
			case "paused":
				x.m.paused["github/github.com"] = x.m.now().Add(time.Hour)
			}
			x.ready = true
			x.m.sweep(t.Context())
			if len(x.sent) != 1 || x.only(t).Pending != "" {
				t.Fatal("forge failure prevented pending delivery")
			}
		})
	}
}

func TestWatchRetainsUpdatesWhileSessionUnavailable(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.ready = false
	x.f.checks = forge.CIStatus{State: forge.CIStateFailure, Checks: []forge.Check{{Name: "tests", State: forge.CIStateFailure}}}
	x.m.sweep(t.Context())
	if !strings.Contains(x.only(t).Pending, "Failed checks: tests") {
		t.Fatal("failed check update not retained")
	}
	x.f.remarks = []forge.Remark{{ID: "review", Author: "other", Body: "Please fix"}}
	x.m.sweep(t.Context())
	if l := x.only(t); !l.Watching || !strings.Contains(l.Pending, "Failed checks: tests") || !strings.Contains(l.Pending, "new or edited") {
		t.Fatal("polling overwrote pending update", l)
	}
	x.ready = true
	x.m.sweep(t.Context())
	if len(x.sent) != 1 || x.only(t).Pending != "" {
		t.Fatal("pending updates not delivered together once ready")
	}
}
