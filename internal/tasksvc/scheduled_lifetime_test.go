package tasksvc

import (
	"context"
	"testing"
)

func TestScheduledObservationPreservesCallerLifetimeForSubscriptions(t *testing.T) {
	m, _ := testManager(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var subscription context.Context
	if err := m.ObserveSchedule(ctx, exampleSchedule, func(ctx context.Context) error { subscription = ctx; return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-subscription.Done():
		t.Fatal("finishing a scheduler check cancelled its long-lived subscriptions")
	default:
	}
	cancel()
	<-subscription.Done()
}
