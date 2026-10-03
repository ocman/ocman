package remote

import (
	"net/url"
	"strings"
)

// NormalizeUpstream removes transport syntax and credentials, retaining the
// repository path and non-default ports. Local filesystem remotes cannot
// identify a repository across hosts.
func NormalizeUpstream(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		before, path, ok := strings.Cut(raw, ":")
		if !ok || strings.Contains(before, "/") || strings.HasPrefix(path, "/") {
			return ""
		}
		if _, host, ok := strings.Cut(before, "@"); ok {
			before = host
		}
		raw = "ssh://" + before + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	switch u.Scheme {
	case "http", "https", "ssh", "git":
	default:
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	defaultPort := (u.Scheme == "ssh" && port == "22") ||
		(u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") ||
		(u.Scheme == "git" && port == "9418")
	if port != "" && !defaultPort {
		host += ":" + port
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return ""
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ""
		}
	}
	if host == "github.com" {
		path = strings.ToLower(path)
	}
	return host + "/" + path
}
