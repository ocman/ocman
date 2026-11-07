package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/httpx"
)

const snapshotRecovery = 5 * time.Second
const snapshotEntryLimit = 64
const snapshotByteLimit = 32 << 20

func invalidatesSessionSnapshots(event string) bool {
	return event != "ocman.session.activity" && (strings.HasPrefix(event, "ocman.session.") ||
		strings.HasPrefix(event, "ocman.permission.") || strings.HasPrefix(event, "ocman.question.") ||
		event == "ocman.projects.changed" || event == "ocman.settings.changed" || event == "ocman.inbox.changed")
}

type httpSnapshot struct {
	body    []byte
	etag    string
	at      time.Time
	used    time.Time
	done    chan struct{}
	version uint64
}

// Request-owned refreshes are joined by concurrent consumers. There is no
// background worker, stale-on-error response, or cache shared between servers.
type httpSnapshots struct {
	mu      sync.Mutex
	entries map[string]*httpSnapshot
	epoch   string
	version uint64
	bytes   int
	now     func() time.Time
}

func (c *httpSnapshots) invalidate() {
	c.mu.Lock()
	c.version++
	c.mu.Unlock()
}

func (c *httpSnapshots) get(ctx context.Context, key string, read func() (any, error)) ([]byte, string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		c.mu.Lock()
		if c.entries == nil {
			c.entries = make(map[string]*httpSnapshot)
			c.epoch = uuid.NewString()
		}
		now := time.Now()
		if c.now != nil {
			now = c.now()
		}
		entry := c.entries[key]
		if entry != nil && entry.done != nil {
			done := entry.done
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}
		if entry != nil && entry.version == c.version && now.Sub(entry.at) < snapshotRecovery {
			entry.used = now
			body, etag := entry.body, entry.etag
			c.mu.Unlock()
			return body, etag, nil
		}
		if entry == nil {
			c.evict(snapshotEntryLimit - 1)
			if len(c.entries) >= snapshotEntryLimit {
				c.mu.Unlock()
				// All slots are in flight. Bound retained state, not correctness.
				value, err := read()
				if err != nil {
					return nil, "", err
				}
				body, err := snapshotJSONBody(value)
				return body, "", err
			}
			entry = &httpSnapshot{}
			c.entries[key] = entry
		}
		c.bytes -= len(entry.body)
		entry.body = nil
		entry.done, entry.version = make(chan struct{}), c.version
		version, epoch := c.version, c.epoch
		c.mu.Unlock()

		body, err := c.read(key, entry, read)
		c.mu.Lock()
		close(entry.done)
		entry.done = nil
		if err != nil || ctx.Err() != nil || version != c.version || len(body) > snapshotByteLimit {
			delete(c.entries, key)
			c.mu.Unlock()
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			// A concurrent mutation fences publication. The initiating request
			// may finish, but cannot give its older response a reusable version.
			return body, "", err
		}
		entry.body, entry.at, entry.used = body, now, now
		entry.etag = fmt.Sprintf(`"%s-%x"`, epoch, sha256.Sum256(body))
		c.bytes += len(body)
		c.evict(snapshotEntryLimit)
		etag := entry.etag
		c.mu.Unlock()
		return body, etag, nil
	}
}

func (c *httpSnapshots) read(key string, entry *httpSnapshot, read func() (any, error)) (body []byte, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			c.mu.Lock()
			delete(c.entries, key)
			close(entry.done)
			entry.done = nil
			c.mu.Unlock()
			panic(failure)
		}
	}()
	value, err := read()
	if err != nil {
		return nil, err
	}
	return snapshotJSONBody(value)
}

func snapshotJSONBody(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	return append(body, '\n'), err
}

func (c *httpSnapshots) age(key, etag string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; etag != "" && entry != nil && entry.etag == etag {
		return max(0, time.Since(entry.at))
	}
	return 0
}

// Called with mu held. Loading entries are never evicted beneath waiters.
func (c *httpSnapshots) evict(maxEntries int) {
	for len(c.entries) > maxEntries || c.bytes > snapshotByteLimit {
		var oldest *httpSnapshot
		var key string
		for k, entry := range c.entries {
			if entry.done == nil && (oldest == nil || entry.used.Before(oldest.used)) {
				key, oldest = k, entry
			}
		}
		if oldest == nil {
			return
		}
		c.bytes -= len(oldest.body)
		delete(c.entries, key)
	}
}

func (s *Server) writeSessionSnapshot(w http.ResponseWriter, r *http.Request, read func() (any, error)) {
	key := r.URL.Path + "?" + r.URL.Query().Encode()
	body, etag, err := s.sessionSnapshots.get(r.Context(), key, read)
	if err != nil {
		httpx.ServerError(w, "reading session snapshot", err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Ocman-Snapshot-Age-Ms", fmt.Sprint(s.sessionSnapshots.age(key, etag).Milliseconds()))
	if etag != "" {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	_, _ = w.Write(body)
}

// Stable relative windows let tabs with the same filters share a snapshot.
// Explicit since timestamps retain their existing, exact semantics.
func sidebarSince(r *http.Request) int64 {
	if r.URL.Query().Has("since") {
		return httpx.QueryInt[int64](r, "since", 0)
	}
	hours := httpx.QueryInt[int64](r, "sinceHours", 0)
	if hours <= 0 || hours > 24*365*100 {
		return 0
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour).UnixMilli()
}

// Fence publication after successful mutations, including module-owned writes
// that do not yet publish events. Rejected/unauthenticated requests and telemetry
// heartbeats must not flush the cache. Read-only POSTs invalidate conservatively.
func (s *Server) invalidateSessionWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		mutation := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if mutation && (strings.HasPrefix(path, "/api/session") || strings.HasPrefix(path, "/api/project") || strings.HasPrefix(path, "/api/settings/")) {
			recorded := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if recorded.status >= http.StatusOK && recorded.status < http.StatusBadRequest {
					s.sessionSnapshots.invalidate()
				}
			}()
			next.ServeHTTP(recorded, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
