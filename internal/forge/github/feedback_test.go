package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedback(t *testing.T) {
	for _, mode := range []string{"ok", "invalid", "denied", "overflow", "pagination"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing credentials")
				}
				switch mode {
				case "invalid":
					fmt.Fprint(w, `not-json`)
					return
				case "denied":
					w.WriteHeader(http.StatusForbidden)
					return
				case "overflow", "pagination":
					if mode == "overflow" || r.URL.Query().Get("page") == "1" {
						fmt.Fprint(w, "["+strings.TrimSuffix(strings.Repeat(`{"id":1,"body":"comment","user":{"login":"reviewer"}},`, 100), ",")+"]")
						return
					}
				}
				fmt.Fprint(w, `[{"id":2,"body":"Fix this","path":"main.go","html_url":"https://github.com/team/repo/pull/42#r2","submitted_at":"2026-10-29T12:00:00Z","user":{"login":"reviewer"}},{"id":3,"body":"","user":{"login":"reviewer"}}]`)
			}))
			defer srv.Close()
			c := NewWithCredentials(srv.URL, "secret", srv.Client())
			remarks, err := c.Feedback(t.Context(), "team/repo", 42)
			if mode == "ok" {
				if err != nil || len(remarks) != 3 || remarks[2].ID != "review:2" || remarks[0].UpdatedAt == "" {
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
