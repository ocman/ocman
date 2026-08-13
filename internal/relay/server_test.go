package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NoUseFreak/ocman/internal/share"
)

type harness struct {
	t     *testing.T
	srv   *Server
	store share.Store
	now   time.Time
}

func newHarness(t *testing.T, tweak func(*Config)) *harness {
	t.Helper()
	store, err := share.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	h := &harness{t: t, store: store, now: time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)}
	cfg := Config{
		Store: store,
		Now:   func() time.Time { return h.now },
	}
	if tweak != nil {
		tweak(&cfg)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.srv = srv
	return h
}

func (h *harness) do(method, path string, body []byte, token string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.1:1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec
}

func (h *harness) create() createResponse {
	h.t.Helper()
	rec := h.do(http.MethodPost, "/s", nil, "")
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create: status %d, body %s", rec.Code, rec.Body)
	}
	var resp createResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		h.t.Fatalf("decoding create response: %v", err)
	}
	return resp
}

func (h *harness) read(id string, from uint64) readResponse {
	h.t.Helper()
	rec := h.do(http.MethodGet, fmt.Sprintf("/s/%s?from=%d", id, from), nil, "")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("read: status %d, body %s", rec.Code, rec.Body)
	}
	var resp readResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		h.t.Fatalf("decoding read response: %v", err)
	}
	return resp
}

func TestCreate_ReturnsIdTokenAndLimits(t *testing.T) {
	h := newHarness(t, nil)
	resp := h.create()

	if !validID(resp.ID) {
		t.Fatalf("create returned an invalid id %q", resp.ID)
	}
	if resp.DeleteToken == "" {
		t.Fatal("create returned no delete token")
	}
	if resp.MaxChunkBytes != DefaultMaxChunkBytes || resp.MaxChunks != DefaultMaxChunks {
		t.Fatalf("limits not echoed: %+v", resp)
	}
	if resp.ExpiresAt != h.now.Add(DefaultTTL).UnixMilli() {
		t.Fatalf("ExpiresAt = %d, want %d", resp.ExpiresAt, h.now.Add(DefaultTTL).UnixMilli())
	}
}

// TestCreate_StoresOnlyHashedDeleteToken is the guard on the "no
// replayable credential at rest" property: reading the backend must not
// yield the token itself.
func TestCreate_StoresOnlyHashedDeleteToken(t *testing.T) {
	h := newHarness(t, nil)
	resp := h.create()

	objs, err := h.store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, o := range objs {
		b, err := h.store.Get(context.Background(), o.Key)
		if err != nil {
			t.Fatalf("Get %s: %v", o.Key, err)
		}
		if bytes.Contains(b, []byte(resp.DeleteToken)) {
			t.Fatalf("delete token stored in the clear at %s", o.Key)
		}
	}
}

// TestRoundTrip_SealAppendReadOpen is the end-to-end proof that a writer
// and a viewer agree on the format, and that the relay is a pure conduit
// for bytes it cannot read.
func TestRoundTrip_SealAppendReadOpen(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()

	key, err := share.NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	turns := [][]byte{
		[]byte(`{"session":{"id":"ses_1"},"messages":[{"id":"m1"}]}`),
		[]byte(`{"messages":[{"id":"m2"}]}`),
		[]byte(`{"messages":[{"id":"m3"}]}`),
	}
	for seq, plain := range turns {
		sealed, err := share.Seal(key, created.ID, uint64(seq), plain)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		rec := h.do(http.MethodPut, fmt.Sprintf("/s/%s/%d", created.ID, seq), sealed, created.DeleteToken)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("append seq %d: status %d, body %s", seq, rec.Code, rec.Body)
		}
	}

	resp := h.read(created.ID, 0)
	if len(resp.Chunks) != len(turns) {
		t.Fatalf("got %d chunks, want %d", len(resp.Chunks), len(turns))
	}
	if resp.Last != int64(len(turns)-1) {
		t.Fatalf("Last = %d, want %d", resp.Last, len(turns)-1)
	}
	for i, c := range resp.Chunks {
		if c.Seq != uint64(i) {
			t.Fatalf("chunk %d has seq %d; chunks must be ordered", i, c.Seq)
		}
		raw, err := base64.RawStdEncoding.DecodeString(c.Data)
		if err != nil {
			t.Fatalf("decoding chunk %d: %v", i, err)
		}
		plain, err := share.Open(key, created.ID, c.Seq, raw)
		if err != nil {
			t.Fatalf("Open chunk %d: %v", i, err)
		}
		if !bytes.Equal(plain, turns[i]) {
			t.Fatalf("chunk %d = %q, want %q", i, plain, turns[i])
		}
	}
}

// TestRead_FromResumesWithoutRefetching is the polling path: a viewer
// that has seen up to Last asks for the rest.
func TestRead_FromResumesWithoutRefetching(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()
	for seq := range uint64(3) {
		h.appendChunk(created, seq, []byte{byte(seq)})
	}

	resp := h.read(created.ID, 2)
	if len(resp.Chunks) != 1 || resp.Chunks[0].Seq != 2 {
		t.Fatalf("from=2 returned %+v, want only seq 2", resp.Chunks)
	}
	empty := h.read(created.ID, 3)
	if len(empty.Chunks) != 0 {
		t.Fatalf("from beyond the end returned %d chunks", len(empty.Chunks))
	}
	if empty.Last != -1 {
		t.Fatalf("Last = %d for an empty response, want -1", empty.Last)
	}
}

func (h *harness) appendChunk(created createResponse, seq uint64, body []byte) {
	h.t.Helper()
	rec := h.do(http.MethodPut, fmt.Sprintf("/s/%s/%d", created.ID, seq), body, created.DeleteToken)
	if rec.Code != http.StatusNoContent {
		h.t.Fatalf("append seq %d: status %d, body %s", seq, rec.Code, rec.Body)
	}
}

// TestAppend_IsIdempotent covers the retry path that writer-allocated
// sequence numbers exist to enable.
func TestAppend_IsIdempotent(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()
	h.appendChunk(created, 0, []byte("turn zero"))
	h.appendChunk(created, 0, []byte("turn zero"))

	resp := h.read(created.ID, 0)
	if len(resp.Chunks) != 1 {
		t.Fatalf("a retried append produced %d chunks, want 1", len(resp.Chunks))
	}
}

func TestAppend_RequiresDeleteToken(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()

	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"wrong token", "not-the-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.do(http.MethodPut, "/s/"+created.ID+"/0", []byte("x"), tc.token)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404 (existence must not be confirmed)", rec.Code)
			}
		})
	}
	if resp := h.read(created.ID, 0); len(resp.Chunks) != 0 {
		t.Fatal("an unauthorised append was stored")
	}
}

func TestDelete_RevokesAndCollapsesTo404(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()
	h.appendChunk(created, 0, []byte("secret"))

	rec := h.do(http.MethodDelete, "/s/"+created.ID, nil, created.DeleteToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d, body %s", rec.Code, rec.Body)
	}
	rec = h.do(http.MethodGet, "/s/"+created.ID, nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("read after delete: status %d, want 404", rec.Code)
	}
	objs, err := h.store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("delete left %d objects behind: %v", len(objs), objs)
	}
}

func TestDelete_RequiresDeleteToken(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()
	h.appendChunk(created, 0, []byte("x"))

	rec := h.do(http.MethodDelete, "/s/"+created.ID, nil, "wrong")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if resp := h.read(created.ID, 0); len(resp.Chunks) != 1 {
		t.Fatal("an unauthorised delete removed data")
	}
}

func TestRead_UnknownShareIs404(t *testing.T) {
	h := newHarness(t, nil)
	id, err := newID(h.now)
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	rec := h.do(http.MethodGet, "/s/"+id, nil, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// TestRead_RejectsTraversalIds proves an untrusted path segment can
// never yield content. Some forms are cleaned into a redirect by the
// mux before reaching the handler; what matters is that none of them
// return data.
func TestRead_RejectsTraversalIds(t *testing.T) {
	h := newHarness(t, nil)
	for _, id := range []string{"..", "%2e%2e", "20260813-..", "not-an-id", "....", "20260813-aa%2Fbb"} {
		rec := h.do(http.MethodGet, "/s/"+id, nil, "")
		if rec.Code == http.StatusOK {
			t.Fatalf("id %q returned 200 with body %s", id, rec.Body)
		}
	}
}

func TestRead_SetsPermissiveCORS(t *testing.T) {
	h := newHarness(t, nil)
	created := h.create()
	rec := h.do(http.MethodGet, "/s/"+created.ID, nil, "")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want * (an ocman on another origin forks from here)", got)
	}
}

func TestAppend_RejectsOversizedChunk(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxChunkBytes = 16 })
	created := h.create()
	rec := h.do(http.MethodPut, "/s/"+created.ID+"/0", bytes.Repeat([]byte("x"), 64), created.DeleteToken)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
}

func TestAppend_RejectsTooManyChunks(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxChunks = 2 })
	created := h.create()
	h.appendChunk(created, 0, []byte("a"))
	h.appendChunk(created, 1, []byte("b"))

	rec := h.do(http.MethodPut, "/s/"+created.ID+"/2", []byte("c"), created.DeleteToken)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want the third chunk refused", rec.Code)
	}
	// Overwriting an existing chunk must still be allowed at the cap,
	// or a writer could never retry its last append.
	h.appendChunk(created, 1, []byte("b2"))
}

// TestAppend_RejectsScatteredSequence proves the chunk cap cannot be
// defeated by writing sparse, far-apart sequence numbers.
func TestAppend_RejectsScatteredSequence(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxChunks = 4 })
	created := h.create()
	for _, seq := range []string{"4", "999999999", "18446744073709551615", "-1", "abc"} {
		rec := h.do(http.MethodPut, "/s/"+created.ID+"/"+seq, []byte("x"), created.DeleteToken)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Fatalf("seq %q: status %d, want rejection", seq, rec.Code)
		}
	}
}

func TestAppend_RejectsOversizedShare(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.MaxChunkBytes = 32
		c.MaxShareBytes = 40
	})
	created := h.create()
	h.appendChunk(created, 0, bytes.Repeat([]byte("x"), 30))

	rec := h.do(http.MethodPut, "/s/"+created.ID+"/1", bytes.Repeat([]byte("y"), 30), created.DeleteToken)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413 once the share exceeds its byte cap", rec.Code)
	}
}

// TestAppend_OverwriteDoesNotDoubleCountBytes proves the size check
// subtracts the chunk being replaced; otherwise retries would slowly
// exhaust a share's budget.
func TestAppend_OverwriteDoesNotDoubleCountBytes(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.MaxChunkBytes = 32
		c.MaxShareBytes = 40
	})
	created := h.create()
	body := bytes.Repeat([]byte("x"), 30)
	h.appendChunk(created, 0, body)
	h.appendChunk(created, 0, body)
	h.appendChunk(created, 0, body)
}

func TestCreate_IsRateLimited(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.CreatePerHour = 1
		c.CreateBurst = 2
	})
	for i := range 2 {
		if rec := h.do(http.MethodPost, "/s", nil, ""); rec.Code != http.StatusCreated {
			t.Fatalf("create %d: status %d, want 201", i, rec.Code)
		}
	}
	if rec := h.do(http.MethodPost, "/s", nil, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429 once the burst is spent", rec.Code)
	}
	// The bucket refills with time.
	h.now = h.now.Add(2 * time.Hour)
	if rec := h.do(http.MethodPost, "/s", nil, ""); rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201 after the bucket refills", rec.Code)
	}
}

func TestNew_RequiresStore(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New succeeded without a store")
	}
}

func TestViewer(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<html>app</html>")},
		"assets/app.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
		"robots.txt":     &fstest.MapFile{Data: []byte("User-agent: *")},
		"dashboard.html": &fstest.MapFile{Data: []byte("secret")},
	}
	h := newHarness(t, func(c *Config) { c.Assets = fs.FS(assets) })
	created := h.create()

	t.Run("serves the app shell for a share url", func(t *testing.T) {
		rec := h.do(http.MethodGet, "/v/"+created.ID, nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html>app</html>") {
			t.Fatalf("body = %q, want the app shell", rec.Body)
		}
	})

	t.Run("serves bundled assets", func(t *testing.T) {
		if rec := h.do(http.MethodGet, "/assets/app.js", nil, ""); rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200", rec.Code)
		}
	})

	t.Run("rejects a malformed share id", func(t *testing.T) {
		if rec := h.do(http.MethodGet, "/v/nope", nil, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", rec.Code)
		}
	})

	// Only the viewer route falls back to the shell. Other SPA routes
	// bundled in the same build must not be reachable here.
	t.Run("does not fall back for other routes", func(t *testing.T) {
		for _, p := range []string{"/", "/dashboard", "/settings"} {
			if rec := h.do(http.MethodGet, p, nil, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status %d, want 404", p, rec.Code)
			}
		}
	})
}

func TestHealthz(t *testing.T) {
	h := newHarness(t, nil)
	if rec := h.do(http.MethodGet, "/healthz", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

func TestClientKey_TrustProxy(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		forwarded  string
		want       string
	}{
		{name: "untrusted proxy header is ignored", forwarded: "203.0.113.9", want: "192.0.2.1"},
		{name: "trusted proxy header is used", trustProxy: true, forwarded: "203.0.113.9, 70.41.3.18", want: "203.0.113.9"},
		{name: "trusted but absent falls back to peer", trustProxy: true, want: "192.0.2.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(c *Config) { c.TrustProxy = tc.trustProxy })
			req := httptest.NewRequest(http.MethodPost, "/s", nil)
			req.RemoteAddr = "192.0.2.1:1234"
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			if got := h.srv.clientKey(req); got != tc.want {
				t.Fatalf("clientKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"},
		{"Basic abc", ""},
		{"Bearer", ""},
		{"", ""},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		if got := bearerToken(req); got != tc.want {
			t.Fatalf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}
