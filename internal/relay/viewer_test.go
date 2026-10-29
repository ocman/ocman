package relay

import (
	"crypto/tls"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestViewerMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, forwarded, scheme string
		trustProxy, tls         bool
	}{
		{name: "http", scheme: "http"},
		{name: "tls", tls: true, scheme: "https"},
		{name: "trusted proxy", trustProxy: true, forwarded: "https", scheme: "https"},
		{name: "untrusted proxy", forwarded: "https", scheme: "http"},
		{name: "invalid proxy scheme", trustProxy: true, forwarded: "javascript", scheme: "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(c *Config) {
				c.TrustProxy = tc.trustProxy
				c.Assets = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(
					`<html><head><title>ocman</title></head><body><script src="/assets/app.js"></script></body></html>`,
				)}}
			})
			id := h.create().ID
			req := httptest.NewRequest(http.MethodGet, "http://relay.example/v/"+id+"?k=secret&title=private", nil)
			req.Header.Set("X-Forwarded-Proto", tc.forwarded)
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			rec := httptest.NewRecorder()
			h.srv.ServeHTTP(rec, req)
			body := rec.Body.String()
			for _, want := range []string{
				`<meta property="og:title" content="Shared content on ocman"`,
				`<meta property="og:description" content="View encrypted shared content on ocman."`,
				`<meta property="og:type" content="website"`,
				`<meta property="og:site_name" content="ocman"`,
				`<meta property="og:url" content="` + tc.scheme + "://relay.example/v/" + id + `"`,
				`<meta property="og:image" content="` + tc.scheme + `://relay.example/share-preview.png"`,
				`<meta name="twitter:card" content="summary"`,
				`<meta name="twitter:title" content="Shared content on ocman"`,
				`<meta name="twitter:description" content="View encrypted shared content on ocman."`,
				`<meta name="twitter:image" content="` + tc.scheme + `://relay.example/share-preview.png"`,
				`<script src="/assets/app.js"></script>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %s in %s", want, body)
				}
			}
			if strings.Contains(body, "secret") || strings.Contains(body, "private") {
				t.Error("query parameters leaked into viewer HTML")
			}
			if strings.Index(body, `property="og:title"`) > strings.Index(body, "</head>") {
				t.Error("metadata is outside the document head")
			}
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected response: %d %v", rec.Code, rec.Header())
			}
			if rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
				t.Error("viewer must not be indexed")
			}
		})
	}
}

func TestViewerRobots(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Assets = fstest.MapFS{} })
	rec := h.do(http.MethodGet, "/robots.txt", nil, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("robots response: %d %v", rec.Code, rec.Header())
	}
	for _, want := range []string{
		"User-agent: Twitterbot\n", "User-agent: facebookexternalhit\n",
		"User-agent: Slackbot\n", "User-agent: Discordbot\n",
		"User-agent: TelegramBot\n", "User-agent: WhatsApp\n", "User-agent: LinkedInBot\n",
		"Allow: /v/\nAllow: /share-preview.png\nDisallow: /\n",
		"User-agent: *\nDisallow: /\n",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("robots.txt missing %q", want)
		}
	}
}

func TestViewerPreviewImage(t *testing.T) {
	// Use the real public asset so the advertised image cannot silently disappear.
	h := newHarness(t, func(c *Config) { c.Assets = os.DirFS("../../frontend/public") })
	rec := h.do(http.MethodGet, "/share-preview.png", nil, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image response: %d %v", rec.Code, rec.Header())
	}
	img, err := png.DecodeConfig(rec.Body)
	if err != nil || img.Width != 600 || img.Height != 600 {
		t.Fatalf("preview image dimensions: %+v, %v", img, err)
	}
}

func TestViewerMetadataEscapesHost(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Assets = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<head></head>")}}
	})
	req := httptest.NewRequest(http.MethodGet, "/v/"+h.create().ID, nil)
	req.Host = `relay.example"><script>alert(1)</script>`
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("unescaped request host in HTML: %s", rec.Body)
	}
}

func TestViewerMissingAssets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		assets fstest.MapFS
	}{
		{name: "API only"},
		{name: "missing shell", assets: fstest.MapFS{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(c *Config) {
				if tc.assets != nil {
					c.Assets = tc.assets
				}
			})
			if rec := h.do(http.MethodGet, "/v/"+h.create().ID, nil, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("viewer status = %d, want 404", rec.Code)
			}
			if rec := h.do(http.MethodGet, "/share-preview.png", nil, ""); rec.Code != http.StatusNotFound {
				t.Fatalf("image status = %d, want 404", rec.Code)
			}
		})
	}
}
