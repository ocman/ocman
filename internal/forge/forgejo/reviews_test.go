package forgejo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPRApproval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/alice/repo/pulls/42/reviews" || r.URL.RawQuery != "limit=30&page=1" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "token test" {
			t.Errorf("missing owner credential")
		}
		_, _ = w.Write([]byte(`[{"id":1,"state":"APPROVED","user":{"login":"bob"}}]`))
	}))
	defer srv.Close()
	c := NewForTest("example.com", srv.URL, "test", srv.Client())
	approved, err := c.PRApproval(context.Background(), "alice/repo", 42)
	if err != nil || !approved {
		t.Fatalf("approved=%v err=%v", approved, err)
	}
}
