package opencode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGlobalEventsTransport(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/global/event" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "data: {}\n\n")
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			body, err := OpenGlobalEvents(t.Context(), server.Client(), u.Port())
			if status >= 400 {
				if err == nil || body != nil {
					t.Fatal("HTTP failure accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			bytes, err := io.ReadAll(body)
			if err != nil || string(bytes) != "data: {}\n\n" {
				t.Fatalf("%q %v", bytes, err)
			}
		})
	}
	if _, err := OpenGlobalEvents(t.Context(), http.DefaultClient, "bad\nport"); err == nil {
		t.Fatal("malformed target accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenGlobalEvents(ctx, http.DefaultClient, "1"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
