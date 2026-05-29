// Package forge provides forge-agnostic types and the Forge interface
// that the PR/Issue sidebar handlers code against. Per-forge clients
// (GitHub, Forgejo) live in internal/integrations/<name> and expose
// adapters that satisfy this interface.
//
// The package intentionally has no HTTP code: it owns types and a
// few pure helpers (the prompt-template renderer, upstream detection).
// All network IO is the caller's responsibility, supplied via the
// Forge interface.
package forge

import (
	"context"
	"errors"
	"time"
)

// RemoteType identifies which kind of forge a git remote points at.
type RemoteType string

const (
	RemoteTypeGitHub  RemoteType = "github"
	RemoteTypeForgejo RemoteType = "forgejo"
)

// Remote describes one git remote of a project, classified by host
// type and parsed into the owner/name pair used by forge APIs.
type Remote struct {
	// Name is the local git remote name (e.g. "origin", "upstream").
	Name string `json:"remote"`
	// URL is the raw URL from `git remote -v` (HTTPS or SSH form).
	URL string `json:"url,omitempty"`
	// Host is the parsed host (e.g. "github.com", "code.example.com").
	Host string `json:"host"`
	// Type is the forge classification.
	Type RemoteType `json:"type"`
	// Repo is "owner/name", the canonical form forge APIs use.
	Repo string `json:"repo"`
}

// Label is a forge label attached to a PR or Issue.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// User is a forge user reference (author, assignee, reviewer).
type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// PR is the normalised shape of a pull/merge request returned by
// any Forge implementation.
type PR struct {
	Number             int       `json:"number"`
	Title              string    `json:"title"`
	Body               string    `json:"body"`
	Author             string    `json:"author"`
	Status             string    `json:"status"` // "open" | "draft" | "merged" | "closed"
	UpdatedAt          time.Time `json:"updatedAt"`
	Labels             []Label   `json:"labels"`
	Assignees          []User    `json:"assignees"`
	RequestedReviewers []User    `json:"requestedReviewers"`
	Branch             string    `json:"branch"`
	URL                string    `json:"url"`
	Host               string    `json:"host"`
	Repo               string    `json:"repo"`
	// CrossFork is true when the PR's head repo differs from the
	// base repo. Used by FR-9a to gate the worktree-launch
	// confirmation prompt.
	CrossFork bool `json:"crossFork"`
}

// Issue is the normalised shape of an issue returned by any Forge
// implementation. Issues have no source branch and no requested
// reviewers, so those PR fields are omitted here.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	Status    string    `json:"status"` // "open" | "closed"
	UpdatedAt time.Time `json:"updatedAt"`
	Labels    []Label   `json:"labels"`
	Assignees []User    `json:"assignees"`
	URL       string    `json:"url"`
	Host      string    `json:"host"`
	Repo      string    `json:"repo"`
}

// CurrentUser identifies the authenticated user on a given host.
// Used to evaluate the "mine" filter (author OR assignee OR (PR)
// requested reviewer).
type CurrentUser struct {
	Host  string `json:"host"`
	Login string `json:"login"`
}

// ListOptions controls how a Forge lists PRs or Issues.
type ListOptions struct {
	// State is one of "open", "closed", "all". An empty string is
	// treated as "open" by adapters.
	State string
	// Mine, when non-empty, narrows the list to items where this
	// login is the author, an assignee, or (for PRs) a requested
	// reviewer. Adapters may delegate to forge-side filters where
	// available and post-filter otherwise.
	Mine string
	// Page is 1-based. 0 is treated as 1 by adapters.
	Page int
	// PerPage caps page size. 0 is treated as a sane default
	// (typically 30) by adapters.
	PerPage int
}

// RateLimit captures rate-limit metadata returned by a forge so the
// frontend can show a live countdown (FR-12). An adapter sets
// Limited=true when the forge returned a 429 or a near-zero remaining
// quota; otherwise Limited stays false and ResetAt is the zero value.
type RateLimit struct {
	Limited bool      `json:"limited"`
	ResetAt time.Time `json:"resetAt,omitempty"`
}

// Forge is the abstraction handlers code against. Two implementations
// exist in v1: a GitHub adapter wrapping internal/integrations/github,
// and a Forgejo adapter wrapping internal/integrations/forgejo.
//
// All methods accept a context for cancellation/deadline propagation.
// Implementations are expected to be safe for concurrent use.
type Forge interface {
	// Host returns the canonical host this forge targets, matching
	// the Host field of the Remote(s) it serves.
	Host() string

	// Authenticated reports whether the forge has a credential.
	// Adapters with no token may still serve public resources but
	// callers typically gate "mine" / private-only flows on this.
	Authenticated() bool

	// ListPRs returns one page of pull/merge requests for the
	// repo (owner/name).
	ListPRs(ctx context.Context, repo string, opts ListOptions) ([]PR, RateLimit, error)

	// ListIssues returns one page of issues for the repo.
	ListIssues(ctx context.Context, repo string, opts ListOptions) ([]Issue, RateLimit, error)

	// CurrentUser returns the authenticated user's login. Returns
	// ErrUnauthenticated when no credential is configured.
	CurrentUser(ctx context.Context) (CurrentUser, error)

	// FetchPRHead fetches the PR head ref into a deterministic
	// local branch ("ocman/pr-<n>") on the given repoRoot, using
	// the configured remoteName. Idempotent: re-running on the
	// same PR updates the local branch in place. Returns the
	// local branch name on success.
	FetchPRHead(ctx context.Context, repoRoot, remoteName string, prNumber int) (branch string, err error)
}

// ErrUnauthenticated is returned by CurrentUser (and may be returned
// by other methods) when the forge has no credential. Handlers turn
// this into a 401-shaped error envelope on the wire.
var ErrUnauthenticated = errors.New("forge: not authenticated")

// ErrRateLimited is returned when the forge responded with 429.
// Adapters set the RateLimit.Limited flag in their list-call return
// values; this error is reserved for methods that don't return a
// RateLimit (e.g. CurrentUser, FetchPRHead).
var ErrRateLimited = errors.New("forge: rate limited")
