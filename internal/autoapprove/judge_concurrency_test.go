package autoapprove

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Each OpenCode judgment creates a session and runs an LLM turn, so a burst
// of prompts must not start them all at once: at most two run, the rest wait
// for a slot, and a waiter whose context ends gives up without a session.
func TestOpenCodeJudgeConcurrencyIsBounded(t *testing.T) {
	release := make(chan struct{})
	var active, peak, created atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/info":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			n := created.Add(1)
			now := active.Add(1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			_, _ = fmt.Fprintf(w, `{"id":"judge-%d"}`, n)
		case r.Method == http.MethodDelete:
			active.Add(-1)
			_, _ = w.Write([]byte(`true`))
		case strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
		default:
			<-release
			_, _ = w.Write([]byte(`[{"info":{"role":"assistant","finish":"stop"},"parts":[{"type":"text","text":"{\"verdict\":\"safe\",\"reasoning\":\"ok\"}"}]}]`))
		}
	}))
	defer server.Close()
	svc := NewService(Deps{})
	svc.judge.openCodePort = func(string) string { return strings.TrimPrefix(server.URL, "http://127.0.0.1:") }

	const judges = 5
	var wg sync.WaitGroup
	for range judges {
		wg.Go(func() {
			svc.judge.JudgeWithCallback(t.Context(), "/repo", "read", nil, nil, nil, nil)
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for created.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // give unbounded judges time to show up
	if got := created.Load(); got != 2 {
		close(release)
		wg.Wait()
		t.Fatalf("%d judge sessions running at once, want 2", got)
	}

	// A waiter whose context ends fails closed without creating a session.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if r := svc.judge.JudgeWithCallback(ctx, "/repo", "read", nil, nil, nil, nil); r.Verdict == verdictSafe || !r.EvaluationFailed {
		t.Errorf("cancelled waiter = %+v, want a failed evaluation", r)
	}
	if got := created.Load(); got != 2 {
		t.Errorf("cancelled waiter created a session: %d created", got)
	}

	close(release)
	wg.Wait()
	if got := created.Load(); got != judges {
		t.Fatalf("%d judge sessions created, want %d", got, judges)
	}
	if got := peak.Load(); got > 2 {
		t.Fatalf("peak concurrent judge sessions = %d, want <= 2", got)
	}
}
