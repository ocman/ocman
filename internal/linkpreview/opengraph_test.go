package linkpreview

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestParseOpenGraph(t *testing.T) {
	base, _ := url.Parse("https://forgejo.example/dries/ocman/pulls/1026")
	p, err := parseOpenGraph(strings.NewReader(`<head>
<meta content="Fix &amp; test" property="og:title">
<meta property="og:title" content="second title">
<meta name="og:description" content="A useful description">
<meta property="og:site_name" content="Forgejo">
<meta property="og:url" content="https://evil.example/redirect">
<meta property="og:image" content="/assets/card.png">
<meta property="og:image:alt" content="PR card">
</head><body><meta property="og:title" content="ignore body">`), base)
	if err != nil || p.Title != "Fix & test" || p.Description != "A useful description" || p.Image != "https://forgejo.example/assets/card.png" || p.ImageAlt != "PR card" || p.URL != "" {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	ref, _ := (openGraph{}).ParseURL(base)
	p.Description = strings.Repeat("x", 600)
	p = sanitize(p, ref)
	if p.State != StateOK || len([]rune(p.Description)) != 500 || p.URL != base.String() || p.Meta[0] != "Forgejo" {
		t.Fatalf("sanitized = %+v", p)
	}
}

func TestOpenGraphMetadataFallbacks(t *testing.T) {
	base, _ := url.Parse("https://example.com/page")
	for _, tc := range []struct {
		name, tags, image string
		fails             bool
	}{
		{"missing", `<title>Plain title</title>`, "", true},
		{"empty", `<meta property="og:title" content=" ">`, "", true},
		{"secure image", `<meta property="og:image" content="/insecure"><meta property="og:image:secure_url" content="https://cdn.example/secure">`, "https://cdn.example/secure", false},
		{"image url", `<meta property="og:image:url" content="//cdn.example/card">`, "https://cdn.example/card", false},
		{"unsafe image", `<meta property="og:image" content="http://127.0.0.1/secret">`, "", false},
		{"script image", `<meta property="og:image" content="javascript:alert(1)">`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tags := tc.tags
			if !tc.fails {
				tags = `<meta property="og:title" content="Title">` + tags
			}
			p, err := parseOpenGraph(strings.NewReader(tags), base)
			if (err != nil) != tc.fails || p.Image != tc.image {
				t.Fatalf("%+v, %v", p, err)
			}
		})
	}
}

func TestOpenGraphPublicTargets(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "https://user:pass@example.com", "http://localhost", "http://x.local", "http://x.localhost", "http://127.0.0.1", "http://10.0.0.1", "http://169.254.169.254", "http://100.64.0.1", "http://[::1]", "https://example.com:8443", "http://0.0.0.0"} {
		u, _ := url.Parse(raw)
		if _, ok := (openGraph{}).ParseURL(u); ok {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://example.com/page?q=1#section", "http://example.com", "https://example.com:443"} {
		u, _ := url.Parse(raw)
		ref, ok := (openGraph{}).ParseURL(u)
		if !ok || strings.Contains(ref.URL, "#") {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "::ffff:127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "224.0.0.1", "::", "fe80::1", "fc00::1", "0.1.2.3", "192.0.2.1", "198.18.0.1", "240.0.0.1", "2001:db8::1", "64:ff9b::a00:1", "2002:7f00:1::"} {
		if publicPageAddr(netip.MustParseAddr(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	client := publicPageClient()
	for _, raw := range []string{"http://127.0.0.1", "file:///secret"} {
		u, _ := url.Parse(raw)
		if client.CheckRedirect(&http.Request{URL: u}, nil) == nil {
			t.Errorf("unsafe redirect %s", raw)
		}
	}
	u, _ := url.Parse("https://example.com")
	if client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 5)) == nil {
		t.Fatal("redirect limit missing")
	}
	resp, err := client.Get("http://127.0.0.1")
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("loopback dial accepted")
	}
}

type ogTransport func(*http.Request) (*http.Response, error)

func (f ogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchPublicPageBoundsAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		fails      bool
	}{
		{"ok", "<html>page</html>", 200, false},
		{"large", strings.Repeat("a", 33), 200, true},
		{"not found", "missing", 404, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: ogTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept") != "text/html" {
					t.Fatal("unsafe headers")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}, Request: r}, nil
			})}
			body, _, _, err := fetchPublicPage(context.Background(), client, "https://example.com/page", "text/html", 32)
			if (err != nil) != tc.fails || (!tc.fails && string(body) != tc.body) {
				t.Fatalf("%q, %v", body, err)
			}
		})
	}
}

func TestOpenGraphRejectsInvalidRequestsAndImages(t *testing.T) {
	s := New(nil, nil)
	for _, raw := range []string{"%bad", "http://localhost", "http://127.0.0.1"} {
		if p := s.ResolveOpenGraph(context.Background(), "viewer", "local", raw); p.State != StateError {
			t.Fatalf("%+v", p)
		}
		if _, _, err := s.OpenGraphImage(context.Background(), raw); err == nil {
			t.Fatalf("image accepted %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range maxInFlight {
		s.sem <- struct{}{}
	}
	if _, _, err := s.OpenGraphImage(ctx, "https://example.com"); err == nil {
		t.Fatal("cancel ignored")
	}
}

func TestOpenGraphFetchAndImageContent(t *testing.T) {
	client := func(body, contentType string, status int) *http.Client {
		return &http.Client{Transport: ogTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {contentType}}, Request: r}, nil
		})}
	}
	ref := Ref{URL: "https://example.com/page"}
	p, err := fetchOpenGraph(context.Background(), client(`<meta property="og:title" content="Article">`, "text/html; charset=utf-8", 200), ref)
	if err != nil || p.Title != "Article" {
		t.Fatalf("%+v, %v", p, err)
	}
	for _, c := range []*http.Client{client("{}", "application/json", 200), client("", "text/html", 404)} {
		if _, err := fetchOpenGraph(context.Background(), c, ref); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	for _, tc := range []struct{ body, want string }{
		{"\x89PNG\r\n\x1a\n", "image/png"},
		{"\xff\xd8\xff", "image/jpeg"},
		{"GIF89a", "image/gif"},
		{"RIFF1234WEBPVP", "image/webp"},
		{"<svg></svg>", ""},
		{"<html>fake image</html>", ""},
	} {
		body, kind, err := fetchOpenGraphImage(context.Background(), client(tc.body, "image/png", 200), ref.URL)
		if (err == nil) != (tc.want != "") || kind != tc.want || (err == nil && string(body) != tc.body) {
			t.Fatalf("%q: %q, %v", tc.body, kind, err)
		}
	}
	if _, _, err := fetchOpenGraphImage(context.Background(), client("", "image/png", 404), ref.URL); err == nil {
		t.Fatal("missing image accepted")
	}
}
