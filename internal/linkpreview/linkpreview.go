// Package linkpreview discovers previewable resources in conversation text
// and resolves them, per viewer, into normalized preview metadata.
//
// Discovery never trusts a text match as a fetchable URL: a Resolver turns a
// known direct URL or a configured ticket identifier into a Ref holding a
// validated resource ID, and resolution then talks only to that provider's
// fixed API hosts (see API). Results are cached per viewer, owner, provider,
// workspace and resource so one browser's private metadata never reaches
// another.
package linkpreview

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
)

// State is the connect/error state of one preview.
type State string

const (
	StateOK          State = "ok"
	StateConnect     State = "connect"      // viewer has no usable grant
	StateExpired     State = "expired"      // grant expired and cannot refresh
	StateDenied      State = "denied"       // provider returned 403
	StateNotFound    State = "not_found"    // provider returned 404
	StateRateLimited State = "rate_limited" // provider or local limit; retry later
	StateAmbiguous   State = "ambiguous"    // several workspaces could own it
	StateError       State = "error"
)

// Ref identifies one provider resource. It is the preview request.
type Ref struct {
	Provider  string `json:"provider"`
	Workspace string `json:"workspace,omitempty"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	// URL is the canonical resource URL: the matched direct URL on a
	// request, the provider-returned URL on a result. Empty for an
	// unresolved ticket identifier.
	URL string `json:"url,omitempty"`
}

func (r Ref) key() string {
	return strings.Join([]string{r.Provider, r.Workspace, r.Kind, r.ID}, "\x00")
}

// Preview is the normalized preview result.
type Preview struct {
	Ref
	Title     string          `json:"title,omitempty"`
	Status    string          `json:"status,omitempty"`
	Icon      string          `json:"icon,omitempty"` // Bootstrap icon class, e.g. bi-kanban
	Meta      []string        `json:"meta,omitempty"` // short, display-safe facts
	UpdatedAt time.Time       `json:"updatedAt,omitzero"`
	State     State           `json:"state"`
	Stale     bool            `json:"stale,omitempty"` // cached data served while rate limited
	HeadSHA   string          `json:"headSha,omitempty"`
	Checks    *forge.CIStatus `json:"checks,omitempty"`
	// Choices are the candidates for an ambiguous ticket identifier; a
	// resolver returning them makes the preview StateAmbiguous.
	Choices []Choice `json:"choices,omitempty"`
}

// Choice is one provider-returned candidate resource.
type Choice struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Resolver is one preview provider.
type Resolver interface {
	// Provider is the previewauth provider ID whose grants Fetch uses.
	Provider() string
	// APIHosts are the only hosts (host[:port]) Fetch may reach.
	APIHosts() []string
	// ParseURL recognizes a direct resource URL on a known host.
	ParseURL(u *url.URL) (Ref, bool)
	// ParseIdentifier validates a configured ticket identifier (ABC-42).
	ParseIdentifier(id string) (Ref, bool)
	// Fetch loads metadata through api. Errors should be *HTTPError when
	// they come from the provider.
	Fetch(ctx context.Context, api *API, ref Ref) (Preview, error)
}

// Fallback is a Resolver that can preview without the viewer's own grant,
// using the owner machine's credential (env/CLI token, may be empty). Fetch
// then sees API.PublicOnly() unless the request carries WithOwnerAccess, and
// returns ErrNeedsGrant (or a 404) for anything that is not public.
type Fallback interface {
	Resolver
	FallbackToken() string
}

// ErrNeedsGrant: the resource is private and the viewer may connect.
var ErrNeedsGrant = errors.New("preview needs the viewer's own grant")

type ownerAccessKey struct{}

// WithOwnerAccess marks a request made by the owner machine's own user, who
// may see private resources through the owner credential of a Fallback.
func WithOwnerAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownerAccessKey{}, true)
}

func ownerAccess(ctx context.Context) bool {
	v, _ := ctx.Value(ownerAccessKey{}).(bool)
	return v
}

// IdentifierRule routes a text pattern's matches (capture group 1 when
// present, else the whole match) to a provider. The rule's own replacement
// link remains the client-side fallback when the provider cannot resolve it.
type IdentifierRule struct {
	Pattern  string
	Provider string
}

// MaxRefs bounds how many resources one text may preview.
const MaxRefs = 20

// maxCandidates bounds the matches examined per pattern, so a huge
// transcript full of unrelated links costs a fixed amount of work.
const maxCandidates = 2000

var urlPattern = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)

// Discover finds up to MaxRefs distinct resources in text: direct URLs
// first, then identifier-rule matches, deduplicated by resource.
func Discover(text string, resolvers []Resolver, rules []IdentifierRule) []Ref {
	var refs []Ref
	seen := map[string]bool{}
	add := func(ref Ref, ok bool) bool {
		if ok && !seen[ref.key()] {
			seen[ref.key()] = true
			refs = append(refs, ref)
		}
		return len(refs) < MaxRefs
	}
	for _, raw := range urlPattern.FindAllString(text, maxCandidates) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)]}"))
		if err != nil || u.User != nil {
			continue
		}
		for _, r := range resolvers {
			ref, ok := r.ParseURL(u)
			if ok {
				ref.Provider = r.Provider()
				if !add(ref, true) {
					return refs
				}
				break
			}
		}
	}
	byID := map[string]Resolver{}
	for _, r := range resolvers {
		byID[r.Provider()] = r
	}
	for _, rule := range rules {
		r, ok := byID[rule.Provider]
		re, err := regexp.Compile(rule.Pattern)
		if !ok || err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(text, maxCandidates) {
			id := m[0]
			if len(m) > 1 && m[1] != "" {
				id = m[1]
			}
			ref, ok := r.ParseIdentifier(id)
			ref.Provider = r.Provider()
			if !add(ref, ok) {
				return refs
			}
		}
	}
	return refs
}
