package owned

import (
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestRunningCountUsesOnlyScopedSidebarObservations(t *testing.T) {
	a, p, _ := storedFixture(t)
	a.native = &sidebarNative{nativePlatform: p, t: t}
	worker := &sidebarWorker{adapter: a}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close(t.Context()) })
	if err := worker.snapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n, err := a.RunningSessionCount(t.Context(), platforms.SidebarQuery{}); err != nil || n != 1 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	if _, err := a.RunningSessionCountFor(t.Context(), []platforms.SidebarSource{{Owner: "other"}}, platforms.SidebarQuery{}); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("cross-owner count: %v", err)
	}
	if n, err := a.RunningSessionCountFor(t.Context(), nil, platforms.SidebarQuery{}); err != nil || n != 0 {
		t.Fatalf("empty group=%d %v", n, err)
	}
	second, err := New(t.Context(), &nativePlatform{id: "second", source: "second-source"}, a.store, a.history, a.owner)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, second, db.Session{ID: "second-native", Title: "second", Directory: "/second"})
	firstSource, err := a.SidebarSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := second.SidebarSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.sidebarReader.Close() })
	if err := a.history.ReplaceSidebarObservations(t.Context(), second.observationScope(), second.sidebarGeneration, time.Now().Add(time.Hour).UnixMilli(), map[string]statev2.SidebarObservation{"second-native": {Status: "busy"}}); err != nil {
		t.Fatal(err)
	}
	if n, err := a.RunningSessionCountFor(t.Context(), []platforms.SidebarSource{firstSource, secondSource}, platforms.SidebarQuery{}); err != nil || n != 2 {
		t.Fatalf("combined local platforms=%d %v", n, err)
	}
	if n, err := a.RunningSessionCount(t.Context(), platforms.SidebarQuery{}); err != nil || n != 1 {
		t.Fatalf("single platform borrowed another source: %d %v", n, err)
	}
}
