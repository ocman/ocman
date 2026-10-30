package httpx

import "strings"

const BrowserCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self' data:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; manifest-src 'self'"

// BrowserSecurityPolicy scopes WS sources to the serving authority for browsers
// that do not match WebSockets against 'self'. Never trust forwarded headers or
// broaden to ws:. Invalid authorities retain the restrictive base policy.
func BrowserSecurityPolicy(host string) string {
	u, ok := BrowserResourceURL("http://" + host)
	if !ok || u.Host != host || u.RequestURI() != "/" || u.Fragment != "" {
		return BrowserCSP
	}
	return strings.Replace(BrowserCSP, "connect-src 'self';",
		"connect-src 'self' ws://"+host+" wss://"+host+";", 1)
}
