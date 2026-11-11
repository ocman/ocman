package relayauth

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed assets/*
var pageAssets embed.FS

var page = template.Must(template.ParseFS(pageAssets, "assets/page.html"))

type pageData struct {
	ID        string
	Providers []Provider
	Failed    bool
}

func renderPage(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if data.Failed {
		// A provider rejection is not a failed gateway. Proxies may replace 502
		// responses, hiding the retry instructions from the browser.
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = page.Execute(w, data)
}

func servePageAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	var contentType string
	switch name {
	case "style.css":
		contentType = "text/css; charset=utf-8"
	case "logo.svg":
		contentType = "image/svg+xml"
	default:
		http.NotFound(w, r)
		return
	}
	data, _ := pageAssets.ReadFile("assets/" + name)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}
