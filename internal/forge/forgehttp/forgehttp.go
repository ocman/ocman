// Package forgehttp holds the REST plumbing shared by the GitHub and
// Forgejo forge clients: rate-limit header parsing and the
// read-body-and-classify step of a GET. The per-forge clients differ
// only in base URL and auth scheme, which they apply when building the
// request; everything downstream of "send the request" is identical
// and lives here.
package forgehttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// defaultTimeout bounds a forge API call when the caller doesn't supply
// its own *http.Client.
const defaultTimeout = 10 * time.Second

// MaxResponseBytes bounds a forge API response. PR/issue list pages with
// full bodies are tens of KB; 8 MiB leaves a wide margin while keeping a
// misbehaving (or impersonated) forge from filling ocman's heap.
const MaxResponseBytes int64 = 8 << 20

// Get issues req using client (or a default 10s client when nil), reads
// the full body, parses rate-limit headers, and returns body +
// rate-limit info + HTTP status. Network and read errors surface as
// err; an HTTP 429 comes back as status=429 with rl.Limited=true so
// callers can distinguish "rate limited" from "totally failed".
func Get(ctx context.Context, client *http.Client, req *http.Request) ([]byte, forge.RateLimit, int, error) {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, forge.RateLimit{}, 0, err
	}
	defer resp.Body.Close()

	// Bounded read: reads one byte past the limit so an oversized body is
	// reported as an error instead of silently truncated into invalid JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, forge.RateLimit{}, resp.StatusCode, err
	}
	if int64(len(body)) > MaxResponseBytes {
		return nil, forge.RateLimit{}, resp.StatusCode,
			fmt.Errorf("forge response too large (over %d bytes)", MaxResponseBytes)
	}
	rl := ParseRateLimit(resp.Header, resp.StatusCode == http.StatusTooManyRequests)
	return body, rl, resp.StatusCode, nil
}

// ParseRateLimit extracts Retry-After (delta-seconds) or
// X-RateLimit-Reset (Unix seconds) from a response header. ResetAt is
// set when a header is parseable; Limited follows the supplied flag.
// Both GitHub and Forgejo emit these headers.
func ParseRateLimit(h http.Header, limited bool) forge.RateLimit {
	if v := h.Get("Retry-After"); v != "" {
		// Retry-After can be HTTP-date or delta-seconds; we accept seconds.
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return forge.RateLimit{Limited: limited, ResetAt: time.Now().Add(time.Duration(secs) * time.Second)}
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return forge.RateLimit{Limited: limited, ResetAt: time.Unix(ts, 0)}
		}
	}
	return forge.RateLimit{Limited: limited}
}
