package forge

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// ForgejoHostMap is the small subset of forgejo.Registry that
// detection needs. Defined as an interface so this package stays
// import-free of the per-forge clients.
type ForgejoHostMap interface {
	// Knows reports whether host is a configured Forgejo host. Used
	// to classify a git remote as "forgejo" rather than "unsupported".
	Knows(host string) bool
}

// Detect runs `git -C repoRoot remote -v`, classifies each remote
// into a supported forge (or drops it), and returns the result sorted
// by remote name. Returns an empty slice (not an error) when the
// directory has no remotes; returns an error only when git itself
// fails (e.g. repoRoot is not a git repo).
func Detect(ctx context.Context, repoRoot string, hosts ForgejoHostMap) ([]Remote, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("forge: Detect requires repoRoot")
	}
	out, err := gitexec.Output(ctx, repoRoot, "remote", "-v")
	if err != nil {
		return nil, fmt.Errorf("git remote -v: %w", err)
	}
	raw := parseGitRemoteV(out)
	return classifyRemotes(raw, hosts), nil
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
// any unsupported hosts. The result is sorted by remote name and
// deduplicated (each remote appears once even though `git remote -v`
// emits it twice).
func classifyRemotes(raw []rawRemote, hosts ForgejoHostMap) []Remote {
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
		var t RemoteType
		switch {
		case host == "github.com":
			t = RemoteTypeGitHub
		case hosts != nil && hosts.Knows(host):
			t = RemoteTypeForgejo
		default:
			// Unsupported host — drop.
			continue
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
//	git@host:owner/repo[.git]
//
// Returns ok=false for shapes we don't recognise (local paths,
// git://, file://, ...).
func parseRemoteURL(raw string) (host, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}

	// SCP-style: "git@host:owner/repo[.git]"
	if !strings.Contains(raw, "://") && strings.Contains(raw, "@") && strings.Contains(raw, ":") {
		atIdx := strings.Index(raw, "@")
		colonOffset := strings.Index(raw[atIdx+1:], ":")
		if colonOffset < 0 {
			return "", "", false
		}
		colonIdx := atIdx + 1 + colonOffset
		host = raw[atIdx+1 : colonIdx]
		path := raw[colonIdx+1:]
		repo = trimRepo(path)
		return host, repo, host != "" && repo != ""
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
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !validRepoPart(parts[0]) || !validRepoPart(parts[1]) {
		return ""
	}
	return path
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
