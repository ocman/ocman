package apiv1

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestHTTPResponses(t *testing.T) {
	for _, operation := range []string{"get", "post", "send"} {
		for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError} {
			for _, body := range []string{"", `{"name":"Rejected","data":{"message":"try again"}}`} {
				t.Run(operation+"/"+http.StatusText(status)+"/"+body, func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if operation != "get" {
							data, _ := io.ReadAll(r.Body)
							if r.Header.Get("Content-Type") != "application/json" || string(data) != `{}` {
								t.Errorf("request content type/body = %q/%q", r.Header.Get("Content-Type"), data)
							}
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, body)
					}))
					t.Cleanup(srv.Close)
					port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
					var got []byte
					var err error
					switch operation {
					case "get":
						got, err = GetJSON(t.Context(), srv.Client(), port, "/config")
					case "post":
						got, err = PostJSONReturning(t.Context(), srv.Client(), port, "/session", []byte(`{}`))
					case "send":
						err = SendJSON(t.Context(), srv.Client(), http.MethodPatch, port, "/session/x", []byte(`{}`))
					}
					if (err != nil) != (status >= 400) {
						t.Fatalf("response = %q, %v", got, err)
					}
					if status == http.StatusOK && operation != "send" && string(got) != body {
						t.Fatalf("response body = %q, want %q", got, body)
					}
					if operation != "get" && status == http.StatusBadRequest {
						var upstream *platforms.UpstreamError
						if !errors.As(err, &upstream) || upstream.Status != status {
							t.Fatalf("upstream rejection = %v", err)
						}
						if body != "" && upstream.Message != "try again" {
							t.Fatalf("rejection message = %q", upstream.Message)
						}
					}
				})
			}
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestHTTPFailures(t *testing.T) {
	failure := errors.New("upstream read failed")
	for _, operation := range []struct {
		name string
		call func(context.Context, *http.Client, string) error
	}{
		{"get", func(ctx context.Context, c *http.Client, port string) error {
			_, err := GetJSON(ctx, c, port, "/config")
			return err
		}},
		{"post", func(ctx context.Context, c *http.Client, port string) error {
			_, err := PostJSONReturning(ctx, c, port, "/session", nil)
			return err
		}},
		{"send", func(ctx context.Context, c *http.Client, port string) error {
			return SendJSON(ctx, c, http.MethodPost, port, "/session", nil)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(t.Context(), http.DefaultClient, "invalid\nport"); err == nil {
				t.Fatal("invalid URL accepted")
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, failure
			})}
			if err := operation.call(t.Context(), client, "1234"); !errors.Is(err, failure) {
				t.Fatalf("transport error = %v", err)
			}
			client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				status := http.StatusOK
				if operation.name == "send" {
					status = http.StatusBadRequest
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(failingReader{failure})}, nil
			})
			if err := operation.call(t.Context(), client, "1234"); !errors.Is(err, failure) {
				t.Fatalf("body error = %v", err)
			}
		})
	}
}

func TestGetJSONRejectsNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>not a catalog</html>")
	}))
	t.Cleanup(srv.Close)
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	if _, err := GetJSON(t.Context(), srv.Client(), port, "/config"); err == nil {
		t.Fatal("non-JSON response accepted")
	}
}

func TestExtractErrorMessage(t *testing.T) {
	for _, tt := range []struct{ body, want string }{
		{"", ""},
		{" \n", ""},
		{"boom", "boom"},
		{`{"foo":"bar"}`, `{"foo":"bar"}`},
		{`{"name":"Rejected","data":{"message":"try again"}}`, "try again"},
		{`{"name":"Rejected","data":{"z":2,"a":1}}`, "Rejected: a=1, z=2"},
		{`{"name":"Rejected","data":{}}`, "Rejected"},
		{`{"_tag":"SessionBusyError","message":"Session is busy"}`, "Session is busy"},
		{`{"_tag":"SessionBusyError"}`, "SessionBusyError"},
	} {
		t.Run(tt.body, func(t *testing.T) {
			if got := ExtractErrorMessage([]byte(tt.body)); got != tt.want {
				t.Fatalf("message = %q, want %q", got, tt.want)
			}
		})
	}
}
