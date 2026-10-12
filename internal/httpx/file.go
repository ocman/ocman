package httpx

import (
	"net/http"
	"strings"
)

// InertFileCSP sandboxes a served file with every capability off: an opaque
// origin and no script, whatever the embedding iframe allows.
const InertFileCSP = "sandbox; frame-ancestors 'self'"

// SetInertFileHeaders neutralises active content (SVG and HTML can carry
// script) and lets only the dashboard itself frame the file, replacing the
// global X-Frame-Options: DENY so HTML previews can render in an iframe.
func SetInertFileHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", InertFileCSP)
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
}

// FileDisposition reports whether a content type is one browsers render
// usefully in place ("inline") or should just download ("attachment").
func FileDisposition(ctype string) string {
	base, _, _ := strings.Cut(ctype, ";")
	base = strings.TrimSpace(base)
	switch {
	case strings.HasPrefix(base, "image/"),
		strings.HasPrefix(base, "video/"),
		strings.HasPrefix(base, "audio/"),
		strings.HasPrefix(base, "text/"),
		base == "application/pdf",
		base == "application/json":
		return "inline"
	}
	return "attachment"
}
