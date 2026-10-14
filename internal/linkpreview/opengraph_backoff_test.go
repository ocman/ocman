package linkpreview

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOpenGraphBackoffIsOriginScopedAndHonorsRetryAfter(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	hits := map[string]int{}
	limited := true
	transport := ogTransport(func(req *http.Request) (*http.Response, error) {
		hits[req.URL.Host]++
		status, body := http.StatusOK, `<meta property="og:title" content="A page">`
		header := http.Header{"Content-Type": {"text/html"}}
		if req.URL.Host == "a.example" && limited {
			status = http.StatusTooManyRequests
			header.Set("Retry-After", "600")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: header, Request: req}, nil
	})
	s := New(nil, nil)
	s.PublicClient.Transport = transport
	s.now = func() time.Time { return now }
	gk := grantKey("viewer", "local", "opengraph", "")
	resolve := func(raw string) Preview {
		return s.ResolveOpenGraph(ctx, "viewer", "local", raw)
	}
	if got := resolve("https://a.example/one"); got.State != StateRateLimited {
		t.Fatalf("A: %+v", got)
	}
	if got := resolve("https://b.example/one"); got.State != StateOK {
		t.Fatalf("A's backoff blocked B: %+v", got)
	}
	now = now.Add(2 * time.Minute)
	if got := resolve("https://A.example:443/two"); got.State != StateRateLimited || hits["a.example"] != 1 {
		t.Fatalf("Retry-After ignored: %+v, hits=%v", got, hits)
	}
	limited = false
	now = now.Add(8 * time.Minute)
	if got := resolve("https://a.example/two"); got.State != StateOK || hits["a.example"] != 2 {
		t.Fatalf("backoff did not expire: %+v, %v", got, hits)
	}

	// A host-scoped backoff must not create a host-scoped fetch budget.
	s.budget[gk] = window{start: now, n: fetchesPerMinute}
	if got := resolve("https://c.example/one"); got.State != StateRateLimited || hits["c.example"] != 0 {
		t.Fatalf("aggregate budget bypassed: %+v", got)
	}
}

func TestOpenGraphRetryAfterFormats(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"600", 10 * time.Minute}, {"-1", 0}, {"invalid", 0},
		{"9223372036854775807", maxBackoff},
		{now.Add(10 * time.Minute).UTC().Format(http.TimeFormat), 10 * time.Minute},
		{now.Add(-time.Minute).UTC().Format(http.TimeFormat), 0},
	} {
		if got := publicPageRetryAfter(tc.value, now); got != tc.want {
			t.Errorf("%q = %s, want %s", tc.value, got, tc.want)
		}
	}
}

func TestOpenGraphHTTPErrorRetainsRetryAfter(t *testing.T) {
	client := &http.Client{Transport: ogTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Retry-After": {"600"}}, Request: req}, nil
	})}
	_, _, _, err := fetchPublicPage(context.Background(), client, "https://example.com", "text/html", maxResponse)
	var he *HTTPError
	if !errors.As(err, &he) || he.RetryAfter != 10*time.Minute {
		t.Fatalf("Retry-After discarded: %v", err)
	}
}
