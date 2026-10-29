package relay

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
)

// The same public metadata describes every share. Never derive a preview from
// encrypted content, query parameters or the decryption key in the fragment.
const viewerMetadata = `
<meta property="og:title" content="Shared content on ocman" />
<meta property="og:description" content="View encrypted shared content on ocman." />
<meta property="og:type" content="website" />
<meta property="og:site_name" content="ocman" />
<meta property="og:url" content="%[1]s" />
<meta property="og:image" content="%[2]s" />
<meta property="og:image:type" content="image/png" />
<meta property="og:image:width" content="600" />
<meta property="og:image:height" content="600" />
<meta property="og:image:alt" content="ocman logo" />
<meta name="twitter:card" content="summary" />
<meta name="twitter:title" content="Shared content on ocman" />
<meta name="twitter:description" content="View encrypted shared content on ocman." />
<meta name="twitter:image" content="%[2]s" />
<meta name="twitter:image:alt" content="ocman logo" />
`

// handleViewer serves the app shell with metadata available without JavaScript.
// The relay never receives the URL fragment containing the decryption key.
func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	if !validID(r.PathValue("id")) {
		http.Error(w, "share not found", http.StatusNotFound)
		return
	}
	index, err := fs.ReadFile(s.cfg.Assets, "index.html")
	if err != nil {
		http.Error(w, "viewer is not available", http.StatusNotFound)
		return
	}
	scheme := "http"
	if r.TLS != nil || (s.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	page := url.URL{Scheme: scheme, Host: r.Host, Path: "/v/" + r.PathValue("id")}
	image := url.URL{Scheme: scheme, Host: r.Host, Path: "/share-preview.png"}
	metadata := fmt.Sprintf(viewerMetadata, html.EscapeString(page.String()), html.EscapeString(image.String()))
	index = bytes.Replace(index, []byte("</head>"), []byte(metadata+"</head>"), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	_, _ = w.Write(index)
}

// Preview crawlers may fetch the public shell and logo, never the share API.
// Search engines remain disallowed, as on the main application's robots.txt.
func (s *Server) handleViewerRobots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, `User-agent: Twitterbot
User-agent: facebookexternalhit
User-agent: Slackbot
User-agent: Discordbot
User-agent: TelegramBot
User-agent: WhatsApp
User-agent: LinkedInBot
Allow: /v/
Allow: /share-preview.png
Disallow: /

User-agent: *
Disallow: /
`)
}
