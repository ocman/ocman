package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func TestReadsShareCachedResponses(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"number":1,"title":"Cached","state":"open"}`)
	}))
	defer srv.Close()
	for range 2 {
		client := NewForTest(srv.URL, "same-token", srv.Client())
		if _, err := client.LookupPR(WithCache(context.Background()), "o/r", 1); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want one shared read", calls.Load())
	}
}

func TestReadsStopAtHalfReportedQuota(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("X-RateLimit-Limit", "4")
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(4-int(n)))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		fmt.Fprint(w, `{"number":1,"state":"open"}`)
	}))
	defer srv.Close()
	client := NewForTest(srv.URL, "same-token", srv.Client())
	for number := 1; number <= 2; number++ {
		if _, err := client.LookupPR(WithCache(context.Background()), "o/r", number); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.LookupPR(WithCache(context.Background()), "o/r", 3); err == nil {
		t.Fatal("uncached read exceeded half the GitHub quota")
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want half the quota (2)", calls.Load())
	}
}

func TestRefreshCannotBypassHalfQuota(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		w.Header().Set("X-RateLimit-Limit", "4")
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(4-int(n)))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		fmt.Fprintf(w, `{"number":1,"title":"Version %d","state":"open"}`, n)
	}))
	defer srv.Close()
	c := NewForTest(srv.URL, "token", srv.Client())
	for _, want := range []struct {
		refresh, stale bool
		title          string
	}{
		{false, false, "Version 1"}, {false, false, "Version 1"},
		{true, false, "Version 2"}, {true, true, "Version 2"},
	} {
		ctx := WithCache(context.Background())
		if want.refresh {
			ctx = WithRefresh(ctx)
		}
		body, rl, status, err := c.fetch(ctx, "/repos/o/r/pulls/1")
		if err != nil || status != 200 || rl.Stale != want.stale || !strings.Contains(string(body), want.title) {
			t.Fatalf("refresh %v: body=%s quota=%+v status=%d err=%v", want.refresh, body, rl, status, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("refresh spent %d calls, want 2", calls.Load())
	}
	_, rl, _, err := c.fetch(WithCache(context.Background()), "/uncached")
	if !rl.Limited || rl.ResetAt.IsZero() || err != nil {
		t.Fatalf("uncached quota=%+v err=%v", rl, err)
	}
}

func TestAnonymousBudgetAndCredentialIsolation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	anon := NewForTest(srv.URL, "", srv.Client())
	for i := range 30 {
		if _, _, status, err := anon.fetch(WithCache(context.Background()), fmt.Sprintf("/read/%d", i)); err != nil || status != 200 {
			t.Fatalf("read %d: %d %v", i, status, err)
		}
	}
	if _, rl, _, _ := anon.fetch(WithCache(context.Background()), "/extra"); !rl.Limited {
		t.Fatal("anonymous quota exceeded 30")
	}
	other := NewForTest(srv.URL, "another-token", srv.Client())
	if _, _, status, err := other.fetch(WithCache(context.Background()), "/extra"); err != nil || status != 200 {
		t.Fatalf("other credential shared anonymous quota: %d %v", status, err)
	}
	if calls.Load() != 31 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestConcurrentReadersAndCancelledWaiter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); fmt.Fprint(w, `{}`) }))
	defer srv.Close()
	c := NewForTest(srv.URL, "token", srv.Client())
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/same"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent readers made %d requests", calls.Load())
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/cancelled", nil)
	req.Header.Set("Authorization", "Bearer token")
	b := sharedBudget(req, time.Now())
	b.gate <- struct{}{}
	ctx, cancel := context.WithCancel(WithCache(context.Background()))
	cancel()
	_, _, _, err := c.fetch(ctx, "/cancelled")
	<-b.gate
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("cancelled request: %v calls=%d", err, calls.Load())
	}
}

func TestSecondaryBackoffUsesCacheAndExpires(t *testing.T) {
	for _, status := range []int{403, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			now := time.Now()
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 2 {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(status)
					return
				}
				fmt.Fprint(w, `{}`)
			}))
			defer srv.Close()
			c := NewWithCredentials(srv.URL, "token", BudgetClient(srv.Client(), func() time.Time { return now }))
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/cached"); err != nil {
				t.Fatal(err)
			}
			_, rl, code, err := c.fetch(WithRefresh(context.Background()), "/cached")
			if err != nil || code != 200 || !rl.Stale {
				t.Fatalf("cache fallback: %+v %d %v", rl, code, err)
			}
			now = now.Add(time.Minute)
			_, rl, _, _ = c.fetch(WithCache(context.Background()), "/missing")
			if !rl.Limited || calls != 2 {
				t.Fatalf("backoff bypassed: %+v calls=%d", rl, calls)
			}
			if delta := rl.ResetAt.Sub(now); delta > time.Minute+time.Second {
				t.Fatalf("secondary delay became an hour: %v", delta)
			}
			now = now.Add(time.Minute + time.Second)
			if _, _, code, err := c.fetch(WithRefresh(context.Background()), "/cached"); err != nil || code != 200 || calls != 3 {
				t.Fatalf("backoff never expired: %d %v calls=%d", code, err, calls)
			}
		})
	}
}

func TestRefreshClickAndAutoRefreshLimits(t *testing.T) {
	now := time.Now()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; fmt.Fprint(w, `{}`) }))
	defer srv.Close()
	c := NewWithCredentials(srv.URL, "token", BudgetClient(srv.Client(), func() time.Time { return now }))
	get := func(refresh bool) forge.RateLimit {
		ctx := WithCache(context.Background())
		if refresh {
			ctx = WithRefresh(ctx)
		}
		_, rl, _, err := c.fetch(ctx, "/same")
		if err != nil {
			t.Fatal(err)
		}
		return rl
	}
	get(false) // first load
	// Normal reads within a minute reuse the snapshot.
	now = now.Add(59 * time.Second)
	if get(false); calls != 1 {
		t.Fatalf("list refetched inside the 1-minute cache: calls=%d", calls)
	}
	// Refresh clicks force a fetch, 10 per minute.
	for range clicksPerMinute {
		get(true)
	}
	if rl := get(true); !rl.Stale || calls != 1+clicksPerMinute {
		t.Fatalf("11th click per minute: %+v calls=%d", rl, calls)
	}
	now = now.Add(time.Minute)
	if rl := get(true); rl.Stale || calls != 2+clicksPerMinute {
		t.Fatalf("click window never reset: %+v calls=%d", rl, calls)
	}
	// Automatic reads refetch expired snapshots at most 60 times an hour,
	// counted across URLs (one URL alone can expire at most once a minute).
	paths := make([]string, autoRefreshesPerHour+1)
	for i := range paths {
		paths[i] = fmt.Sprintf("/item/%d", i)
		if _, _, _, err := c.fetch(WithCache(context.Background()), paths[i]); err != nil {
			t.Fatal(err)
		}
	}
	base := calls
	now = now.Add(cacheTTL)
	for i, path := range paths {
		_, rl, _, err := c.fetch(WithCache(context.Background()), path)
		if err != nil {
			t.Fatal(err)
		}
		if rl.Stale != (i == autoRefreshesPerHour) {
			t.Fatalf("automatic refresh %d stale=%v", i+1, rl.Stale)
		}
	}
	if calls != base+autoRefreshesPerHour {
		t.Fatalf("automatic refreshes=%d", calls-base)
	}
	now = now.Add(time.Hour)
	if _, rl, _, _ := c.fetch(WithCache(context.Background()), paths[0]); rl.Stale {
		t.Fatalf("auto window never reset: %+v", rl)
	}
}

type budgetRoundTripFunc func(*http.Request) (*http.Response, error)

func (f budgetRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQuotaProbePreservesHalfAlreadyUsed(t *testing.T) {
	for _, remaining := range []int{2, 3} {
		t.Run(strconv.Itoa(remaining), func(t *testing.T) {
			token := fmt.Sprintf("probe-%d-%d", remaining, time.Now().UnixNano())
			probes, calls := 0, 0
			transport := budgetRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{}`
				if r.URL.Path == "/rate_limit" {
					probes++
					body = fmt.Sprintf(`{"resources":{"core":{"limit":4,"remaining":%d,"reset":%d}}}`, remaining, time.Now().Add(time.Hour).Unix())
				} else {
					calls++
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			c := NewWithCredentials(DefaultAPIBase, token, &http.Client{Transport: transport})
			_, rl, _, err := c.fetch(WithCache(context.Background()), "/resource")
			if err != nil || rl.Limited != (remaining == 2) || calls != remaining-2 || probes != 1 {
				t.Fatalf("probe quota=%+v calls=%d probes=%d err=%v", rl, calls, probes, err)
			}
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/another"); err != nil || probes != 1 {
				t.Fatalf("repeated probe: %d %v", probes, err)
			}
		})
	}
}

func TestQuotaProbeFailuresNeverSpend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{"network", 0, "", errors.New("offline")}, {"status", 401, "", nil},
		{"json", 200, "{", nil}, {"invalid", 200, `{"resources":{"core":{"limit":0}}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := budgetRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/rate_limit" {
					t.Fatal("resource called without a valid quota")
				}
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			c := NewWithCredentials(DefaultAPIBase, tc.name+strconv.FormatInt(time.Now().UnixNano(), 10), &http.Client{Transport: transport})
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/resource"); err == nil {
				t.Fatal("quota probe failure was ignored")
			}
		})
	}
}

func TestWritesAreUncachedAndRespectHalfContentLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); fmt.Fprint(w, `{}`) }))
	defer srv.Close()
	client := BudgetClient(srv.Client())
	for range 40 {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/issues", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer token")
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("write: %v", err)
		}
		resp.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/issues", nil)
	req.Header.Set("Authorization", "Bearer token")
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusTooManyRequests || calls.Load() != 40 {
		t.Fatalf("write budget: %v calls=%d", err, calls.Load())
	}
	resp.Body.Close()
}

func TestCredentialDenialPurgesSnapshots(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls > 1 {
					w.WriteHeader(status)
					return
				}
				fmt.Fprint(w, `{}`)
			}))
			defer srv.Close()
			c := NewForTest(srv.URL, "token", srv.Client())
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/private"); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := c.fetch(WithRefresh(context.Background()), "/private"); !errors.Is(err, forge.ErrUnauthenticated) {
				t.Fatalf("denial=%v", err)
			}
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/private"); err == nil || calls != 3 {
				t.Fatalf("denied snapshot survived: %v calls=%d", err, calls)
			}
		})
	}
}

type failingBudgetBody struct{}

func (failingBudgetBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingBudgetBody) Close() error             { return nil }

func TestBudgetNetworkAndBodyFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.ReadCloser
		err  error
	}{
		{"network", nil, errors.New("offline")},
		{"read", failingBudgetBody{}, nil},
		{"oversized", io.NopCloser(strings.NewReader(strings.Repeat("x", (8<<20)+1))), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: budgetRoundTripFunc(func(*http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: http.StatusOK, Body: tc.body}, nil
			})}
			c := NewWithCredentials("https://test.invalid", t.Name(), client)
			if _, _, _, err := c.fetch(WithCache(context.Background()), "/resource"); err == nil {
				t.Fatal("failure ignored")
			}
		})
	}
}

func TestLargeInvalidAndNonOKResponsesAreNotCached(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"large", `"` + strings.Repeat("x", 1<<20) + `"`, 200},
		{"invalid", "not json", 200}, {"missing", `{}`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: budgetRoundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			c := NewWithCredentials("https://test.invalid", t.Name(), client)
			for range 2 {
				if _, _, _, err := c.fetch(WithCache(context.Background()), "/resource"); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatal("unusable response was cached")
			}
		})
	}
}

func TestSnapshotCapacityAndResponseIsolation(t *testing.T) {
	for _, byBytes := range []bool{false, true} {
		t.Run(strconv.FormatBool(byBytes), func(t *testing.T) {
			calls := 0
			client := BudgetClient(&http.Client{Transport: budgetRoundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})})
			req, _ := http.NewRequest(http.MethodGet, "https://test.invalid/new", nil)
			req = req.WithContext(WithCache(req.Context()))
			req.Header.Set("Authorization", t.Name())
			b := sharedBudget(req, time.Now())
			count, body := 1000, []byte(`{}`)
			if byBytes {
				count, body = 32, bytes.Repeat([]byte("x"), 1<<20)
			}
			for i := range count {
				b.cache[fmt.Sprintf("old-%d", i)] = cachedRead{body: body, at: time.Now().Add(-time.Hour).Add(time.Duration(i) * time.Millisecond)}
				b.bytes += len(body)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Header.Set("X-Test", "changed")
			resp.Body.Close()
			resp, err = client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.Header.Get("X-Test") != "" || calls != 1 || len(b.cache) > 1000 || b.bytes > 32<<20 {
				t.Fatalf("mutable or unbounded snapshot: %d %d", len(b.cache), b.bytes)
			}
			if _, ok := b.cache["old-0"]; ok {
				t.Fatal("oldest snapshot not evicted")
			}
		})
	}
}

func TestProbeHeadersAndMutationRequestShape(t *testing.T) {
	var calls int
	client := &http.Client{Transport: budgetRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		header := make(http.Header)
		body := `{}`
		if req.URL.Path == "/rate_limit" {
			if req.Method != http.MethodGet || req.Body != nil || req.ContentLength != 0 || req.URL.RawQuery != "" {
				t.Fatal("quota probe reused mutation body or query")
			}
			body = fmt.Sprintf(`{"resources":{"core":{"limit":5000,"remaining":5000,"reset":%d}}}`, time.Now().Add(time.Hour).Unix())
			header.Set("X-RateLimit-Limit", "5000")
			header.Set("X-RateLimit-Remaining", "2500")
			header.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		} else {
			t.Fatal("headers' lower remaining quota was ignored")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	req, _ := http.NewRequest(http.MethodPost, DefaultAPIBase+"/issues?something=1", strings.NewReader(`{}`))
	req.Header.Set("Authorization", t.Name())
	resp, err := BudgetClient(client).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || calls != 1 {
		t.Fatalf("quota probe: %d calls=%d", resp.StatusCode, calls)
	}
}

func TestCancelledDefaultClientAndInvalidHeaders(t *testing.T) {
	ctx, cancel := context.WithCancel(WithCache(context.Background()))
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://test.invalid/cancelled", nil)
	resp, err := BudgetClient(nil).Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	b := &readBudget{limit: 60, remaining: 59, reset: time.Now().Add(time.Hour)}
	for _, h := range []http.Header{
		{"X-Ratelimit-Limit": {"0"}},
		{"X-Ratelimit-Limit": {"60"}, "X-Ratelimit-Remaining": {"bad"}, "X-Ratelimit-Reset": {"bad"}},
		{"X-Ratelimit-Limit": {"60"}, "X-Ratelimit-Remaining": {"-1"}, "X-Ratelimit-Reset": {"1"}},
	} {
		b.observe(h, time.Now())
	}
	if b.limit != 60 || b.remaining != 59 {
		t.Fatal("invalid headers changed quota")
	}
}

func TestUncachedCallersAlwaysReadLive(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"number":1,"title":"v%d","state":"open"}`, calls.Add(1))
	}))
	defer srv.Close()
	c := NewForTest(srv.URL, "factory-token", srv.Client())
	for want := range []int{1, 2} {
		pr, err := c.LookupPR(context.Background(), "o/r", 1)
		if err != nil || pr.Title != fmt.Sprintf("v%d", want+1) {
			t.Fatalf("delivery check read a cached PR: %+v %v", pr, err)
		}
	}
}

// Review finding: a forced refresh that gets 404 must evict the snapshot, so
// a later normal read cannot resurrect a deleted (or now private) resource.
func TestNotFoundEvictsSnapshot(t *testing.T) {
	gone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if gone {
			http.NotFound(w, nil)
			return
		}
		fmt.Fprint(w, `{"number":1}`)
	}))
	defer srv.Close()
	c := NewForTest(srv.URL, t.Name(), srv.Client())
	if _, _, status, _ := c.fetch(WithCache(context.Background()), "/issue"); status != http.StatusOK {
		t.Fatal("populate failed")
	}
	gone = true
	if _, _, status, _ := c.fetch(WithRefresh(context.Background()), "/issue"); status != http.StatusNotFound {
		t.Fatalf("refresh status=%d", status)
	}
	if _, _, status, _ := c.fetch(WithCache(context.Background()), "/issue"); status != http.StatusNotFound {
		t.Fatalf("deleted resource resurrected from cache: status=%d", status)
	}
}

// Review finding: a failed first quota probe keeps its HTTP status for normal
// classification, and a rate-limited probe records a backoff that later
// requests honor without probing again.
func TestProbeFailuresKeepStatusAndBackoff(t *testing.T) {
	for _, tc := range []struct {
		status  int
		header  string
		wantErr error
		backoff bool
	}{
		{http.StatusUnauthorized, "", forge.ErrUnauthenticated, false},
		{http.StatusTooManyRequests, "120", nil, true},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			probes, reads := 0, 0
			transport := budgetRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/rate_limit" {
					reads++
				} else {
					probes++
				}
				h := make(http.Header)
				if tc.header != "" {
					h.Set("Retry-After", tc.header)
				}
				return &http.Response{StatusCode: tc.status, Header: h, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})
			c := NewWithCredentials(DefaultAPIBase, t.Name()+strconv.FormatInt(time.Now().UnixNano(), 10), &http.Client{Transport: transport})
			_, rl, status, err := c.fetch(WithCache(context.Background()), "/resource")
			if status != tc.status || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("probe failure lost its status: status=%d err=%v", status, err)
			}
			if tc.backoff {
				if !rl.Limited || rl.ResetAt.IsZero() {
					t.Fatalf("429 probe lost its rate limit: %+v", rl)
				}
				if _, rl, _, _ = c.fetch(WithCache(context.Background()), "/resource"); !rl.Limited || probes != 1 {
					t.Fatalf("second request ignored probe backoff: %+v probes=%d", rl, probes)
				}
			}
			if reads != 0 {
				t.Fatalf("resource read despite a failed probe: %d", reads)
			}
		})
	}
}
