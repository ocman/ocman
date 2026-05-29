package forgejo

import (
	log "github.com/sirupsen/logrus"
)

// Registry maps Forgejo hostnames to authenticated clients. It is
// built once at server startup from the user's `tea` config (plus
// any env-var token overrides). Reconstruction on tea-config changes
// is out of scope for v1 — restart ocman to pick up new logins.
//
// Lookups are by hostname so the upstream-detection layer (which
// already knows the host of each remote) can find the right client
// without re-parsing URLs.
type Registry struct {
	clients map[string]*Client
}

// NewRegistry reads the tea config (TeaLogins) and constructs one
// Client per discovered host. Logins that share a host collapse to
// the first occurrence (tea allows multiple logins per host; ocman
// picks the first per A-3 in spec/pr-issue-sidebar/requirements.md).
//
// Returns an empty registry (not an error) when tea is not configured;
// callers treat that as "no Forgejo hosts available".
func NewRegistry() *Registry {
	r := &Registry{clients: map[string]*Client{}}
	logins, err := TeaLogins()
	if err != nil {
		log.WithError(err).Warn("forgejo: failed to read tea logins; no Forgejo hosts available")
		return r
	}
	for _, l := range logins {
		if _, exists := r.clients[l.Host]; exists {
			log.WithField("host", l.Host).
				WithField("login", l.Name).
				Debug("forgejo: duplicate login for host, keeping first")
			continue
		}
		r.clients[l.Host] = NewClient(l.Host, l.URL, l.Token)
	}
	if len(r.clients) > 0 {
		log.WithField("hosts", len(r.clients)).Info("forgejo: registry initialised")
	}
	return r
}

// ForHost returns the client for the given host, or nil when no
// client is configured. Callers must nil-check before use; the
// upstream-detection layer skips remotes whose host isn't in the
// registry, so by the time a handler dispatches the result here it
// should always be non-nil.
func (r *Registry) ForHost(host string) *Client {
	if r == nil {
		return nil
	}
	return r.clients[host]
}

// Knows reports whether the registry has a client for host. Used by
// upstream detection to classify a git remote as "forgejo" vs.
// "unsupported".
func (r *Registry) Knows(host string) bool {
	if r == nil {
		return false
	}
	_, ok := r.clients[host]
	return ok
}

// Hosts returns the sorted list of known hosts. Stable order so
// listing in logs / debug output is deterministic.
func (r *Registry) Hosts() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.clients))
	for h := range r.clients {
		out = append(out, h)
	}
	// Tiny insertion sort — len(out) is the number of Forgejo hosts
	// configured, which is realistically 0-2.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
