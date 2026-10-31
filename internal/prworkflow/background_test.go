package prworkflow

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestWatchBackgroundCountsFollowDurableLifecycle(t *testing.T) {
	x := newFixture(t)
	key := state.Key{Platform: "r-box:opencode", SessionID: "s1"}
	check := func(want int) {
		t.Helper()
		counts, err := BackgroundCounts(t.Context(), x.m.d.DB)
		if err != nil || counts[key] != want {
			t.Fatalf("counts=%v err=%v want=%d", counts, err, want)
		}
	}
	x.link(t)
	check(0)
	x.watch(t)
	check(1)
	// Multiple watched PRs count separately; unrelated sessions stay separate.
	other := x.only(t)
	other.Number++
	if err := x.m.put(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	check(2)
	if err := x.m.remove(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	x.m = New(x.m.d)
	check(1)
	x.f.pr.Status = "merged"
	x.sendErr = errors.New("queue unavailable")
	x.m.sweep(t.Context())
	if l := x.only(t); l.Watching || l.Pending == "" {
		t.Fatalf("terminal notice not pending: %+v", l)
	}
	check(1)
	x.sendErr = nil
	x.m.sweep(t.Context())
	check(0)
}

func TestWatchBackgroundCountsStopAndUnlink(t *testing.T) {
	for _, action := range []string{"unwatch", "unlink"} {
		t.Run(action, func(t *testing.T) {
			x := newFixture(t)
			x.link(t)
			x.watch(t)
			status := 200
			if action == "unlink" {
				status = 204
			}
			x.request(t, "pull-requests", `{"action":"`+action+`","host":"github.com","repo":"team/repo","number":42}`, status)
			counts, err := BackgroundCounts(t.Context(), x.m.d.DB)
			if err != nil || len(counts) != 0 {
				t.Fatalf("counts=%v err=%v", counts, err)
			}
		})
	}
}

func TestWatchBackgroundCountsSurviveReadFailuresUntilWatchStops(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.watch(t)
	x.f.err = errors.New("forge unavailable")
	for attempt := 1; attempt <= 8; attempt++ {
		l := x.only(t)
		l.NextAt = 0
		if err := x.m.put(t.Context(), l); err != nil {
			t.Fatal(err)
		}
		x.m.sweep(t.Context())
		counts, err := BackgroundCounts(t.Context(), x.m.d.DB)
		want := 1
		if attempt == 8 {
			want = 0
		}
		if err != nil || counts[state.Key{Platform: l.Platform, SessionID: l.SessionID}] != want {
			t.Fatalf("attempt=%d counts=%v err=%v", attempt, counts, err)
		}
	}
}

func TestWatchBackgroundCountsUnavailableStorage(t *testing.T) {
	counts, err := BackgroundCounts(t.Context(), nil)
	if err != nil || len(counts) != 0 {
		t.Fatal(counts, err)
	}
	x := newFixture(t)
	if err := x.m.d.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := BackgroundCounts(t.Context(), x.m.d.DB); err == nil {
		t.Fatal("closed storage accepted")
	}
}
