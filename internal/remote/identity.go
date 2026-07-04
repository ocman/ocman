package remote

import (
	"path/filepath"
	"strings"
)

// ProjectIdentity describes one project known to a host, used for the
// new-session machine-picker matching (FR-12 / AD-9). Key is the
// normalized identity used for cross-host matching; Origin/Basename are
// kept for display and fallback; Dir is the absolute path on the owning
// host.
type ProjectIdentity struct {
	Key      string `json:"key"`
	Origin   string `json:"origin"`
	Basename string `json:"basename"`
	Dir      string `json:"dir"`

	// Aggregate stats carried across the wire so the hub's project list
	// can show real counts for remote projects (not just identities).
	SessionCount   int     `json:"sessionCount,omitempty"`
	MessageCount   int     `json:"messageCount,omitempty"`
	LastUsed       int64   `json:"lastUsed,omitempty"`
	TotalTokensIn  int64   `json:"totalTokensIn,omitempty"`
	TotalTokensOut int64   `json:"totalTokensOut,omitempty"`
	TotalCost      float64 `json:"totalCost,omitempty"`
}

// NormalizeProjectIdentity computes the matching key for a project from
// its git origin URL, falling back to a basename key when there is no
// origin (AD-9). Normalization: strip a trailing ".git", canonicalize
// ssh (git@host:org/repo) and https (https://host/org/repo) forms to
// "host/org/repo", and lowercase the host. Matching is exact on the key.
//
//	git@github.com:Org/Repo.git   -> github.com/org/repo
//	https://github.com/Org/Repo   -> github.com/org/repo
//	ssh://git@host:22/org/repo.git -> host/org/repo
//	(no origin), dir=/x/y/myapp    -> basename:myapp
func NormalizeProjectIdentity(origin, dir string) string {
	o := strings.TrimSpace(origin)
	if o == "" {
		return "basename:" + filepath.Base(strings.TrimRight(dir, "/"))
	}
	o = strings.TrimSuffix(o, ".git")

	// scp-like syntax: git@host:org/repo
	if !strings.Contains(o, "://") {
		if at := strings.LastIndex(o, "@"); at >= 0 {
			o = o[at+1:]
		}
		// host:org/repo -> host/org/repo
		o = strings.Replace(o, ":", "/", 1)
		return normalizeHostPath(o)
	}

	// URL form: scheme://[user@]host[:port]/path
	rest := o[strings.Index(o, "://")+3:]
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	// Split host[:port] from path.
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return normalizeHostPath(rest)
	}
	hostPort := rest[:slash]
	path := rest[slash:]
	if colon := strings.Index(hostPort, ":"); colon >= 0 {
		hostPort = hostPort[:colon] // drop port
	}
	return normalizeHostPath(hostPort + path)
}

// normalizeHostPath lowercases and trims a "host/org/repo" string.
func normalizeHostPath(s string) string {
	s = strings.Trim(s, "/")
	return strings.ToLower(s)
}

// basenameOf returns the final path element of an absolute directory,
// trimming any trailing slash.
func basenameOf(dir string) string {
	return filepath.Base(strings.TrimRight(dir, "/"))
}
