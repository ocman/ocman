package opencode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func TestFirstPromptOutlivesReadTimeout(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "named"}[named], func(t *testing.T) {
			shortenReadTimeout(t)
			var prompts atomic.Int32
			f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodPost || r.URL.Path != "/api/session/cold/prompt" {
					return false
				}
				prompts.Add(1)
				select {
				case <-time.After(600 * time.Millisecond):
					writeJSONBody(w, `{"data":{"id":"msg_first"}}`)
				case <-r.Context().Done():
				}
				return true
			})
			h := legacyHTTP()
			if named {
				h = newHTTPState(ocapi.Auth{})
				h.api.Timeout = openCodeClient.Timeout
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err := h.sendMessageOnPort(ctx, f.Port(), platforms.SendMessageRequest{SessionID: "cold", Message: "first", FirstPrompt: true}); err != nil {
				t.Fatalf("cold first prompt: %v", err)
			}
			if prompts.Load() != 1 {
				t.Fatalf("prompt submitted %d times", prompts.Load())
			}
			if err := h.sendMessageOnPort(ctx, f.Port(), platforms.SendMessageRequest{SessionID: "cold", Message: "follow-up"}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("follow-up lost ordinary timeout: %v", err)
			}
		})
	}
}

type promptDeadlineTransport func(*http.Request) (*http.Response, error)

func (f promptDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFirstPromptDeadlineBudget(t *testing.T) {
	for _, callerBudget := range []time.Duration{0, time.Second, 2 * time.Minute} {
		t.Run(callerBudget.String(), func(t *testing.T) {
			ctx := t.Context()
			want := 60 * time.Second
			if callerBudget != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, callerBudget)
				defer cancel()
				if callerBudget < want {
					want = callerBudget
				}
			}
			h := &httpState{llm: &http.Client{Transport: promptDeadlineTransport(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || remaining > want || remaining < want-time.Second/2 {
					t.Errorf("first prompt remaining budget = %v, want %v", remaining, want)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}}
			if err := h.sendMessageOnPort(ctx, "1", platforms.SendMessageRequest{SessionID: "cold", Message: "first", FirstPrompt: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFirstPromptHonorsCallerCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			started := make(chan struct{})
			a, _ := newOpsFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/prompt_async") {
					close(started)
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte(`{}`))
			})
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 300*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				result <- a.SendMessage(ctx, platforms.SendMessageRequest{SessionID: opsSID, Message: "first", FirstPrompt: true})
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("first prompt never reached upstream")
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("first prompt returned %v, want %v", err, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("first prompt ignored caller cancellation")
			}
		})
	}
}

func TestPromptTimeoutDoesNotRediscoverOrRetry(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			a, _ := newOpsFixture(t, nil, nil)
			var requests atomic.Int32
			client := &http.Client{Transport: promptDeadlineTransport(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return nil, failure
			})}
			a.httpState = &httpState{api: client, llm: client}
			a.rt.httpState = a.httpState
			a.rt.rememberSessionPort(opsSID, "1")
			if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: opsSID, Message: "first", FirstPrompt: true}); !errors.Is(err, failure) {
				t.Fatalf("send error = %v, want %v", err, failure)
			}
			if requests.Load() != 1 || a.rt.preferredSessionPort(opsSID) != "1" {
				t.Fatalf("uncertain delivery triggered rediscovery: requests=%d, port=%q", requests.Load(), a.rt.preferredSessionPort(opsSID))
			}
		})
	}
}
