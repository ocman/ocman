package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestSessionSearchBackfillBatchesAndRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var calls []time.Duration
		runSessionSearchBackfill(t.Context(), func(context.Context) (bool, error) {
			calls = append(calls, time.Since(start))
			switch len(calls) {
			case 1:
				return false, nil
			case 2:
				return false, errors.New("temporary write failure")
			default:
				return true, nil
			}
		})
		want := []time.Duration{0, 50 * time.Millisecond, 10*time.Second + 50*time.Millisecond}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("batch calls at %v, want %v", calls, want)
		}
	})
}

func TestSessionSearchBackfillCancelsDuringWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := time.Now()
		calls := 0
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			runSessionSearchBackfill(ctx, func(context.Context) (bool, error) {
				calls++
				return false, nil
			})
		}()
		synctest.Wait()
		cancel()
		<-exited
		if elapsed := time.Since(start); calls != 1 || elapsed != 0 {
			t.Fatalf("cancellation took %v with %d batch calls; want immediate exit after one call", elapsed, calls)
		}
	})
}
