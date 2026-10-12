package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
)

type refreshKey struct{}
type cacheKey struct{}

// WithCache lets sidebar and link-preview reads share one-minute snapshots.
// Other callers (Factory delivery checks) always read live data.
func WithCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, cacheKey{}, true)
}

type budgetHandledKey struct{}

// WithRefresh marks an explicit Refresh click: it bypasses cached reads
// (10 per minute), never the shared GitHub request budget.
func WithRefresh(ctx context.Context) context.Context {
	return context.WithValue(WithCache(ctx), refreshKey{}, true)
}

type cachedRead struct {
	header http.Header
	body   []byte
	at     time.Time
}

type readBudget struct {
	gate         chan struct{}
	limit        int
	remaining    int
	used         int
	reset        time.Time
	blocked      time.Time
	initialized  bool
	cache        map[string]cachedRead
	bytes        int
	minute       time.Time
	points       int
	writes       int
	minuteWrites int
	clickStart   time.Time
	clicks       int
	autoStart    time.Time
	autos        int
}

const (
	// cacheTTL: normal reads (lists, previews, polling) reuse a snapshot this long.
	cacheTTL = time.Minute
	// clicksPerMinute caps explicit Refresh-button revalidations.
	clicksPerMinute = 10
	// autoRefreshesPerHour caps automatic revalidation of expired snapshots.
	autoRefreshesPerHour = 60
)

// take spends one slot from a fixed window; false when it is full.
func take(start *time.Time, n *int, now time.Time, window time.Duration, limit int) bool {
	if now.Sub(*start) >= window {
		*start, *n = now, 0
	}
	if *n >= limit {
		return false
	}
	*n++
	return true
}

var budgets = struct {
	sync.Mutex
	byCredential map[string]*readBudget
}{byCredential: make(map[string]*readBudget)}

type budgetTransport struct {
	http.RoundTripper
	now func() time.Time
}

// BudgetClient shares reads and quota across sidebar, previews and clients
// using the same API host and credential. It preserves the caller's transport.
func BudgetClient(client *http.Client, clocks ...func() time.Time) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	copy := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if _, ok := transport.(budgetTransport); ok {
		return &copy
	}
	now := time.Now
	if len(clocks) > 0 && clocks[0] != nil {
		now = clocks[0]
	}
	copy.Transport = budgetTransport{RoundTripper: transport, now: now}
	return &copy
}

func sharedBudget(req *http.Request, now time.Time) *readBudget {
	auth := req.Header.Get("Authorization")
	key := fmt.Sprintf("%s://%s/%x", req.URL.Scheme, req.URL.Host, sha256.Sum256([]byte(auth)))
	budgets.Lock()
	defer budgets.Unlock()
	if b := budgets.byCredential[key]; b != nil {
		return b
	}
	limit := 60
	if auth != "" {
		limit = 5000
	}
	b := &readBudget{gate: make(chan struct{}, 1), limit: limit, remaining: limit,
		reset: now.Add(time.Hour), cache: make(map[string]cachedRead)}
	budgets.byCredential[key] = b
	return b
}

func (t budgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/graphql" || req.Context().Value(budgetHandledKey{}) == true {
		return t.RoundTripper.RoundTrip(req)
	}
	started := t.now()
	b := sharedBudget(req, started)
	// ponytail: one in-flight request per credential also avoids GitHub's
	// concurrency limit; use a cancellable gate rather than a waiting mutex.
	select {
	case b.gate <- struct{}{}:
		defer func() { <-b.gate }()
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	now := t.now()
	if now.Sub(b.minute) >= time.Minute {
		b.minute, b.points, b.minuteWrites = now, 0, 0
	}
	if !now.Before(b.reset) {
		b.used, b.remaining, b.reset = 0, b.limit, now.Add(time.Hour)
		b.writes = 0
		b.initialized = false
	}
	// Keyed by URL only so the sidebar and link previews share snapshots.
	key := req.URL.String()
	cached, hit := b.cache[key]
	read := req.Method == http.MethodGet && req.Context().Value(cacheKey{}) == true
	hit = hit && read
	refresh := req.Context().Value(refreshKey{}) == true
	if hit {
		switch {
		case cached.at.After(started): // a concurrent caller just fetched it
			return cached.response(req), nil
		case refresh:
			if !take(&b.clickStart, &b.clicks, now, time.Minute, clicksPerMinute) {
				return cached.stale(req, b.clickStart.Add(time.Minute)), nil
			}
		case now.Sub(cached.at) < cacheTTL:
			return cached.response(req), nil
		default:
			if !take(&b.autoStart, &b.autos, now, time.Hour, autoRefreshesPerHour) {
				return cached.stale(req, b.autoStart.Add(time.Hour)), nil
			}
		}
	}
	// Honor a known provider backoff before any request, including the probe.
	if now.Before(b.blocked) {
		if hit {
			return cached.stale(req, b.blocked), nil
		}
		return limitedResponse(req, b.blocked, now), nil
	}
	// The free quota endpoint makes the first real request respect quotas
	// already consumed by other tools, including after an ocman restart.
	if !b.initialized && req.URL.Hostname() == "api.github.com" {
		if resp, err := t.probe(req, b, now); resp != nil || err != nil {
			return resp, err
		}
	}
	retryAt := b.blocked
	points := 1
	mutating := req.Method == http.MethodPost || req.Method == http.MethodPut || req.Method == http.MethodPatch || req.Method == http.MethodDelete
	if mutating {
		points = 5
		if b.minuteWrites >= 40 {
			retryAt = maxTime(retryAt, b.minute.Add(time.Minute))
		}
		if b.writes >= 250 {
			retryAt = maxTime(retryAt, b.reset)
		}
	}
	if b.points+points > 450 {
		retryAt = maxTime(retryAt, b.minute.Add(time.Minute))
	}
	if b.used >= b.limit/2 || b.remaining <= b.limit-b.limit/2 {
		retryAt = maxTime(retryAt, b.reset)
	}
	if now.Before(retryAt) {
		if hit {
			return cached.stale(req, retryAt), nil
		}
		return limitedResponse(req, retryAt, now), nil
	}
	b.used++
	b.remaining--
	b.points += points
	if mutating {
		b.writes++
		b.minuteWrites++
	}
	forward := req.WithContext(context.WithValue(req.Context(), budgetHandledKey{}, true))
	resp, err := t.RoundTripper.RoundTrip(forward)
	if err != nil {
		return nil, err
	}
	b.observe(resp.Header, now)
	limited := b.backoff(resp, now)
	if limited {
		if hit {
			resp.Body.Close()
			return cached.stale(req, b.blocked), nil
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden && !limited {
		clear(b.cache)
		b.bytes = 0
	}
	// A confirmed 404 (deleted, or now private) invalidates the old snapshot.
	if resp.StatusCode == http.StatusNotFound && hit {
		b.bytes -= len(cached.body)
		delete(b.cache, key)
	}
	if resp.StatusCode != http.StatusOK {
		return resp, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, forgehttp.MaxResponseBytes+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > forgehttp.MaxResponseBytes {
		return nil, fmt.Errorf("forge response too large (over %d bytes)", forgehttp.MaxResponseBytes)
	}
	entry := cachedRead{header: resp.Header.Clone(), body: body, at: t.now()}
	// Bound retained bytes to 32 MiB and 1000 snapshots per credential.
	// Oversized responses are returned normally but not cached.
	if read && len(body) <= 1<<20 && json.Valid(body) {
		if hit {
			b.bytes -= len(cached.body)
			delete(b.cache, key)
		}
		for len(b.cache) >= 1000 || b.bytes+len(body) > 32<<20 {
			oldestKey, oldest := "", entry.at
			for k, value := range b.cache {
				if oldestKey == "" || value.at.Before(oldest) {
					oldestKey, oldest = k, value.at
				}
			}
			b.bytes -= len(b.cache[oldestKey].body)
			delete(b.cache, oldestKey)
		}
		b.cache[key] = entry
		b.bytes += len(body)
	}
	return entry.response(req), nil
}

func (b *readBudget) observe(header http.Header, now time.Time) {
	limit, err := strconv.Atoi(header.Get("X-RateLimit-Limit"))
	if err != nil || limit < 1 {
		return
	}
	b.limit = limit
	if remaining, err := strconv.Atoi(header.Get("X-RateLimit-Remaining")); err == nil && remaining >= 0 {
		b.remaining = min(b.remaining, remaining)
	}
	if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(reset, 0).After(now) {
		b.reset = time.Unix(reset, 0)
	}
}

// backoff records a primary or secondary rate-limit deadline from resp.
func (b *readBudget) backoff(resp *http.Response, now time.Time) bool {
	limited := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "")
	if !limited {
		return false
	}
	b.blocked = forgehttp.ParseRateLimit(resp.Header, true).ResetAt
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
		b.blocked = now.Add(time.Duration(seconds) * time.Second)
	}
	if !b.blocked.After(now) {
		b.blocked = now.Add(time.Minute)
	}
	return true
}

// probe reads the free /rate_limit endpoint. A non-200 response is returned
// to the caller unchanged, so 401/403/429 keep their status and headers for
// normal classification, and rate limits record a backoff.
func (t budgetTransport) probe(req *http.Request, b *readBudget, now time.Time) (*http.Response, error) {
	probe := req.Clone(context.WithValue(req.Context(), budgetHandledKey{}, true))
	probe.Method, probe.Body, probe.GetBody, probe.ContentLength = http.MethodGet, nil, nil, 0
	probe.URL.Path, probe.URL.RawPath, probe.URL.RawQuery = "/rate_limit", "", ""
	b.points++
	resp, err := t.RoundTripper.RoundTrip(probe)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b.backoff(resp, now)
		resp.Request = req
		return resp, nil
	}
	defer resp.Body.Close()
	var result struct {
		Resources struct {
			Core struct{ Limit, Remaining, Reset int } `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, forgehttp.MaxResponseBytes)).Decode(&result); err != nil {
		return nil, err
	}
	core := result.Resources.Core
	if core.Limit < 1 || core.Remaining < 0 || core.Remaining > core.Limit || int64(core.Reset) <= now.Unix() {
		return nil, fmt.Errorf("github quota probe returned invalid core quota")
	}
	b.limit, b.remaining, b.reset = core.Limit, core.Remaining, time.Unix(int64(core.Reset), 0)
	b.observe(resp.Header, now)
	b.initialized = true
	return nil, nil
}

func (c cachedRead) response(req *http.Request) *http.Response {
	header := c.header.Clone()
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header,
		Body: io.NopCloser(bytes.NewReader(c.body)), ContentLength: int64(len(c.body)), Request: req}
}

func (c cachedRead) stale(req *http.Request, reset time.Time) *http.Response {
	resp := c.response(req)
	resp.Header.Set("X-Ocman-Cache", "stale")
	resp.Header.Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	return resp
}

func limitedResponse(req *http.Request, reset, now time.Time) *http.Response {
	header := make(http.Header)
	header.Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	header.Set("Retry-After", strconv.Itoa(max(1, int((reset.Sub(now)+time.Second-1)/time.Second))))
	return &http.Response{StatusCode: http.StatusTooManyRequests, Header: header,
		Body: io.NopCloser(bytes.NewReader([]byte(`{"message":"ocman GitHub half-quota budget exhausted"}`))), Request: req}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
