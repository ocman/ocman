package ocv2

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsolatedTransportsNeverSharePromptSessionBindings(t *testing.T) {
	newAccount := func(session string) (*http.Client, string) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/info":
				_, _ = w.Write([]byte(`{"version":"2.1.2"}`))
			case "/api/permission/request":
				fmt.Fprintf(w, `{"data":[{"id":"same-request-id","sessionID":%q}]}`, session)
			case "/api/session/" + session + "/permission/same-request-id/reply":
				_, _ = w.Write([]byte(`{"data":true}`))
			default:
				t.Errorf("%s account addressed sibling request path %s", session, r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		return &http.Client{Transport: WrapIsolated(http.DefaultTransport)}, server.URL
	}
	a, aURL := newAccount("session-alpha")
	b, bURL := newAccount("session-beta")
	request := func(client *http.Client, method, url, body string) {
		req, err := http.NewRequestWithContext(t.Context(), method, url, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("response status %d", resp.StatusCode)
		}
	}
	request(a, http.MethodGet, aURL+"/permission", "")
	request(b, http.MethodGet, bURL+"/permission", "")
	request(a, http.MethodPost, aURL+"/permission/same-request-id/reply", `{"reply":"once"}`)
	request(b, http.MethodPost, bURL+"/permission/same-request-id/reply", `{"reply":"once"}`)
}
