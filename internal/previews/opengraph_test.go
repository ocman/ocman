package previews

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

type publicFixtureTransport func(*http.Request) (*http.Response, error)

func (f publicFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenGraphEndpointFallbackCacheAndImageDelivery(t *testing.T) {
	providerAPI := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer providerAPI.Close()
	s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: "github", Host: "github.com", APIBase: providerAPI.URL})
	s.Client = providerAPI.Client()
	svc := s.linkPreviews()
	pageHits, imageHits := 0, 0
	image := "\x89PNG\r\n\x1a\n"
	// Replace only the network transport; URL and redirect guards stay installed.
	svc.PublicClient.Transport = publicFixtureTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("credentials reached public fetch")
		}
		body, contentType := image, "image/png"
		if req.URL.Host == "github.com" {
			pageHits++
			body, contentType = `<meta property="og:title" content="Public PR page"><meta property="og:description" content="Description"><meta property="og:image" content="https://images.example/card.png">`, "text/html"
		} else {
			imageHits++
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {contentType}}, Request: req}, nil
	})
	b := newBrowser()
	const target = "https://github.com/o/r/pull/1"
	if got := resolvePreviews(t, b, s, target); len(got) != 1 || got[0].State != linkpreview.StateNotFound || pageHits != 0 {
		t.Fatalf("automatic discovery fetched a page: %+v, hits=%d", got, pageHits)
	}
	for range 2 {
		rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"`+target+`","openGraph":true}`)
		var response struct{ Previews []linkpreview.Preview }
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &response) != nil || len(response.Previews) != 1 {
			t.Fatalf("resolve = %d %s", rr.Code, rr.Body.String())
		}
		p := response.Previews[0]
		if p.Provider != "opengraph" || p.State != linkpreview.StateOK || p.Title != "Public PR page" || p.Description != "Description" || p.URL != target || p.Image != "https://images.example/card.png" {
			t.Fatalf("provider fallback not replaced: %+v", p)
		}
	}
	if pageHits != 1 {
		t.Fatalf("metadata cache missed: hits=%d", pageHits)
	}
	rr := b.do(t, s, http.MethodGet, "/api/previews/image?url=https://images.example/card.png", "")
	if rr.Code != http.StatusOK || rr.Body.String() != image || imageHits != 1 {
		t.Fatalf("image = %d %q, hits=%d", rr.Code, rr.Body.String(), imageHits)
	}
	for name, want := range map[string]string{
		"Content-Type": "image/png", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox", "Cache-Control": "private, max-age=60",
	} {
		if got := rr.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	unsafe := &http.Request{URL: &url.URL{Scheme: "http", Host: "127.0.0.1"}}
	if svc.PublicClient.CheckRedirect(unsafe, nil) == nil {
		t.Fatal("production redirect guard was lost")
	}
}

func TestOpenGraphRequiresAppAccessAndRejectsLocalImages(t *testing.T) {
	s := newHarness(t)
	b := newBrowser()
	if rr := b.do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"http://127.0.0.1/secret","openGraph":true}`); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"previews":[]`) {
		t.Fatalf("unsafe fallback = %d %s", rr.Code, rr.Body.String())
	}
	if rr := b.do(t, s, http.MethodGet, "/api/previews/image?url=http://127.0.0.1/secret", ""); rr.Code != http.StatusBadGateway {
		t.Fatalf("unsafe image = %d", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/previews/image?url=https://example.com/image.png", nil)
	req.RemoteAddr, req.Host = "127.0.0.1:1", "localhost:8228"
	req.Header.Set("X-Forwarded-For", "192.0.2.4")
	rr := httptest.NewRecorder()
	s.mux().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("untrusted image = %d", rr.Code)
	}
}

func TestOpenGraphPrefersRichProviderCard(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"title":"Existing PR card","head":{"sha":"abc123"}}`)
	}))
	defer api.Close()
	s := previewServer(t, newMockOAuth(t), "").withResolvers(linkpreview.Forge{ID: "github", Host: "github.com", APIBase: api.URL})
	s.Client = api.Client()
	rr := newBrowser().do(t, s, http.MethodPost, "/api/previews/resolve", `{"text":"https://github.com/o/r/pull/1","openGraph":true}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Existing PR card") || strings.Contains(rr.Body.String(), "opengraph") {
		t.Fatalf("provider precedence = %d %s", rr.Code, rr.Body.String())
	}
}
