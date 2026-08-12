package forgehttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestGetBoundsResponseSize proves a forge (or anything impersonating
// one, e.g. a captive-portal proxy) cannot make ocman buffer an
// unbounded response into memory.
func TestGetBoundsResponseSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := strings.Repeat("a", 1<<20)
		for written := int64(0); written <= MaxResponseBytes; written += int64(len(chunk)) {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _, status, err := Get(context.Background(), nil, req)
	if err == nil {
		t.Fatalf("expected an error, got %d bytes (status %d)", len(body), status)
	}
	if int64(len(body)) > MaxResponseBytes {
		t.Errorf("buffered %d bytes past the %d limit", len(body), MaxResponseBytes)
	}
}

// TestGetReadsNormalResponse keeps the happy path honest: a small body
// still comes back whole, with rate-limit headers parsed.
func TestGetReadsNormalResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`[{"number":1}]`))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	body, rl, status, err := Get(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusTooManyRequests || !rl.Limited {
		t.Fatalf("status = %d, limited = %v, want 429/true", status, rl.Limited)
	}
	if rl.ResetAt.Before(time.Now()) {
		t.Errorf("ResetAt = %v, want a future time from Retry-After", rl.ResetAt)
	}
	if string(body) != `[{"number":1}]` {
		t.Errorf("body = %q, want the full small payload", body)
	}
}
