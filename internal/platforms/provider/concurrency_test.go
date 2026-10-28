package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestHeadlessPromptDelivery(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.frames(`{"type":"permission.asked","properties":{"sessionID":"s","id":"p","permission":"bash","patterns":["pwd"],"always":["pwd"]}}`)
	a := f.adapter(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	delivered := 0
	a.SetEventHandler(func(event providerprotocol.Event) {
		if event.Type != "permission.asked" {
			t.Error(event.Type)
		}
		pending, err := a.ListPermissions(ctx, "s")
		if err != nil || len(pending) != 1 {
			t.Error("prompt not cached before callback", pending, err)
		}
		if err := a.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p", Reply: "once"}); err != nil {
			t.Error(err)
		}
		delivered++
		cancel()
	})
	if err := a.WatchEvents(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if delivered != 1 {
		t.Fatal(delivered)
	}
	pending, _ := a.ListPermissions(t.Context(), "s")
	if len(pending) != 0 {
		t.Fatal("headless response did not resolve prompt")
	}
}

func TestReconciliationDoesNotResurrectConcurrentReplies(t *testing.T) {
	for _, kind := range []string{"permission", "question", "reject"} {
		t.Run(kind, func(t *testing.T) {
			f := newFake(t, allCapabilities())
			a := f.adapter(t)
			started, release := make(chan struct{}), make(chan struct{})
			path := "/permissions"
			if kind != "permission" {
				path = "/questions"
			}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != path || r.Method != http.MethodGet {
					return false
				}
				close(started)
				<-release
				_ = json.NewEncoder(w).Encode([]platforms.LivePrompt{{"id": "p", "sessionID": "s"}})
				return true
			}
			done := make(chan error, 1)
			go func() {
				var err error
				if kind == "permission" {
					_, err = a.RefreshPermissions(t.Context(), "s")
				} else {
					_, err = a.RefreshQuestions(t.Context(), "s")
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("no refresh")
			}
			var err error
			switch kind {
			case "permission":
				err = a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p"})
			case "question":
				err = a.RespondQuestion(t.Context(), platforms.RespondQuestionRequest{SessionID: "s", RequestID: "p"})
			case "reject":
				err = a.RejectQuestion(t.Context(), platforms.RejectQuestionRequest{SessionID: "s", RequestID: "p"})
			}
			close(release)
			requireNoError(t, err)
			if err := <-done; !errors.Is(err, ErrReconciliationConflict) {
				t.Fatal("conflicted refresh claimed authority", err)
			}
			var pending []platforms.LivePrompt
			if kind == "permission" {
				pending, _ = a.ListPermissions(t.Context(), "s")
			} else {
				pending, _ = a.ListQuestions(t.Context(), "s")
			}
			if len(pending) != 0 {
				t.Fatal("stale snapshot resurrected resolved prompt", pending)
			}
		})
	}
}

func TestTerminalStatusesAndBoundedFrames(t *testing.T) {
	for _, status := range []string{"waiting", "error", "interrupted", "idle"} {
		t.Run(status, func(t *testing.T) {
			f := newFake(t, allCapabilities())
			f.frames(fmt.Sprintf(`{"type":"session.status","properties":{"sessionID":"s","status":{"type":%q}}}`, status))
			a := f.adapter(t)
			requireNoError(t, a.ProxyEvents(t.Context(), "s", io.Discard, nil))
			expected := status
			if status == "idle" {
				expected = "done"
			}
			if a.LiveStatus("s").Status != expected {
				t.Fatal(a.LiveStatus("s"))
			}
		})
	}
	f := newFake(t, allCapabilities())
	f.events = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: 1\n")
		line := "data: " + strings.Repeat("x", 1<<20) + "\n"
		for i := 0; i < 9; i++ {
			if _, err := io.WriteString(w, line); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, "\n")
	}
	a := f.adapter(t)
	if err := a.ProxyEvents(t.Context(), "", io.Discard, nil); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatal(err)
	}
}

func TestSlowBrowserUsesItsOwnIdlePredecessor(t *testing.T) {
	for _, limitedFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(limitedFirst), func(t *testing.T) {
			f := newFake(t, allCapabilities())
			status := "limited"
			if !limitedFirst {
				status = "busy"
			}
			later := "busy"
			if !limitedFirst {
				later = "limited"
			}
			f.frames(
				fmt.Sprintf(`{"type":"session.status","properties":{"sessionID":"s","status":{"type":%q}}}`, status),
				`{"type":"session.idle","properties":{"sessionID":"s"}}`,
				fmt.Sprintf(`{"type":"session.status","properties":{"sessionID":"s","status":{"type":%q}}}`, later),
			)
			a := f.adapter(t)
			// One consumer advances shared live state before a slower browser replays.
			requireNoError(t, a.ProxyEvents(t.Context(), "s", io.Discard, nil))
			var browser bytes.Buffer
			requireNoError(t, a.ProxyEvents(t.Context(), "s", &browser, nil))
			hasIdle := strings.Contains(browser.String(), `"type":"session.idle"`)
			if hasIdle == limitedFirst {
				t.Fatal("idle validity depended on newer shared status", browser.String())
			}
		})
	}
}

func TestHandshakeRequiresExplicitVersionAndCLI(t *testing.T) {
	for _, body := range []string{`{"providerId":"fake","cliVersion":"test-only"}`, `{"protocolVersion":1,"providerId":"fake"}`} {
		f := newFake(t, allCapabilities())
		f.hook = func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/hello" {
				return false
			}
			_, _ = io.WriteString(w, body)
			return true
		}
		if _, err := New(t.Context(), f.socket); err == nil {
			t.Fatal("accepted incomplete handshake", body)
		}
	}
}

func TestPromptArrivingDuringStreamConnectIsForwarded(t *testing.T) {
	f := newFake(t, allCapabilities())
	a := f.adapter(t)
	frame := `{"type":"permission.asked","properties":{"id":"p","sessionID":"s","permission":"bash","patterns":["pwd"],"always":["pwd"]}}`
	f.events = func(w http.ResponseWriter, _ *http.Request) {
		var event providerprotocol.Event
		_ = json.Unmarshal([]byte(frame), &event)
		// The headless consumer receives this prompt while the browser is
		// connecting, after its initial pending-prompts read.
		_, err := a.observe(1, event)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "id: 1\ndata: %s\n\n", frame)
	}
	var browser bytes.Buffer
	requireNoError(t, a.ProxyEvents(t.Context(), "s", &browser, nil))
	if !strings.Contains(browser.String(), `"permission.asked"`) {
		t.Fatal("prompt arriving during connection was suppressed")
	}
}
