package httpx

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
)

var browserHostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?\.?$`)

// BrowserResourceURL accepts absolute HTTP(S) URLs with a CSP-safe authority.
// It does not resolve DNS or prevent SSRF: callers must not use it to authorize
// server-side fetches. It is for browser resources after explicit user consent.
func BrowserResourceURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Opaque != "" || u.Host == "" {
		return nil, false
	}
	host := u.Hostname()
	if host == "" || (net.ParseIP(host) == nil && (len(host) > 253 || !browserHostname.MatchString(host))) {
		return nil, false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, false
		}
	} else if u.Host[len(u.Host)-1] == ':' {
		return nil, false
	}
	return u, true
}
