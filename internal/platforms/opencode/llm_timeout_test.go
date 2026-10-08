package opencode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func shortenReadTimeout(t *testing.T) {
	t.Helper()
	previous := openCodeClient.Timeout
	openCodeClient.Timeout = 20 * time.Millisecond
	t.Cleanup(func() { openCodeClient.Timeout = previous })
}

func TestWorktreeNameOutlivesReadTimeout(t *testing.T) {
	shortenReadTimeout(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /config":
			_, _ = w.Write([]byte(`{"small_model":"test/fast"}`))
		case "POST /session":
			_, _ = w.Write([]byte(`{"id":"naming"}`))
		case "POST /session/naming/message":
			time.Sleep(80 * time.Millisecond)
			_, _ = w.Write([]byte(`{"parts":[{"type":"text","text":"Fix Login Flow"}]}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	name, err := WorktreeName(ctx, u.Port(), "/repo", "fix login")
	if err != nil || name != "fix-login-flow" {
		t.Fatalf("WorktreeName = %q, %v", name, err)
	}
}

func TestModelOperationsOutliveReadTimeout(t *testing.T) {
	shortenReadTimeout(t)
	for _, operation := range []opsInvocation{
		{"command", func(ctx context.Context, a *Adapter) error {
			return a.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{SessionID: opsSID, Command: "init"})
		}},
		{"compact", func(ctx context.Context, a *Adapter) error {
			return a.Compact(ctx, platforms.CompactRequest{SessionID: opsSID, ProviderID: "test", ModelID: "fast"})
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			started := make(chan struct{}, 3)
			a, _ := newOpsFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/command") || strings.HasSuffix(r.URL.Path, "/summarize") {
					started <- struct{}{}
					select {
					case <-time.After(80 * time.Millisecond):
					case <-r.Context().Done():
						return
					}
				}
				_, _ = w.Write([]byte(`{}`))
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := operation.call(ctx, a); err != nil {
				t.Fatalf("slow operation: %v", err)
			}
			<-started
			ctx, cancelRequest := context.WithCancel(ctx)
			result := make(chan error, 1)
			go func() { result <- operation.call(ctx, a) }()
			<-started
			cancelRequest()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not abort request")
			}
			deadline, cancelDeadline := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancelDeadline()
			if err := operation.call(deadline, a); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline: %v", err)
			}
		})
	}
}

func TestOrdinaryReadStillTimesOut(t *testing.T) {
	shortenReadTimeout(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := getJSON(ctx, u.Port(), "/config"); !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("expected client timeout before caller deadline: %v (ctx: %v)", err, ctx.Err())
	}
}

func TestModelClientSharesConfiguredTransport(t *testing.T) {
	previous := openCodeClient.Transport
	t.Cleanup(func() {
		openCodeClient.Transport = previous
		openCodeLLMClient.Transport = previous
	})
	configureHTTPAuth(ocapi.New("test-password"))
	if openCodeLLMClient.Transport != openCodeClient.Transport || openCodeLLMClient.Timeout != 0 {
		t.Fatal("model client must share the authenticated/instrumented compatibility transport without a timeout")
	}
}
