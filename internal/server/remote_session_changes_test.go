package server

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRemoteSessionChangesFilterForeignAndMalformedEventsAndDrainCoalescing(t *testing.T) {
	s := testServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	attached, release := make(chan struct{}), make(chan struct{})
	seen := make(chan string, 40)
	done := make(chan error, 1)
	go func() {
		done <- s.StreamRemoteSessionChanges(ctx, func(platform, id string) error {
			if platform == "" && id == "" {
				close(attached)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			}
			seen <- platform + "/" + id
			return nil
		})
	}()
	<-attached
	for _, data := range []string{`not json`, `{}`, `{"platform":"fake"}`, `{"sessionID":"s1"}`, `{"platform":"r-other:fake","sessionID":"s1"}`} {
		s.broadcastGlobalEvent("ocman.session.changed", []byte(data))
	}
	s.broadcastGlobalEvent("ocman.session.activity", []byte(`{"platform":"fake","sessionID":"ignored"}`))
	for i := range 40 {
		s.broadcastSessionPatch("fake", fmt.Sprintf("s%d", i), nil)
	}
	close(release)
	ids := map[string]bool{}
	for range 40 {
		select {
		case id := <-seen:
			ids[id] = true
		case <-ctx.Done():
			t.Fatal("coalesced invalidations were lost")
		}
	}
	if len(ids) != 40 {
		t.Fatalf("invalidations=%v", ids)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.broadcastHub.subscriberCount() != 0 {
		t.Fatal("remote stream subscription leaked")
	}
}

func TestRemoteSessionChangesPropagateSendFailures(t *testing.T) {
	for _, failInitial := range []bool{true, false} {
		t.Run(fmt.Sprint(failInitial), func(t *testing.T) {
			s := testServer(t)
			want := errors.New("send failed")
			err := s.StreamRemoteSessionChanges(t.Context(), func(platform, _ string) error {
				if platform == "" && !failInitial {
					s.broadcastSessionPatch("fake", "s1", nil)
					return nil
				}
				return want
			})
			if !errors.Is(err, want) || s.broadcastHub.subscriberCount() != 0 {
				t.Fatalf("err=%v subscriptions=%d", err, s.broadcastHub.subscriberCount())
			}
		})
	}
}
