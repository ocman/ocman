package forge

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/ocman/ocman/internal/gitexec"
)

// Detect runs `git -C repoRoot remote -v`, classifies each parseable
// remote as GitHub (github.com) or Forgejo (any other host), and returns
// the result sorted by remote name. The hub narrows Forgejo remotes to
// its configured hosts. Returns an empty slice (not an error) when the
// directory has no remotes; returns an error only when git itself
// fails (e.g. repoRoot is not a git repo).
func Detect(ctx context.Context, repoRoot string) ([]Remote, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("forge: Detect requires repoRoot")
	}
	out, err := gitexec.Output(ctx, repoRoot, "remote", "-v")
	if err != nil {
		return nil, fmt.Errorf("git remote -v: %w", err)
	}
	raw := parseGitRemoteV(out)
	return classifyRemotes(raw), nil
}

// rawRemote is one row of `git remote -v` (a name + url pair, with
// the trailing "(fetch)" / "(push)" stripped). Each remote appears
// twice in the raw output; classifyRemotes deduplicates.
type rawRemote struct {
	Name string
	URL  string
}

// parseGitRemoteV parses the verbatim output of `git remote -v`.
// Each non-blank line has the form:
//
//	<name>\t<url> (fetch|push)
//
// Lines that don't match are skipped silently.
func parseGitRemoteV(out string) []rawRemote {
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var rows []rawRemote
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Split off the trailing " (fetch)" / " (push)".
		var trimmed string
		switch {
		case strings.HasSuffix(line, " (fetch)"):
			trimmed = strings.TrimSuffix(line, " (fetch)")
		case strings.HasSuffix(line, " (push)"):
			trimmed = strings.TrimSuffix(line, " (push)")
		default:
			continue
		}
		// Name and URL are separated by whitespace (typically a tab,
		// but git is forgiving on input — be permissive here).
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		rows = append(rows, rawRemote{Name: fields[0], URL: fields[1]})
	}
	return rows
}

// classifyRemotes turns raw rows into typed Remote entries, dropping
// unparseable URLs. The result is sorted by remote name and
// deduplicated (each remote appears once even though `git remote -v`
// emits it twice).
func classifyRemotes(raw []rawRemote) []Remote {
	seen := map[string]bool{}
	out := make([]Remote, 0, len(raw))
	for _, r := range raw {
		if seen[r.Name] {
			continue
		}
		host, repo, ok := parseRemoteURL(r.URL)
		if !ok {
			continue
		}
		t := RemoteTypeForgejo
		if host == "github.com" {
			t = RemoteTypeGitHub
		}
		seen[r.Name] = true
		out = append(out, Remote{
			Name: r.Name,
			Host: host,
			Type: t,
			Repo: repo,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// parseRemoteURL extracts (host, owner/name) from a git remote URL.
// Supports the three forms git accepts:
//
//	https://host/owner/repo[.git]
//	ssh://git@host/owner/repo[.git]
//	[user@]host:owner/repo[.git]
//
// Returns ok=false for shapes we don't recognise (local paths,
// git://, file://, ...).
func parseRemoteURL(raw string) (host, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}

	// SCP-style: "[user@]host:owner/repo[.git]". Split it the way git
	// does, so ocman never attributes a remote to a host git would not
	// contact: the host is everything before the first ':' (minus
	// userinfo), and a '/' before that ':' makes it a local path.
	if !strings.Contains(raw, "://") {
		colon := strings.IndexByte(raw, ':')
		if colon < 0 {
			return "", "", false
		}
		if slash := strings.IndexByte(raw, '/'); slash >= 0 && slash < colon {
			return "", "", false
		}
		host = raw[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		repo = trimRepo(raw[colon+1:])
		if host == "" || repo == "" {
			return "", "", false
		}
		return host, repo, true
	}

	// URL-style.
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Path == "" {
		return "", "", false
	}
	// Only http/https/ssh — anything else (file://, git://) is unsupported.
	switch u.Scheme {
	case "http", "https", "ssh":
	default:
		return "", "", false
	}
	repo = trimRepo(u.Path)
	return u.Host, repo, repo != ""
}

// trimRepo turns "/owner/repo.git" into "owner/repo".
func trimRepo(path string) string {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if !ValidRepo(path) {
		return ""
	}
	return path
}

// ValidRepo reports whether repo is a plain "owner/name" whose parts are
// safe to place in API paths and storage keys.
func ValidRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && validRepoPart(parts[0]) && validRepoPart(parts[1])
}

func validRepoPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	for _, r := range part {
		if !validRepoRune(r) {
			return false
		}
	}
	return true
}

func validRepoRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r)
}
