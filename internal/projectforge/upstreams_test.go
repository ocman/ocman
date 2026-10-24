package projectforge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func TestDetectUpstreams_CachesByOwnerAndDirectory(t *testing.T) {
	now := time.Unix(100, 0)
	m := newTestModule()
	m.upstreams.now = func() time.Time { return now }
	a := &fakeHost{upstreams: githubUpstreams("/repo"), remoteID: "rem1"}
	b := &fakeHost{upstreams: githubUpstreams("/repo"), remoteID: "rem2"}

	for range 2 {
		if _, _, err := m.detectUpstreams(context.Background(), a, "/repo"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.detectUpstreams(context.Background(), b, "/repo"); err != nil {
		t.Fatal(err)
	}
	if a.upstreamCalls != 1 || b.upstreamCalls != 1 {
		t.Fatalf("calls before expiry = %d, %d; want 1, 1", a.upstreamCalls, b.upstreamCalls)
	}

	now = now.Add(upstreamsTTL + time.Millisecond)
	if _, _, err := m.detectUpstreams(context.Background(), a, "/repo"); err != nil {
		t.Fatal(err)
	}
	if a.upstreamCalls != 2 {
		t.Fatalf("calls after expiry = %d, want 2", a.upstreamCalls)
	}
}

func TestDetectUpstreams_TTLStartsAfterDetection(t *testing.T) {
	now := time.Unix(100, 0)
	m := newTestModule()
	m.upstreams.now = func() time.Time { return now }
	host := &fakeHost{
		upstreams: githubUpstreams("/repo"), remoteID: "rem1",
		onUpstream: func() { now = now.Add(upstreamsTTL) },
	}
	for range 2 {
		if _, _, err := m.detectUpstreams(context.Background(), host, "/repo"); err != nil {
			t.Fatal(err)
		}
	}
	if host.upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want cached result after slow detection", host.upstreamCalls)
	}
}

type cancelAwareUpstreamHost struct {
	hostsvc.Host
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type boundedUpstreamHost struct {
	hostsvc.Host
	mu      sync.Mutex
	active  int
	max     int
	calls   int
	started chan struct{}
	release chan struct{}
}

func (h *boundedUpstreamHost) RemoteID() string { return "rem1" }
func (h *boundedUpstreamHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	h.mu.Lock()
	h.active++
	h.calls++
	if h.active > h.max {
		h.max = h.active
	}
	h.mu.Unlock()
	h.started <- struct{}{}
	<-h.release
	h.mu.Lock()
	h.active--
	h.mu.Unlock()
	return githubUpstreams("/repo"), nil
}

func TestDetectUpstreams_CanceledRequestsDoNotQueueDetachedWork(t *testing.T) {
	const limit = 8
	m := newTestModule()
	host := &boundedUpstreamHost{started: make(chan struct{}, 64), release: make(chan struct{})}
	done := make(chan error, limit)
	for i := range limit {
		go func() {
			_, _, err := m.detectUpstreams(context.Background(), host, fmt.Sprintf("/active/%d", i))
			done <- err
		}()
	}
	for range limit {
		<-host.started
	}
	canceledDone := make(chan error, 20)
	cancels := make([]context.CancelFunc, 0, 20)
	for i := range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		go func() {
			_, _, err := m.detectUpstreams(ctx, host, fmt.Sprintf("/canceled/%d", i))
			canceledDone <- err
		}()
	}
	time.Sleep(20 * time.Millisecond)
	for _, cancel := range cancels {
		cancel()
	}
	for range 20 {
		if err := <-canceledDone; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request error = %v", err)
		}
	}
	close(host.release)
	for range limit {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.calls != limit {
		t.Fatalf("owner calls = %d, want %d", host.calls, limit)
	}
}

func TestDetectUpstreams_QueuedCreatorCancellationKeepsLiveWaiter(t *testing.T) {
	const limit = 8
	m := newTestModule()
	host := &boundedUpstreamHost{started: make(chan struct{}, 16), release: make(chan struct{})}
	activeDone := make(chan error, limit)
	for i := range limit {
		go func() {
			_, _, err := m.detectUpstreams(context.Background(), host, fmt.Sprintf("/active/%d", i))
			activeDone <- err
		}()
	}
	for range limit {
		<-host.started
	}
	creatorCtx, cancelCreator := context.WithCancel(context.Background())
	creatorDone := make(chan error, 1)
	go func() {
		_, _, err := m.detectUpstreams(creatorCtx, host, "/shared")
		creatorDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	waiterDone := make(chan error, 1)
	go func() {
		_, _, err := m.detectUpstreams(context.Background(), host, "/shared")
		waiterDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancelCreator()
	if err := <-creatorDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("creator error = %v", err)
	}
	close(host.release)
	for range limit {
		if err := <-activeDone; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-waiterDone; err != nil {
		t.Fatalf("live waiter error = %v", err)
	}
}

func TestDetectUpstreams_NewRequestReplacesAbandonedPendingWork(t *testing.T) {
	const limit = 8
	m := newTestModule()
	host := &boundedUpstreamHost{started: make(chan struct{}, 16), release: make(chan struct{})}
	activeDone := make(chan error, limit)
	for i := range limit {
		go func() {
			_, _, err := m.detectUpstreams(context.Background(), host, fmt.Sprintf("/active/%d", i))
			activeDone <- err
		}()
	}
	for range limit {
		<-host.started
	}
	abandonedCtx, cancel := context.WithCancel(context.Background())
	abandonedDone := make(chan error, 1)
	go func() {
		_, _, err := m.detectUpstreams(abandonedCtx, host, "/shared")
		abandonedDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-abandonedDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned error = %v", err)
	}
	replacementDone := make(chan error, 1)
	go func() {
		_, _, err := m.detectUpstreams(context.Background(), host, "/shared")
		replacementDone <- err
	}()
	close(host.release)
	for range limit {
		if err := <-activeDone; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-replacementDone; err != nil {
		t.Fatalf("replacement error = %v", err)
	}
}

func TestDetectUpstreams_BoundsConcurrentOwners(t *testing.T) {
	const limit = 8
	m := newTestModule()
	host := &boundedUpstreamHost{started: make(chan struct{}, limit+1), release: make(chan struct{})}
	done := make(chan error, limit+1)
	for i := range limit + 1 {
		go func() {
			_, _, err := m.detectUpstreams(context.Background(), host, fmt.Sprintf("/repo/%d", i))
			done <- err
		}()
	}
	for range limit {
		<-host.started
	}
	select {
	case <-host.started:
		t.Fatal("more than the configured upstream detections started")
	case <-time.After(20 * time.Millisecond):
	}
	close(host.release)
	for range limit + 1 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.max != limit {
		t.Fatalf("max concurrency = %d, want %d", host.max, limit)
	}
}

func TestDetectUpstreams_DuplicateWaitersDoNotBlockOtherProjects(t *testing.T) {
	m := newTestModule()
	host := &boundedUpstreamHost{started: make(chan struct{}, 2), release: make(chan struct{})}
	done := make(chan error, 9)
	for range 8 {
		go func() {
			_, _, err := m.detectUpstreams(context.Background(), host, "/same")
			done <- err
		}()
	}
	<-host.started
	go func() {
		_, _, err := m.detectUpstreams(context.Background(), host, "/other")
		done <- err
	}()
	select {
	case <-host.started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("duplicate waiters blocked an unrelated project")
	}
	close(host.release)
	for range 9 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func (h *cancelAwareUpstreamHost) RemoteID() string { return "rem1" }
func (h *cancelAwareUpstreamHost) ProjectUpstreams(ctx context.Context, _ string) (*hostsvc.ProjectUpstreams, error) {
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
		return githubUpstreams("/repo"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDetectUpstreams_CallerCancellationDoesNotFailOtherWaiters(t *testing.T) {
	m := newTestModule()
	host := &cancelAwareUpstreamHost{started: make(chan struct{}), release: make(chan struct{})}
	firstCtx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() {
		_, _, err := m.detectUpstreams(firstCtx, host, "/repo")
		first <- err
	}()
	<-host.started
	go func() {
		_, _, err := m.detectUpstreams(context.Background(), host, "/repo")
		second <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first error = %v, want context canceled", err)
	}
	close(host.release)
	if err := <-second; err != nil {
		t.Fatalf("second error = %v, want success", err)
	}
}

func TestHandleUpstreams_ServesClassifiedRemotes(t *testing.T) {
	host := &fakeHost{upstreams: &hostsvc.ProjectUpstreams{RepoRoot: "/repo", Remotes: []forge.Remote{
		{Name: "origin", Host: "github.com", Repo: "a/b", Type: forge.RemoteTypeGitHub},
		{Name: "internal", Host: "code.example.com", Repo: "infra/x", Type: forge.RemoteTypeForgejo},
		{Name: "gitlab", Host: "gitlab.com", Repo: "x/y", Type: forge.RemoteTypeForgejo},
	}}}
	m := newTestModule()
	m.KnowsForgejo = func(h string) bool { return h == "code.example.com" }
	m.Owner = func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return host, true }
	serve := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		m.handleUpstreams(rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/repo&remoteId=local", nil))
		return rr
	}
	rr := serve()
	var body struct{ Upstreams []forge.Remote }
	if err := json.Unmarshal(rr.Body.Bytes(), &body); rr.Code != http.StatusOK || err != nil {
		t.Fatalf("status = %d, body = %s, err = %v", rr.Code, rr.Body, err)
	}
	if len(body.Upstreams) != 2 || body.Upstreams[0].Name != "origin" || body.Upstreams[1].Name != "internal" {
		t.Fatalf("upstreams = %+v", body.Upstreams)
	}
	if r, ok := findRemote(body.Upstreams, "internal"); !ok || r.Type != forge.RemoteTypeForgejo {
		t.Fatalf("findRemote(internal) = %+v, %v", r, ok)
	}
	if _, ok := findRemote(body.Upstreams, "gitlab"); ok {
		t.Fatal("findRemote found an unserved remote")
	}

	m = newTestModule()
	m.Owner = func(http.ResponseWriter, string, string) (hostsvc.Host, bool) {
		return &fakeHost{upstreamErr: git.ErrNotARepo}, true
	}
	if rr := serve(); rr.Code != http.StatusNotFound {
		t.Fatalf("not a repo: status = %d", rr.Code)
	}
}
