package previews

import (
	_ "embed"
	"html/template"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
)

// This wrapper never fetches on the server. Its sole external capability is an
// image request from an opaque, script-free browser frame after UI consent.
//
//go:embed external_image.html
var externalImageHTML string

var externalImagePage = template.Must(template.New("image").Parse(externalImageHTML))

func (*Module) handleExternalImage(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("url")
	u, ok := httpx.BrowserResourceURL(raw)
	if !ok || len(raw) > 8192 {
		http.Error(w, "image URL must be an absolute HTTP(S) URL without credentials", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src "+u.Scheme+"://"+u.Host+"; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'")
	_ = externalImagePage.Execute(w, u.String())
}
