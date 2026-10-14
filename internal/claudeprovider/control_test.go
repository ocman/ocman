package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type sdkWriter struct{ write func([]byte) (int, error) }

func (w sdkWriter) Write(b []byte) (int, error) { return w.write(b) }
func (sdkWriter) Close() error                  { return nil }

func TestSDKControlResponses(t *testing.T) {
	for _, tc := range []struct {
		name, subtype, message  string
		exit, noAck, writeError bool
	}{
		{name: "success", subtype: "success"},
		{name: "error", subtype: "error", message: "not allowed"},
		{name: "invalid response", subtype: "invalid", message: "invalid Claude control response"},
		{name: "ack and EOF", subtype: "success", exit: true},
		{name: "EOF without ack", exit: true, noAck: true, message: "exited awaiting"},
		{name: "deadline", noAck: true, message: "deadline exceeded"},
		{name: "write failure", writeError: true, message: "broken pipe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &live{}
			p := &proc{controls: map[string]chan controlResult{}, done: make(chan struct{}), stderr: &tail{}}
			p.stdin = sdkWriter{func(b []byte) (int, error) {
				if tc.writeError {
					return 0, errors.New("broken pipe")
				}
				var request struct {
					RequestID string `json:"request_id"`
				}
				if err := json.Unmarshal(b, &request); err != nil {
					t.Fatal(err)
				}
				// Unrelated and repeated acknowledgments must not unblock another request or block the reader.
				l.receiveControl(p, []byte(`{"response":{"request_id":"other","subtype":"success"}}`))
				if !tc.noAck {
					response, _ := json.Marshal(map[string]any{"response": map[string]any{
						"request_id": request.RequestID, "subtype": tc.subtype,
						"error": tc.message, "response": map[string]any{"ok": true}}})
					l.receiveControl(p, response)
					l.receiveControl(p, response)
				}
				if tc.exit {
					close(p.done)
				}
				return len(b), nil
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			data, err := l.control(ctx, p, "", map[string]any{"subtype": "interrupt"})
			if tc.message == "" {
				if err != nil || string(data) != `{"ok":true}` {
					t.Fatalf("response=%s err=%v", data, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err=%v, want %q", err, tc.message)
			}
			if len(p.controls) != 0 {
				t.Fatal("control waiter leaked")
			}
		})
	}
}

func TestSDKStreamErrorsAndDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, stream, message string }{
		{"diagnostics", "[SandboxDebug] diagnostic\n\n{\"type\":\"future_event\"}\n", ""},
		{"malformed JSON", "{\"type\":\n", "invalid Claude stream JSON"},
		{"missing type", "{}\n", "has no type"},
		{"oversize", "{" + strings.Repeat(" ", 32<<20), "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (&live{}).readMessages(&proc{}, strings.NewReader(tc.stream))
			if tc.message == "" && err != nil {
				t.Fatal(err)
			}
			if tc.message != "" && (err == nil || !strings.Contains(err.Error(), tc.message)) {
				t.Fatalf("err=%v, want %q", err, tc.message)
			}
		})
	}
	err := (&live{}).readMessages(&proc{}, sdkReaderError{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("read error=%v", err)
	}
}

type sdkReaderError struct{}

func (sdkReaderError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSDKWritesAreSerialized(t *testing.T) {
	l := &live{}
	var mu sync.Mutex
	var lines []string
	p := &proc{stdin: sdkWriter{func(b []byte) (int, error) {
		if !mu.TryLock() {
			t.Error("overlapping stdin writes")
			return 0, errors.New("overlap")
		}
		defer mu.Unlock()
		time.Sleep(time.Millisecond)
		lines = append(lines, string(b))
		return len(b), nil
	}}}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			if err := l.write(p, map[string]any{"number": i}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(lines) != 10 {
		t.Fatalf("writes=%d", len(lines))
	}
	for _, line := range lines {
		if !json.Valid([]byte(strings.TrimSpace(line))) || !strings.HasSuffix(line, "\n") {
			t.Fatalf("invalid frame %q", line)
		}
	}
}
