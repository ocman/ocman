package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/forge"
)

func TestChecks_Pagination(t *testing.T) {
	for _, endpoint := range []string{"status", "check-runs"} {
		for _, state := range []string{"pending", "failure"} {
			t.Run(endpoint+"/"+state, func(t *testing.T) {
				var pages []string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path != "/repos/o/r/commits/abc123/"+endpoint {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					pages = append(pages, r.URL.Query().Get("page"))
					if r.URL.Query().Get("page") == "2" {
						if endpoint == "status" {
							_, _ = fmt.Fprintf(w, `{"total_count":101,"statuses":[{"context":"later","state":%q}]}`, state)
						} else {
							status := "completed"
							if state == "pending" {
								status = "in_progress"
							}
							_, _ = fmt.Fprintf(w, `{"total_count":101,"check_runs":[{"name":"later","status":%q,"conclusion":%q}]}`, status, state)
						}
						return
					}
					items := make([]string, 100)
					for i := range items {
						if endpoint == "status" {
							items[i] = fmt.Sprintf(`{"context":"first-%d","state":"success"}`, i)
						} else {
							items[i] = fmt.Sprintf(`{"name":"first-%d","status":"completed","conclusion":"success"}`, i)
						}
					}
					key := "statuses"
					if endpoint == "check-runs" {
						key = "check_runs"
					}
					_, _ = fmt.Fprintf(w, `{"total_count":101,%q:[%s]}`, key, strings.Join(items, ","))
				}))
				defer srv.Close()
				ci, _, err := newTestClient(t, srv, "tok").Checks(context.Background(), "o/r", "abc123")
				if err != nil {
					t.Fatal(err)
				}
				if len(ci.Checks) != 101 || ci.State != forge.CIState(state) {
					t.Fatalf("got %d checks, state %s; want 101, %s", len(ci.Checks), ci.State, state)
				}
				if fmt.Sprint(pages) != "[1 2]" {
					t.Fatalf("requested pages %v", pages)
				}
			})
		}
	}
}

func TestChecks_LaterPageUnavailable(t *testing.T) {
	for _, endpoint := range []string{"status", "check-runs"} {
		for _, response := range []string{"rate-limit", "error", "not-found", "malformed", "empty"} {
			t.Run(endpoint+"/"+response, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path != "/repos/o/r/commits/abc123/"+endpoint {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if r.URL.Query().Get("page") == "1" {
						if endpoint == "status" {
							_, _ = w.Write([]byte(`{"total_count":2,"statuses":[{"context":"first","state":"success"}]}`))
						} else {
							_, _ = w.Write([]byte(`{"total_count":2,"check_runs":[{"name":"first","status":"completed","conclusion":"success"}]}`))
						}
						return
					}
					switch response {
					case "rate-limit":
						w.WriteHeader(http.StatusTooManyRequests)
					case "error":
						w.WriteHeader(http.StatusInternalServerError)
					case "not-found":
						w.WriteHeader(http.StatusNotFound)
					case "malformed":
						_, _ = w.Write([]byte(`{broken`))
					case "empty":
						_, _ = w.Write([]byte(`{"total_count":2,"statuses":[],"check_runs":[]}`))
					}
				}))
				defer srv.Close()
				ci, rl, err := newTestClient(t, srv, "tok").Checks(context.Background(), "o/r", "abc123")
				if response == "rate-limit" {
					if err != nil || !rl.Limited || len(ci.Checks) != 0 {
						t.Fatalf("expected rate-limited result without partial checks: %+v, %+v, %v", ci, rl, err)
					}
				} else if err == nil {
					t.Fatal("expected error, not a settled partial result")
				}
			})
		}
	}
}
