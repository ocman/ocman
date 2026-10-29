package forgejo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedback(t *testing.T) {
	for _, mode := range []string{"ok", "invalid", "denied", "inline-invalid", "inline-denied", "overflow", "inline-overflow", "pagination"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "token secret" {
					t.Error("missing credentials")
				}
				inline := strings.Contains(r.URL.Path, "/reviews/1/comments")
				if mode == "denied" || (mode == "inline-denied" && inline) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if mode == "invalid" || (mode == "inline-invalid" && inline) {
					fmt.Fprint(w, `invalid`)
					return
				}
				if mode == "overflow" || (mode == "inline-overflow" && inline) || (mode == "pagination" && r.URL.Query().Get("page") == "1" && !inline) {
					fmt.Fprint(w, "["+strings.TrimSuffix(strings.Repeat(`{"id":1,"body":"comment","user":{"login":"reviewer"}},`, 100), ",")+"]")
					return
				}
				fmt.Fprint(w, `[{"id":1,"body":"Fix this","path":"main.go","submitted_at":"2026-10-29T12:00:00Z","user":{"login":"reviewer"}}]`)
			}))
			defer srv.Close()
			c := NewWithCredentials("code.example.com", srv.URL, "secret", srv.Client())
			remarks, err := c.Feedback(t.Context(), "team/repo", 42)
			if mode == "ok" {
				if err != nil || len(remarks) != 3 || remarks[2].Path != "main.go" {
					t.Fatalf("remarks=%v err=%v", remarks, err)
				}
			} else if mode == "pagination" {
				if err != nil || len(remarks) != 303 {
					t.Fatalf("len=%d err=%v", len(remarks), err)
				}
			} else if err == nil || remarks != nil {
				t.Fatal("incomplete feedback treated as complete")
			}
		})
	}
}
