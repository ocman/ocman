package linkpreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrUnsafeRequest is returned, without any network call, for a request to
// a host outside the resolver's APIHosts or with an invalid path segment.
var ErrUnsafeRequest = errors.New("unsafe preview request")

// maxResponse bounds a provider response body.
const maxResponse = 1 << 20

// HTTPError is a non-2xx provider response. Redirects are never followed,
// so a 3xx lands here too.
type HTTPError struct {
	Status     int
	RetryAfter time.Duration
	// Body is the bounded error response, for providers that report the
	// real cause inside it (Linear's GraphQL 400s). Never in Error().
	Body []byte
}

func (e *HTTPError) Error() string { return fmt.Sprintf("provider returned %d", e.Status) }

// API is the only way a Resolver reaches its provider: fixed hosts,
// escaped validated path segments, the viewer's bearer token, no redirects.
type API struct {
	client *http.Client
	token  string
	hosts  map[string]bool
	// publicOnly: token is not the viewer's own (see Fallback), so Fetch
	// must return only resources that are public.
	publicOnly bool
	header     http.Header
	// slashes: a segment may hold "/"-separated valid parts, sent as one
	// %2F-escaped segment (GitLab's URL-encoded project path).
	slashes bool
}

// WithEncodedSlashes returns a copy of a whose segments may contain "/",
// each part still validated, escaped as %2F within one segment.
func (a *API) WithEncodedSlashes() *API {
	c := *a
	c.slashes = true
	return &c
}

func (a *API) validSegment(s string) bool {
	if validSegment(s) {
		return true
	}
	if !a.slashes || !strings.Contains(s, "/") {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if !validSegment(p) {
			return false
		}
	}
	return true
}

// WithHeader returns a copy of a that also sends header k: v (e.g. an API
// version). Accept, Content-Type and Authorization cannot be overridden.
func (a *API) WithHeader(k, v string) *API {
	c := *a
	c.header = a.header.Clone()
	if c.header == nil {
		c.header = http.Header{}
	}
	c.header.Set(k, v)
	return &c
}

// PublicOnly reports that the resolver must not return private resources.
func (a *API) PublicOnly() bool { return a.publicOnly }

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= 256 && !strings.ContainsAny(s, "/\\?#%") &&
		strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

// JSON sends method to base + "/" + segments (each path-escaped) with an
// optional JSON body and decodes a JSON response into out.
func (a *API) JSON(ctx context.Context, method, base string, segments []string, query url.Values, body, out any) error {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !a.hosts[strings.ToLower(u.Host)] {
		return ErrUnsafeRequest
	}
	path, raw := strings.TrimRight(u.Path, "/"), strings.TrimRight(u.EscapedPath(), "/")
	for _, s := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if s == "." || s == ".." {
			return ErrUnsafeRequest
		}
	}
	for _, s := range segments {
		if !a.validSegment(s) {
			return ErrUnsafeRequest
		}
		path, raw = path+"/"+s, raw+"/"+url.PathEscape(s)
	}
	u.Path, u.RawPath, u.RawQuery = path, raw, query.Encode()

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return ErrUnsafeRequest
	}
	for k, v := range a.header {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch {
	case strings.HasPrefix(a.token, "lin_api_"):
		// Linear personal API keys go bare; OAuth tokens use Bearer.
		req.Header.Set("Authorization", a.token)
	case a.token != "":
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return &HTTPError{Status: resp.StatusCode, RetryAfter: time.Duration(secs) * time.Second, Body: b}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(out)
}
