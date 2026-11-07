package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSnapshotsRecoveryInvalidationAndEpoch(t *testing.T) {
	now := time.Now()
	c := httpSnapshots{now: func() time.Time { return now }}
	reads := 0
	read := func() (any, error) { reads++; return []string{"row"}, nil }
	_, first, err := c.get(t.Context(), "scope", read)
	if err != nil {
		t.Fatal(err)
	}
	_, same, _ := c.get(t.Context(), "scope", read)
	if reads != 1 || same != first {
		t.Fatalf("read count %d, versions %q %q", reads, first, same)
	}
	now = now.Add(snapshotRecovery)
	_, recovered, _ := c.get(t.Context(), "scope", read)
	if reads != 2 || recovered != first {
		t.Fatal("recovery must reread but retain unchanged content version")
	}
	c.invalidate()
	_, _, _ = c.get(t.Context(), "scope", read)
	if reads != 3 {
		t.Fatal("invalidation did not reread")
	}
	other := httpSnapshots{}
	_, restarted, _ := other.get(t.Context(), "scope", read)
	if first == restarted {
		t.Fatal("server epoch was reused")
	}
}

func TestHTTPSnapshotAgeTracksAuthoritativeReadNotLastConsumer(t *testing.T) {
	var c httpSnapshots
	_, tag, err := c.get(t.Context(), "scope", func() (any, error) { return "value", nil })
	if err != nil {
		t.Fatal(err)
	}
	c.entries["scope"].at = time.Now().Add(-4 * time.Second)
	if age := c.age("scope", tag); age < 4*time.Second || age > 5*time.Second {
		t.Fatal(age)
	}
	if c.age("scope", "other") != 0 || c.age("missing", tag) != 0 {
		t.Fatal("age crossed snapshot identity")
	}
}

func TestHTTPSnapshotsConcurrentReadersAndCancellation(t *testing.T) {
	var c httpSnapshots
	started, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	read := func() (any, error) { reads.Add(1); close(started); <-release; return "row", nil }
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _, err := c.get(t.Context(), "scope", read)
		if err != nil {
			t.Error(err)
		}
	})
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := c.get(ctx, "scope", read); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	for range 20 {
		wg.Go(func() {
			if _, _, err := c.get(t.Context(), "scope", read); err != nil {
				t.Error(err)
			}
		})
	}
	close(release)
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("read count %d", reads.Load())
	}
}

func TestHTTPSnapshotsDoNotPublishInvalidatedOrFailedReads(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var c httpSnapshots
		_, tag, err := c.get(t.Context(), "scope", func() (any, error) {
			c.invalidate()
			if fail {
				return nil, errors.New("owner unavailable")
			}
			return "old", nil
		})
		if tag != "" || (err != nil) != fail || len(c.entries) != 0 {
			t.Fatalf("published invalidated response %q, %v", tag, err)
		}
		body, tag, err := c.get(t.Context(), "scope", func() (any, error) { return "new", nil })
		if err != nil || tag == "" || string(body) != "\"new\"\n" {
			t.Fatal("retry did not recover")
		}
	}
}

func TestHTTPSnapshotsBoundRetentionAndSeparateScopes(t *testing.T) {
	var c httpSnapshots
	for i := range snapshotEntryLimit * 2 {
		key := fmt.Sprint(i)
		if _, _, err := c.get(t.Context(), key, func() (any, error) { return key, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.entries) != snapshotEntryLimit || c.bytes > snapshotByteLimit {
		t.Fatalf("unbounded cache: %d, %d", len(c.entries), c.bytes)
	}
}

func TestHTTPSnapshotsPanicAndSaturationDoNotStrandReaders(t *testing.T) {
	var c httpSnapshots
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()
		_, _, _ = c.get(t.Context(), "panic", func() (any, error) { panic("read failure") })
	}()
	if len(c.entries) != 0 {
		t.Fatal("panicked read retained a loading slot")
	}
	for i := range snapshotEntryLimit {
		c.entries[fmt.Sprint(i)] = &httpSnapshot{done: make(chan struct{})}
	}
	body, tag, err := c.get(t.Context(), "overflow", func() (any, error) { return "uncached", nil })
	if err != nil || tag != "" || string(body) != "\"uncached\"\n" {
		t.Fatalf("saturated response %s, %q, %v", body, tag, err)
	}
}

func TestSnapshotMutationFenceAndEventInvalidations(t *testing.T) {
	for _, event := range []string{"ocman.session.changed", "ocman.permission.resolved", "ocman.question.resolved", "ocman.projects.changed", "ocman.settings.changed", "ocman.inbox.changed"} {
		s := &Server{}
		s.broadcastGlobalEvent(event, nil)
		if s.sessionSnapshots.version != 1 {
			t.Errorf("%s did not invalidate", event)
		}
	}
	s := &Server{}
	s.broadcastGlobalEvent("ocman.session.activity", nil)
	if s.sessionSnapshots.version != 0 {
		t.Fatal("per-token activity invalidated snapshots")
	}
	h := s.invalidateSessionWrites(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		if s.sessionSnapshots.version != 0 {
			t.Error("unconfirmed mutation invalidated the cache")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/project/archive", nil))
	if s.sessionSnapshots.version != 1 {
		t.Fatal("missing completion fence")
	}
	denied := s.invalidateSessionWrites(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	denied.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/project/archive", nil))
	if s.sessionSnapshots.version != 1 {
		t.Fatal("unauthenticated request flushed snapshots")
	}
}

func TestSidebarSinceExplicitAndRelativeWindows(t *testing.T) {
	for _, query := range []string{"since=42&sinceHours=72", "since=42"} {
		if got := sidebarSince(httptest.NewRequest(http.MethodGet, "/?"+query, nil)); got != 42 {
			t.Fatal(got)
		}
	}
	for _, query := range []string{"", "sinceHours=-1", "sinceHours=999999999999"} {
		if got := sidebarSince(httptest.NewRequest(http.MethodGet, "/?"+query, nil)); got != 0 {
			t.Fatal(got)
		}
	}
	got := sidebarSince(httptest.NewRequest(http.MethodGet, "/?sinceHours=72", nil))
	if delta := time.Since(time.UnixMilli(got)) - 72*time.Hour; delta < 0 || delta > time.Second {
		t.Fatal(delta)
	}
}

func BenchmarkSharedHTTPSnapshot(b *testing.B) {
	rows := make([]string, 500)
	for i := range rows {
		rows[i] = fmt.Sprintf("session-%d", i)
	}
	for _, shared := range []bool{false, true} {
		b.Run(fmt.Sprint(shared), func(b *testing.B) {
			var c httpSnapshots
			b.ReportAllocs()
			for range b.N {
				if !shared {
					c.invalidate()
				}
				_, _, _ = c.get(b.Context(), "scope", func() (any, error) { return rows, nil })
			}
		})
	}
}
