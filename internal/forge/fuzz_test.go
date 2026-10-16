package forge

import (
	"net/url"
	"strings"
	"testing"
)

// gitSCPHost is how git itself reads a remote without "://": a local path
// when there is no ':' or a '/' comes before the first ':'; otherwise
// everything before the first ':' is [user@]host.
func gitSCPHost(raw string) (string, bool) {
	colon := strings.IndexByte(raw, ':')
	if colon < 0 {
		return "", false
	}
	if slash := strings.IndexByte(raw, '/'); slash >= 0 && slash < colon {
		return "", false
	}
	host := raw[:colon]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	return host, true
}

// FuzzParseRemoteURL checks that a parsed remote names the host git would
// contact (so PRs and issues are never fetched from, or attributed to, a
// different forge) and a plain owner/name repository.
func FuzzParseRemoteURL(f *testing.F) {
	for _, s := range []string{
		"git@github.com:o/r.git", "https://github.com/o/r", "ssh://git@github.com/o/r.git",
		"https://evil.com/github.com/o/r", "https://github.com@evil.com/o/r",
		"git@github.com:../r", "file:///tmp/x@github.com:o/r", "a:b@github.com:o/r",
		"./x@github.com:o/r", " git@codeberg.org:o/r \n", "http://[::1]:3000/o/r",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		host, repo, ok := parseRemoteURL(raw)
		if !ok {
			return
		}
		parts := strings.Split(repo, "/")
		if len(parts) != 2 {
			t.Fatalf("repo %q from %q is not owner/name", repo, raw)
		}
		for _, p := range parts {
			if p == "." || p == ".." || !validRepoPart(p) {
				t.Fatalf("repo %q from %q has an unsafe component", repo, raw)
			}
		}
		trimmed := strings.TrimSpace(raw)
		if strings.Contains(trimmed, "://") {
			if u, err := url.Parse(trimmed); err != nil || u.Host != host {
				t.Fatalf("host %q from %q, url.Parse says %v (%v)", host, raw, u, err)
			}
			return
		}
		want, remote := gitSCPHost(trimmed)
		if !remote {
			t.Fatalf("git treats %q as a local path, parsed host %q repo %q", raw, host, repo)
		}
		if want != host {
			t.Fatalf("git contacts %q for %q, parsed host %q", want, raw, host)
		}
	})
}
