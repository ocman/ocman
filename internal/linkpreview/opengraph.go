package linkpreview

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ResolveOpenGraph is an explicit, single-link fallback. Transcript discovery
// never fetches arbitrary pages. It shares the provider cache and fetch budget.
func (s *Service) ResolveOpenGraph(ctx context.Context, viewerID, ownerID, raw string) Preview {
	r := openGraph{client: s.PublicClient}
	u, err := url.Parse(raw)
	if err != nil {
		return Preview{State: StateError}
	}
	ref, ok := r.ParseURL(u)
	if !ok {
		return Preview{State: StateError}
	}
	return s.cached(ctx, r, "", true, false, viewerID, ownerID, grantKey(viewerID, ownerID, r.Provider(), ""), ref)
}

type openGraph struct{ client *http.Client }

func (openGraph) Provider() string                   { return "opengraph" }
func (openGraph) APIHosts() []string                 { return nil }
func (openGraph) ParseIdentifier(string) (Ref, bool) { return Ref{}, false }
func (openGraph) ParseURL(u *url.URL) (Ref, bool) {
	if !publicPageURL(u) {
		return Ref{}, false
	}
	copyURL := *u
	u = &copyURL
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	return Ref{Provider: "opengraph", Kind: "page", ID: u.Hostname(), URL: u.String()}, true
}

func publicPageURL(u *url.URL) bool {
	if u == nil || len(u.String()) > 8192 || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" && p != "80" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return publicPageAddr(ip)
	}
	return true
}

func publicPageAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || !gitlabAddrOK(ip, false) {
		return false
	}
	for _, prefix := range nonPublicPagePrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicPagePrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("2002::/16"),
}

func publicPageClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy, t.DialContext, t.DialTLSContext = nil, gitlabDial(publicPageAddr), nil
	t.DisableKeepAlives = true
	return &http.Client{Transport: t, Timeout: fetchTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !publicPageURL(req.URL) {
			return ErrUnsafeRequest
		}
		return nil
	}}
}

func fetchPublicPage(ctx context.Context, client *http.Client, raw, accept string, limit int64) ([]byte, *url.URL, string, error) {
	u, err := url.Parse(raw)
	if err != nil || !publicPageURL(u) {
		return nil, nil, "", ErrUnsafeRequest
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, "", err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "ocman-link-preview")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, "", &HTTPError{Status: resp.StatusCode, RetryAfter: publicPageRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, nil, "", errors.New("preview response exceeds limit or could not be read")
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	return body, resp.Request.URL, contentType, nil
}

func publicPageRetryAfter(value string, now time.Time) time.Duration {
	if secs, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Duration(min(max(secs, 0), int64(maxBackoff/time.Second))) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return min(max(deadline.Sub(now), 0), maxBackoff)
	}
	return 0
}

func (g openGraph) Fetch(ctx context.Context, _ *API, ref Ref) (Preview, error) {
	return fetchOpenGraph(ctx, g.client, ref)
}

func fetchOpenGraph(ctx context.Context, client *http.Client, ref Ref) (Preview, error) {
	body, base, contentType, err := fetchPublicPage(ctx, client, ref.URL, "text/html,application/xhtml+xml", maxResponse)
	if err != nil {
		return Preview{}, err
	}
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		return Preview{}, errors.New("page is not HTML")
	}
	return parseOpenGraph(strings.NewReader(string(body)), base)
}

func parseOpenGraph(reader io.Reader, base *url.URL) (Preview, error) {
	values := map[string]string{}
	z := html.NewTokenizer(reader)
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if !errors.Is(z.Err(), io.EOF) {
				return Preview{}, z.Err()
			}
			break
		}
		t := z.Token()
		if (kind == html.EndTagToken && t.Data == "head") || (kind == html.StartTagToken && t.Data == "body") {
			break
		}
		if (kind != html.StartTagToken && kind != html.SelfClosingTagToken) || t.Data != "meta" {
			continue
		}
		var key, content string
		for _, a := range t.Attr {
			switch a.Key {
			case "property", "name":
				key = strings.ToLower(a.Val)
			case "content":
				content = a.Val
			}
		}
		if strings.HasPrefix(key, "og:") && values[key] == "" {
			values[key] = content
		}
	}
	if strings.TrimSpace(values["og:title"]) == "" {
		return Preview{}, errors.New("page has no Open Graph title")
	}
	p := Preview{Title: values["og:title"], Description: values["og:description"], ImageAlt: values["og:image:alt"]}
	// Keep navigation on the requested page; og:url must not replace the link.
	p.Meta = []string{values["og:site_name"], base.Hostname()}
	image := values["og:image:secure_url"]
	if image == "" {
		image = values["og:image"]
	}
	if image == "" {
		image = values["og:image:url"]
	}
	if image != "" {
		if u, err := base.Parse(image); err == nil && publicPageURL(u) {
			p.Image = u.String()
		}
	}
	return p, nil
}

// OpenGraphImage proxies bounded raster bytes without forwarding credentials.
func (s *Service) OpenGraphImage(ctx context.Context, raw string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	return fetchOpenGraphImage(ctx, s.PublicClient, raw)
}

func fetchOpenGraphImage(ctx context.Context, client *http.Client, raw string) ([]byte, string, error) {
	body, _, _, err := fetchPublicPage(ctx, client, raw, "image/*", 2<<20)
	if err != nil {
		return nil, "", err
	}
	contentType := http.DetectContentType(body)
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return body, contentType, nil
	default:
		return nil, "", errors.New("unsupported preview image")
	}
}
