package ocv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Wrap returns a RoundTripper that serves v1 OpenCode routes against a
// v2 server and passes everything else to inner unchanged. inner must
// already authenticate (it is also used for the v2 calls).
func Wrap(inner http.RoundTripper) http.RoundTripper {
	if inner == nil {
		inner = http.DefaultTransport
	}
	probeRT.Store(&inner)
	return &transport{inner: inner}
}

type transport struct{ inner http.RoundTripper }

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !InstalledV2() || strings.HasPrefix(req.URL.Path, "/api/") || req.URL.Path == "/openapi.json" {
		return t.inner.RoundTrip(req)
	}
	v2, err := detect(req.Context(), t.inner, req.URL.Scheme, req.URL.Host)
	if err != nil {
		return nil, err
	}
	if !v2 {
		return t.inner.RoundTrip(req)
	}
	// A RoundTripper must close the request body; most routes never read it.
	if req.Body != nil {
		defer req.Body.Close()
	}
	c := &compat{rt: t.inner, base: req.URL.Scheme + "://" + req.URL.Host}
	return c.serve(req)
}

// compat answers one v1 request with v2 calls.
type compat struct {
	rt   http.RoundTripper
	base string
}

type routeFn func(c *compat, r *http.Request, m []string) (*http.Response, error)

type route struct {
	method string
	re     *regexp.Regexp
	fn     routeFn
}

var routes []route

func handle(method, pattern string, fn routeFn) {
	routes = append(routes, route{method, regexp.MustCompile("^" + pattern + "$"), fn})
}

func (c *compat) serve(r *http.Request) (*http.Response, error) {
	for _, rt := range routes {
		if rt.method != r.Method {
			continue
		}
		if m := rt.re.FindStringSubmatch(r.URL.Path); m != nil {
			return rt.fn(c, r, m)
		}
	}
	return reply(r, http.StatusNotFound, map[string]any{
		"name": "NotFound", "data": map[string]any{"message": "no OpenCode v2 equivalent for " + r.Method + " " + r.URL.Path},
	}), nil
}

// upstreamError is a non-2xx v2 answer, relayed to the v1 caller as-is
// so status codes (404 tolerance, auth) and error bodies keep working.
type upstreamError struct{ resp *http.Response }

func (e *upstreamError) Error() string { return fmt.Sprintf("opencode v2: HTTP %d", e.resp.StatusCode) }

// call performs one v2 request. 2xx bodies decode into out (when
// non-nil); other statuses come back as *upstreamError.
func (c *compat) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		return &upstreamError{resp: resp}
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// data unwraps the v2 `{data: ...}` envelope.
type data[T any] struct {
	Data T `json:"data"`
}

// finish turns a handler's (value, error) into the v1 response.
func finish(r *http.Request, status int, v any, err error) (*http.Response, error) {
	if err != nil {
		var ue *upstreamError
		if errors.As(err, &ue) {
			ue.resp.Request = r
			return ue.resp, nil
		}
		return nil, err
	}
	return reply(r, status, v), nil
}

func reply(r *http.Request, status int, v any) *http.Response {
	var body []byte
	h := http.Header{}
	if status != http.StatusNoContent {
		body, _ = json.Marshal(v)
		h.Set("Content-Type", "application/json")
	}
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: h, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r,
	}
}

// readBody decodes a v1 JSON request body (empty bodies are fine).
func readBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return err
	}
	return json.Unmarshal(b, v)
}

// location builds the v2 location query for a v1 `?directory=` value.
func location(dir string) url.Values {
	q := url.Values{}
	if dir != "" {
		q.Set("location[directory]", dir)
	}
	return q
}

// requestDirectory returns the v1 directory scope of a request: the
// `directory` query, else the x-opencode-directory header.
func requestDirectory(r *http.Request) string {
	if d := r.URL.Query().Get("directory"); d != "" {
		return d
	}
	if h := r.Header.Get("x-opencode-directory"); h != "" {
		if d, err := url.PathUnescape(h); err == nil {
			return d
		}
		return h
	}
	return ""
}
