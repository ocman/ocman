package github

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetPathsAreEscaped pins that owner/repo/sha are escaped into the
// API path. They arrive from a regex capture over a user-supplied URL,
// and Go's transport does not normalise dot segments, so an unescaped
// `..%2Fuser%23` produced the request URI /repos/../user — which GitHub
// resolves to /user, returning the token owner's private profile.
func TestGetPathsAreEscaped(t *testing.T) {
	tests := []struct {
		name string
		call func(c *Client) (map[string]interface{}, error)
		want string
	}{
		{
			"pr traversal",
			func(c *Client) (map[string]interface{}, error) { return c.GetPR("..%2Fuser#", "x", 1) },
			"/repos/..%252Fuser%23/x/pulls/1",
		},
		{
			"issue traversal",
			func(c *Client) (map[string]interface{}, error) { return c.GetIssue("a", "../../user", 2) },
			"/repos/a/..%2F..%2Fuser/issues/2",
		},
		{
			"commit sha traversal",
			func(c *Client) (map[string]interface{}, error) { return c.GetCommit("a", "b", "../../../user") },
			"/repos/a/b/commits/..%2F..%2F..%2Fuser",
		},
		{
			"ordinary names are unchanged",
			func(c *Client) (map[string]interface{}, error) { return c.GetPR("NoUseFreak", "ocman", 7) },
			"/repos/NoUseFreak/ocman/pulls/7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.URL.EscapedPath()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			c := &Client{apiBase: srv.URL, http: srv.Client()}
			if _, err := tt.call(c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if got != tt.want {
				t.Errorf("request path = %q, want %q", got, tt.want)
			}
		})
	}
}
