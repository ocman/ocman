package linkpreview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestChecksCredentialErrors(t *testing.T) {
	for _, gitea := range []bool{false, true} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(fmt.Sprintf("gitea=%v/status=%d", gitea, status), func(t *testing.T) {
				api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
				defer api.Close()
				base := api.URL
				if gitea {
					base += "/api/v1"
				}
				tokens := &fakeTokens{grants: map[string]string{"alice/github.com": "saved"}}
				svc := New(tokens, api.Client(), Forge{ID: "github", Host: "github.com", APIBase: base, Gitea: gitea})
				svc.cache[grantKey("alice", "local", "github", "github.com")+"\x00old"] = entry{p: Preview{State: StateOK}}
				p := svc.resolve(context.Background(), "alice", "local", Ref{Provider: "github", Kind: "checks", ID: "o/r@abc123"})
				if status == http.StatusUnauthorized {
					if p.State != StateConnect || len(tokens.revoked) != 1 || len(svc.cache) != 0 {
						t.Fatalf("401 must revoke and purge: %+v revoked=%v cache=%v", p, tokens.revoked, svc.cache)
					}
				} else if p.State != StateDenied || len(tokens.revoked) != 0 {
					t.Fatalf("403 must be denied without revoking: %+v", p)
				}
			})
		}
	}
}

func TestChecksProviderBackoff(t *testing.T) {
	for _, gitea := range []bool{false, true} {
		t.Run(fmt.Sprint(gitea), func(t *testing.T) {
			calls := 0
			api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer api.Close()
			base := api.URL
			if gitea {
				base += "/api/v1"
			}
			svc := New(nil, api.Client(), Forge{ID: "github", Host: "github.com", APIBase: base, Gitea: gitea})
			now := time.Now()
			svc.now = func() time.Time { return now }
			ref := Ref{Provider: "github", Kind: "checks", ID: "o/r@abc123"}
			ctx := WithOwnerAccess(context.Background())
			svc.resolve(ctx, "", "local", ref)
			count := calls
			now = now.Add(90 * time.Second)
			svc.resolve(WithChecksRefresh(ctx), "", "local", ref)
			if calls != count {
				t.Fatal("provider backoff was shortened to one minute")
			}
			now = now.Add(40 * time.Second)
			svc.resolve(ctx, "", "local", ref)
			if calls == count {
				t.Fatal("provider backoff never expired")
			}
		})
	}
}

func TestChecksDefaultTransportAndConnectionError(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total_count":0,"statuses":[],"check_runs":[]}`)
	}))
	defer api.Close()
	f := Forge{APIBase: api.URL}
	client := &API{client: &http.Client{Timeout: time.Second}}
	p, err := f.fetchChecks(context.Background(), client, "o/r", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if p.Checks.Checks == nil {
		t.Fatal("empty checks must encode as an array, not null")
	}
	api.Close()
	if _, err := f.fetchChecks(context.Background(), client, "o/r", "abc123"); err == nil {
		t.Fatal("connection failure was ignored")
	}
}

func TestChecksForbiddenRateLimits(t *testing.T) {
	for _, gitea := range []bool{false, true} {
		for _, secondary := range []bool{false, true} {
			t.Run(fmt.Sprintf("gitea=%v/secondary=%v", gitea, secondary), func(t *testing.T) {
				calls := 0
				now := time.Now()
				reset := now.Add(120 * time.Second).Unix()
				api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					if secondary {
						w.Header().Set("Retry-After", "120")
					} else {
						w.Header().Set("X-RateLimit-Remaining", "0")
						w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
					}
					w.WriteHeader(http.StatusForbidden)
				}))
				defer api.Close()
				base := api.URL
				if gitea {
					base += "/api/v1"
				}
				svc := New(nil, api.Client(), Forge{ID: "github", Host: "github.com", APIBase: base, Gitea: gitea})
				svc.now = func() time.Time { return now }
				ctx := WithOwnerAccess(context.Background())
				ref := Ref{Provider: "github", Kind: "checks", ID: "o/r@abc123"}
				if p := svc.resolve(ctx, "", "local", ref); p.State != StateRateLimited {
					t.Fatalf("quota denial should back off: %+v", p)
				}
				count := calls
				now = now.Add(90 * time.Second)
				svc.resolve(ctx, "", "local", ref)
				if calls != count {
					t.Fatal("403 retry deadline was lost")
				}
			})
		}
	}
}
