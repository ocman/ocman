package linkpreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

var ticketID = regexp.MustCompile(`^[A-Z]{2,10}-[0-9]{1,9}$`)

// mockResolver previews tracker.example.com/browse/KEY-1 through a fixed
// API host (the TLS test server).
type mockResolver struct {
	base   string
	target func(ref Ref) (string, []string) // overrides the API request
}

func (m *mockResolver) Provider() string { return "mock" }
func (m *mockResolver) APIHosts() []string {
	u, _ := url.Parse(m.base)
	return []string{u.Host}
}
func (m *mockResolver) ParseURL(u *url.URL) (Ref, bool) {
	id, ok := strings.CutPrefix(u.Path, "/browse/")
	if u.Hostname() != "tracker.example.com" || !ok {
		return Ref{}, false
	}
	return m.ParseIdentifier(id)
}
func (m *mockResolver) ParseIdentifier(id string) (Ref, bool) {
	if !ticketID.MatchString(id) {
		return Ref{}, false
	}
	return Ref{Kind: "issue", ID: id, URL: "https://tracker.example.com/browse/" + id}, true
}
func (m *mockResolver) Fetch(ctx context.Context, api *API, ref Ref) (Preview, error) {
	base, segs := m.base, []string{"issues", ref.ID}
	if m.target != nil {
		base, segs = m.target(ref)
	}
	var body struct{ Title, URL, Icon string }
	if err := api.JSON(ctx, http.MethodGet, base, segs, nil, nil, &body); err != nil {
		return Preview{}, err
	}
	return Preview{Ref: Ref{URL: body.URL}, Title: body.Title, Icon: body.Icon, Status: "Open", Meta: []string{"Team A"}}, nil
}

type fakeTokens struct {
	mu      sync.Mutex
	grants  map[string]string // viewer/workspace -> token
	revoked []string
}

func (f *fakeTokens) AccessToken(_ context.Context, viewer, _, _, ws string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.grants[viewer+"/"+ws]; ok {
		return t, nil
	}
	return "", previewauth.ErrNotConnected
}
func (f *fakeTokens) Revoked(_ context.Context, viewer, _, _, ws string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.grants, viewer+"/"+ws)
	f.revoked = append(f.revoked, viewer+"/"+ws)
	return nil
}
func (f *fakeTokens) Workspaces(_ context.Context, viewer, _, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ws []string
	for k := range f.grants {
		if v, w, _ := strings.Cut(k, "/"); v == viewer {
			ws = append(ws, w)
		}
	}
	return ws, nil
}

type harness struct {
	srv    *httptest.Server
	svc    *Service
	res    *mockResolver
	tokens *fakeTokens
	hits   atomic.Int32
	status atomic.Int32 // response status override
	seen   sync.Map     // token -> true
}

func newHarness(t *testing.T) *harness {
	h := &harness{tokens: &fakeTokens{grants: map[string]string{"alice/w1": "tok-alice"}}}
	h.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		h.seen.Store(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), true)
		time.Sleep(20 * time.Millisecond)
		if st := h.status.Load(); st != 0 {
			if st == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "30")
			}
			w.WriteHeader(int(st))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/issues/")
		fmt.Fprintf(w, `{"title":"Title of %s by %s","url":"https://tracker.example.com/browse/%s","icon":"bi-kanban"}`,
			id, r.Header.Get("Authorization"), id)
	}))
	t.Cleanup(h.srv.Close)
	h.res = &mockResolver{base: h.srv.URL}
	h.svc = New(h.tokens, h.srv.Client(), h.res)
	return h
}

func (h *harness) one(viewer, id string) Preview {
	return h.svc.Resolve(context.Background(), viewer, "owner", []Ref{{Provider: "mock", Kind: "issue", ID: id}})[0]
}

func TestDiscoverDedupesAndFallsBack(t *testing.T) {
	res := &mockResolver{base: "https://api.example.com"}
	text := "see https://tracker.example.com/browse/ABC-42. and ABC-42, also ABC-7 " +
		"https://evil.example.com/browse/ABC-9 https://u:p@tracker.example.com/browse/ABC-8 " +
		"https://tracker.example.com/browse/../../etc ZZ-1x"
	rules := []IdentifierRule{{Pattern: `\b([A-Z]+-\d+)\b`, Provider: "mock"}, {Pattern: `(`, Provider: "mock"}, {Pattern: `X`, Provider: "nope"}}
	refs := Discover(text, []Resolver{res}, rules)
	var ids []string
	for _, r := range refs {
		ids = append(ids, r.Provider+":"+r.ID)
	}
	// ABC-9 and ABC-8 still match as plain identifiers; the untrusted
	// URLs themselves never become refs with their host.
	if got := strings.Join(ids, ","); got != "mock:ABC-42,mock:ABC-7,mock:ABC-9,mock:ABC-8" {
		t.Fatalf("refs = %s", got)
	}
	for _, r := range refs {
		if r.URL != "https://tracker.example.com/browse/"+r.ID {
			t.Fatalf("ref URL not canonical: %+v", r)
		}
	}
}

func TestDiscoverLargeTranscriptIsBounded(t *testing.T) {
	res := &mockResolver{base: "https://api.example.com"}
	var b strings.Builder
	for i := 0; b.Len() < 900_000; i++ {
		fmt.Fprintf(&b, "line %d https://unrelated.example.org/x/%d ABC-%d\n", i, i, i)
	}
	start := time.Now()
	refs := Discover(b.String(), []Resolver{res}, []IdentifierRule{{Pattern: `ABC-\d+`, Provider: "mock"}})
	if len(refs) != MaxRefs {
		t.Fatalf("refs = %d, want %d", len(refs), MaxRefs)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("discover took %s", d)
	}
}

func TestAPIRejectsUnsafeRequests(t *testing.T) {
	h := newHarness(t)
	n := 0
	for name, target := range map[string]func(Ref) (string, []string){
		"invalid host":   func(Ref) (string, []string) { return "https://evil.example.com", []string{"issues", "A-1"} },
		"http scheme":    func(Ref) (string, []string) { return strings.Replace(h.srv.URL, "https", "http", 1), []string{"issues"} },
		"traversal":      func(Ref) (string, []string) { return h.srv.URL, []string{"issues", ".."} },
		"slash in id":    func(Ref) (string, []string) { return h.srv.URL, []string{"issues", "a/../../admin"} },
		"escaped":        func(Ref) (string, []string) { return h.srv.URL, []string{"%2e%2e"} },
		"base traversal": func(Ref) (string, []string) { return h.srv.URL + "/v1/../admin", []string{"x"} },
		"userinfo":       func(Ref) (string, []string) { return "https://u:p@" + strings.TrimPrefix(h.srv.URL, "https://"), []string{"x"} },
	} {
		h.res.target = target
		n++
		if p := h.one("alice", fmt.Sprintf("ABC-%d", n)); p.State != StateError {
			t.Fatalf("%s: state = %s", name, p.State)
		}
	}
	if n := h.hits.Load(); n != 0 {
		t.Fatalf("unsafe requests reached the network: %d", n)
	}
}

func TestAPIDoesNotFollowRedirects(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer redir.Close()
	api := &API{client: New(nil, redir.Client()).client, hosts: map[string]bool{strings.TrimPrefix(redir.URL, "https://"): true}}
	var he *HTTPError
	err := api.JSON(context.Background(), http.MethodGet, redir.URL, []string{"x"}, nil, nil, &struct{}{})
	if !errors.As(err, &he) || he.Status != http.StatusFound || elsewhere.Load() != 0 {
		t.Fatalf("err = %v, elsewhere hits = %d", err, elsewhere.Load())
	}
}

func TestResolveCachesAndDedupesInFlight(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if p := h.one("alice", "ABC-1"); p.State != StateOK || p.Workspace != "w1" {
				t.Errorf("preview = %+v", p)
			}
		})
	}
	wg.Wait()
	p := h.one("alice", "ABC-1")
	if h.hits.Load() != 1 || p.Title != "Title of ABC-1 by Bearer tok-alice" || p.Icon != "bi-kanban" || p.Status != "Open" {
		t.Fatalf("hits = %d preview = %+v", h.hits.Load(), p)
	}
}

func TestResolveIsolatesViewers(t *testing.T) {
	h := newHarness(t)
	if p := h.one("alice", "ABC-1"); p.State != StateOK {
		t.Fatalf("alice = %+v", p)
	}
	for _, viewer := range []string{"bob", ""} {
		if p := h.one(viewer, "ABC-1"); p.State != StateConnect || p.Title != "" {
			t.Fatalf("%q sees %+v", viewer, p)
		}
	}
	h.tokens.grants["bob/w1"] = "tok-bob"
	if p := h.one("bob", "ABC-1"); !strings.Contains(p.Title, "tok-bob") || h.hits.Load() != 2 {
		t.Fatalf("bob = %+v hits = %d", p, h.hits.Load())
	}
}

func TestResolveDisconnectDropsCachedData(t *testing.T) {
	h := newHarness(t)
	h.one("alice", "ABC-1")
	delete(h.tokens.grants, "alice/w1")
	h.tokens.grants["alice/w9"] = "other" // a new workspace must not reuse w1's cache
	if p := h.one("alice", "ABC-1"); p.Workspace != "w9" || !strings.Contains(p.Title, "Bearer other") {
		t.Fatalf("stale data after reconnect: %+v", p)
	}
	delete(h.tokens.grants, "alice/w9")
	if p := h.one("alice", "ABC-1"); p.State != StateConnect || p.Title != "" {
		t.Fatalf("disconnected = %+v", p)
	}
}

func TestResolveProviderErrors(t *testing.T) {
	for status, want := range map[int]State{
		http.StatusForbidden: StateDenied, http.StatusNotFound: StateNotFound,
		http.StatusInternalServerError: StateError, http.StatusMovedPermanently: StateError,
	} {
		h := newHarness(t)
		h.status.Store(int32(status))
		if p := h.one("alice", "ABC-1"); p.State != want || p.Title != "" {
			t.Fatalf("%d: %+v", status, p)
		}
		h.one("alice", "ABC-1") // negative result is cached
		if h.hits.Load() != 1 {
			t.Fatalf("%d: hits = %d", status, h.hits.Load())
		}
	}
}

func TestResolveUnauthorizedRevokesGrant(t *testing.T) {
	h := newHarness(t)
	h.one("alice", "ABC-1")
	h.svc.Purge("alice", "owner", "mock")
	h.status.Store(http.StatusUnauthorized)
	if p := h.one("alice", "ABC-1"); p.State != StateConnect || p.Title != "" {
		t.Fatalf("401 = %+v", p)
	}
	if len(h.tokens.revoked) != 1 {
		t.Fatalf("revoked = %v", h.tokens.revoked)
	}
}

func TestResolveRateLimitBacksOffAndServesStale(t *testing.T) {
	h := newHarness(t)
	clock := time.Now()
	h.svc.now = func() time.Time { return clock }
	h.one("alice", "ABC-1")
	clock = clock.Add(2 * okTTL)
	h.status.Store(http.StatusTooManyRequests)
	if p := h.one("alice", "ABC-1"); p.State != StateOK || !p.Stale {
		t.Fatalf("stale = %+v", p)
	}
	if p := h.one("alice", "ABC-2"); p.State != StateRateLimited {
		t.Fatalf("uncached = %+v", p)
	}
	if h.hits.Load() != 2 {
		t.Fatalf("backoff not honoured: hits = %d", h.hits.Load())
	}
	h.status.Store(0)
	clock = clock.Add(31 * time.Second)
	if p := h.one("alice", "ABC-2"); p.State != StateOK {
		t.Fatalf("after backoff = %+v", p)
	}
}

func TestResolveFetchBudget(t *testing.T) {
	h := newHarness(t)
	for i := range fetchesPerMinute + 5 {
		h.one("alice", fmt.Sprintf("ABC-%d", i))
	}
	if n := h.hits.Load(); n != fetchesPerMinute {
		t.Fatalf("hits = %d", n)
	}
}

func TestResolveAmbiguousWorkspace(t *testing.T) {
	h := newHarness(t)
	h.tokens.grants["alice/w2"] = "tok2"
	if p := h.one("alice", "ABC-1"); p.State != StateAmbiguous || h.hits.Load() != 0 {
		t.Fatalf("ambiguous = %+v", p)
	}
	p := h.svc.Resolve(context.Background(), "alice", "owner", []Ref{{Provider: "mock", Workspace: "w2", Kind: "issue", ID: "ABC-1"}})[0]
	if p.State != StateOK || !strings.Contains(p.Title, "tok2") {
		t.Fatalf("explicit workspace = %+v", p)
	}
}

func TestSanitize(t *testing.T) {
	ref := Ref{Provider: "mock", Kind: "issue", ID: "ABC-1", URL: "https://tracker.example.com/browse/ABC-1"}
	p := sanitize(Preview{
		Ref:   Ref{Provider: "evil", ID: "other", URL: "javascript:alert(1)"},
		Title: strings.Repeat("x", 500) + "\n\x00", Icon: "bi-x\" onload=",
		Meta: []string{"", "a", "b", "c", "d", "e"},
	}, ref)
	if p.Provider != "mock" || p.ID != "ABC-1" || p.URL != ref.URL || p.Icon != "" || len([]rune(p.Title)) != 200 || len(p.Meta) != 4 {
		t.Fatalf("sanitize = %+v", p)
	}
	var choices []Choice
	for range 7 {
		choices = append(choices, Choice{Title: "t", URL: "https://x.test/a"})
	}
	choices[0].URL = "javascript:alert(1)"
	if p := sanitize(Preview{Choices: choices}, ref); p.State != StateAmbiguous || len(p.Choices) != maxChoices || p.Choices[0].URL != "https://x.test/a" {
		t.Fatalf("choices = %+v", p)
	}
}

func TestTokenState(t *testing.T) {
	for err, want := range map[error]State{
		previewauth.ErrNotConnected: StateConnect, previewauth.ErrRevoked: StateConnect,
		previewauth.ErrExpired: StateExpired, errors.New("db"): StateError,
	} {
		if got := tokenState(fmt.Errorf("wrapped: %w", err)); got != want {
			t.Errorf("%v = %s, want %s", err, got, want)
		}
	}
	h := newHarness(t)
	ref := Ref{Provider: "mock", Workspace: "gone", Kind: "issue", ID: "ABC-1"}
	if p := h.svc.Resolve(context.Background(), "alice", "owner", []Ref{ref})[0]; p.State != StateConnect {
		t.Fatalf("explicit disconnected workspace = %+v", p)
	}
}
