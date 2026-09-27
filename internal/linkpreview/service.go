package linkpreview

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Tokens supplies a viewer's grants; *previewauth.Manager implements it.
type Tokens interface {
	AccessToken(ctx context.Context, viewerID, ownerID, provider, workspace string) (string, error)
	Revoked(ctx context.Context, viewerID, ownerID, provider, workspace string) error
	Workspaces(ctx context.Context, viewerID, ownerID, provider string) ([]string, error)
}

const (
	okTTL        = time.Minute
	errorTTL     = 15 * time.Second
	fetchTimeout = 10 * time.Second
	maxInFlight  = 4
	maxEntries   = 4096
	// Per viewer+provider+workspace outbound fetch budget.
	fetchesPerMinute = 60
	defaultBackoff   = time.Minute
	maxBackoff       = 15 * time.Minute
)

type entry struct {
	p       Preview
	expires time.Time
}

type window struct {
	start time.Time
	n     int
}

// Service resolves Refs for a viewer with caching, in-flight dedup, bounded
// concurrency, a per-grant fetch budget and 429 backoff.
type Service struct {
	tokens    Tokens
	client    *http.Client
	resolvers []Resolver
	byID      map[string]Resolver
	now       func() time.Time
	sem       chan struct{}
	group     singleflight.Group

	mu      sync.Mutex
	cache   map[string]entry
	backoff map[string]time.Time
	budget  map[string]window
}

// New returns a Service. client is copied and never follows redirects.
func New(tokens Tokens, client *http.Client, resolvers ...Resolver) *Service {
	c := &http.Client{Timeout: fetchTimeout}
	if client != nil {
		copied := *client
		c = &copied
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s := &Service{
		tokens: tokens, client: c, resolvers: resolvers, byID: map[string]Resolver{}, now: time.Now,
		sem: make(chan struct{}, maxInFlight), cache: map[string]entry{}, backoff: map[string]time.Time{}, budget: map[string]window{},
	}
	for _, r := range resolvers {
		s.byID[r.Provider()] = r
	}
	return s
}

// Discover finds resources in text using this service's resolvers.
func (s *Service) Discover(text string, rules []IdentifierRule) []Ref {
	return Discover(text, s.resolvers, rules)
}

// Resolve returns one Preview per ref, in order. An empty viewerID (no
// browser identity) resolves nothing private and reports StateConnect.
func (s *Service) Resolve(ctx context.Context, viewerID, ownerID string, refs []Ref) []Preview {
	out := make([]Preview, len(refs))
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Go(func() { out[i] = s.resolve(ctx, viewerID, ownerID, ref) })
	}
	wg.Wait()
	return out
}

func grantKey(viewerID, ownerID, provider, workspace string) string {
	return strings.Join([]string{viewerID, ownerID, provider, workspace}, "\x00")
}

func (s *Service) resolve(ctx context.Context, viewerID, ownerID string, ref Ref) Preview {
	r, ok := s.byID[ref.Provider]
	if !ok {
		return Preview{Ref: ref, State: StateError}
	}
	if viewerID == "" {
		return Preview{Ref: ref, State: StateConnect}
	}
	if ref.Workspace == "" {
		ws, err := s.tokens.Workspaces(ctx, viewerID, ownerID, ref.Provider)
		switch {
		case err != nil:
			return Preview{Ref: ref, State: StateError}
		case len(ws) == 0:
			return Preview{Ref: ref, State: StateConnect}
		case len(ws) > 1:
			return Preview{Ref: ref, State: StateAmbiguous}
		}
		ref.Workspace = ws[0]
	}
	gk := grantKey(viewerID, ownerID, ref.Provider, ref.Workspace)
	// The grant is checked before the cache, so a disconnected or revoked
	// viewer never sees data cached while it was connected.
	token, err := s.tokens.AccessToken(ctx, viewerID, ownerID, ref.Provider, ref.Workspace)
	if err != nil {
		s.Purge(viewerID, ownerID, ref.Provider)
		return Preview{Ref: ref, State: tokenState(err)}
	}
	key := gk + "\x00" + ref.key()
	now := s.now()
	s.mu.Lock()
	cached, hit := s.cache[key]
	fresh := hit && now.Before(cached.expires)
	limited := !fresh && (now.Before(s.backoff[gk]) || !s.spend(gk, now))
	s.mu.Unlock()
	if fresh {
		return cached.p
	}
	if limited {
		return staleOr(cached, hit, ref)
	}
	v, _, _ := s.group.Do(key, func() (any, error) {
		return s.fetch(ctx, r, token, viewerID, ownerID, gk, key, ref), nil
	})
	p := v.(Preview)
	if p.State == StateRateLimited {
		return staleOr(cached, hit, ref)
	}
	return p
}

// spend takes one fetch from the grant's per-minute budget. Caller holds mu.
func (s *Service) spend(gk string, now time.Time) bool {
	w := s.budget[gk]
	if now.Sub(w.start) >= time.Minute {
		w = window{start: now}
	}
	if w.n >= fetchesPerMinute {
		return false
	}
	w.n++
	s.budget[gk] = w
	return true
}

func staleOr(e entry, hit bool, ref Ref) Preview {
	if hit && e.p.State == StateOK {
		p := e.p
		p.Stale = true
		return p
	}
	return Preview{Ref: ref, State: StateRateLimited}
}

func tokenState(err error) State {
	switch {
	case errors.Is(err, previewauth.ErrNotConnected), errors.Is(err, previewauth.ErrRevoked):
		return StateConnect
	case errors.Is(err, previewauth.ErrExpired):
		return StateExpired
	}
	return StateError
}

func (s *Service) fetch(ctx context.Context, r Resolver, token, viewerID, ownerID, gk, key string, ref Ref) Preview {
	// Detached from the caller so a shared in-flight fetch survives one
	// caller going away, but still bounded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
	defer cancel()
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return Preview{Ref: ref, State: StateError}
	}
	hosts := map[string]bool{}
	for _, h := range r.APIHosts() {
		hosts[strings.ToLower(h)] = true
	}
	p, err := r.Fetch(ctx, &API{client: s.client, token: token, hosts: hosts}, ref)
	var he *HTTPError
	errors.As(err, &he)
	switch {
	case err == nil:
		p = sanitize(p, ref)
	case he != nil && he.Status == http.StatusUnauthorized:
		_ = s.tokens.Revoked(ctx, viewerID, ownerID, ref.Provider, ref.Workspace)
		s.Purge(viewerID, ownerID, ref.Provider)
		return Preview{Ref: ref, State: StateConnect}
	case he != nil && he.Status == http.StatusTooManyRequests:
		wait := min(max(he.RetryAfter, time.Second), maxBackoff)
		if he.RetryAfter <= 0 {
			wait = defaultBackoff
		}
		s.mu.Lock()
		s.backoff[gk] = s.now().Add(wait)
		s.mu.Unlock()
		return Preview{Ref: ref, State: StateRateLimited}
	case he != nil && he.Status == http.StatusForbidden:
		p = Preview{Ref: ref, State: StateDenied}
	case he != nil && he.Status == http.StatusNotFound:
		p = Preview{Ref: ref, State: StateNotFound}
	default:
		p = Preview{Ref: ref, State: StateError}
	}
	ttl := errorTTL
	if p.State == StateOK {
		ttl = okTTL
	}
	s.mu.Lock()
	if len(s.cache) >= maxEntries {
		// ponytail: wholesale reset at the cap; LRU if hit rates matter.
		s.cache = map[string]entry{}
	}
	s.cache[key] = entry{p: p, expires: s.now().Add(ttl)}
	s.mu.Unlock()
	return p
}

// Purge drops cached previews matching viewer, owner and provider; an empty
// argument matches any value (Purge("", owner, "") forgets a whole owner).
func (s *Service) Purge(viewerID, ownerID, provider string) {
	want := []string{viewerID, ownerID, provider}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cache {
		parts := strings.SplitN(k, "\x00", 4)
		match := true
		for i, w := range want {
			match = match && (w == "" || parts[i] == w)
		}
		if match {
			delete(s.cache, k)
		}
	}
}

var iconPattern = regexp.MustCompile(`^bi-[a-z0-9-]{1,40}$`)

func short(s string, n int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// sanitize keeps the request's identity and bounds everything else to
// short display-safe text and an http(s) URL.
func sanitize(p Preview, ref Ref) Preview {
	out := Preview{
		Ref: ref, Title: short(p.Title, 200), Status: short(p.Status, 40),
		UpdatedAt: p.UpdatedAt, State: StateOK,
	}
	if u, err := url.Parse(p.URL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
		out.URL = u.String()
	}
	if iconPattern.MatchString(p.Icon) {
		out.Icon = p.Icon
	}
	for _, m := range p.Meta {
		if m = short(m, 80); m != "" && len(out.Meta) < 4 {
			out.Meta = append(out.Meta, m)
		}
	}
	return out
}
