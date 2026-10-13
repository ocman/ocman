package notifications

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoveClearsDeliveriesWithForeignKeysOff(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	sub := subscription(t)
	if _, err := m.store.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := m.store.enqueue(ctx, Candidate{Key: "old-alert"}); err != nil {
		t.Fatal(err)
	}
	if err := m.store.remove(ctx, sub.Endpoint); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := m.store.db.QueryRow(`SELECT count(*) FROM push_delivery`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("orphan deliveries retained with foreign keys off")
	}
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	rows, err := m.store.due(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("registration revived old alerts")
	}
}

func TestRemoveRollsBackWhenSubscriptionDeleteFails(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	sub := subscription(t)
	if err := m.store.save(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := m.store.enqueue(ctx, Candidate{Key: "owed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.db.Exec(`CREATE TRIGGER reject_unsubscribe BEFORE DELETE ON push_subscription BEGIN SELECT RAISE(ABORT,'delete failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.store.remove(ctx, sub.Endpoint); err == nil {
		t.Fatal("subscription deletion failure hidden")
	}
	n, err := m.store.count(ctx)
	if err != nil || n != 1 {
		t.Fatal("failed removal deleted subscription")
	}
	rows, err := m.store.due(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal("failed removal lost owed alert")
	}
}

func TestSnapshotsContinueDuringStalledDelivery(t *testing.T) {
	m := fixture(t)
	sub := subscription(t)
	if err := m.store.save(t.Context(), sub); err != nil {
		t.Fatal(err)
	}
	if err := m.store.enqueue(t.Context(), Candidate{Key: "pending"}); err != nil {
		t.Fatal(err)
	}
	var snapshots atomic.Int32
	m.d.Snapshot = func(context.Context) ([]Candidate, error) { snapshots.Add(1); return nil, nil }
	entered := make(chan struct{})
	m.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		close(entered)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); m.run(ctx, 10*time.Millisecond) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	time.Sleep(50 * time.Millisecond)
	if snapshots.Load() < 2 {
		t.Error("stalled delivery prevented snapshot observation")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not join on cancellation")
	}
}

func TestFailedInitialSnapshotRecoveryDoesNotReplayHistory(t *testing.T) {
	m := fixture(t)
	ctx := t.Context()
	if err := m.store.save(ctx, subscription(t)); err != nil {
		t.Fatal(err)
	}
	m.d.Snapshot = func(context.Context) ([]Candidate, error) { return nil, errors.New("read failed") }
	if err := m.tick(ctx); err == nil {
		t.Fatal("incomplete snapshot succeeded")
	}
	m.d.Snapshot = func(context.Context) ([]Candidate, error) { return []Candidate{{Key: "historical"}}, nil }
	if err := m.tick(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := m.store.due(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("historical alert enqueued on recovery")
	}
}
