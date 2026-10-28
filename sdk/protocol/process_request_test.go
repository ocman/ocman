package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestProcessHelper(t *testing.T) {
	if len(os.Args) != 5 || os.Args[2] != "--" {
		return
	}
	mode := os.Args[3]
	enc, dec := NewEncoder(os.Stdout), NewDecoder(os.Stdin)
	d := sessionDescription()
	_ = enc.Encode(Envelope{Type: TypeHello, Hello: &Hello{Mode: ModeServe, Token: os.Getenv("OCMAN_PLUGIN_TOKEN"), Description: &d}})
	if _, err := dec.Decode(); err != nil {
		os.Exit(2)
	}
	deadline := time.Now().Add(time.Minute)
	if mode == "deadline" {
		deadline = time.Now().Add(50 * time.Millisecond)
	}
	c := Call{ID: 1, OperationID: "caller-1", Capability: "session", Version: SessionCapability.Version, Method: "run", DeadlineUnixMS: deadline.UnixMilli(), Params: json.RawMessage(`{"prompt":"go"}`)}
	_ = enc.Encode(Envelope{Type: TypeCall, Call: &c})
	if mode == "cancel" {
		_ = enc.Encode(Envelope{Type: TypeCancel, Cancel: &Cancel{ID: 1}})
	}
	for {
		e, err := dec.Decode()
		if err != nil || e.Type == TypeShutdown {
			os.Exit(0)
		}
		if e.Result != nil {
			data, _ := json.Marshal(e.Result)
			_ = os.WriteFile("reply", data, 0600)
			if mode == "late-cancel" && e.Result.ID == 1 {
				_ = enc.Encode(Envelope{Type: TypeCancel, Cancel: &Cancel{ID: 1}})
				c.ID, c.OperationID = 2, "caller-2"
				_ = enc.Encode(Envelope{Type: TypeCall, Call: &c})
			}
		}
	}
}

func TestProcessInboundRequestLifetime(t *testing.T) {
	for _, mode := range []string{"success", "error", "deadline", "cancel", "late-cancel"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "plugin")
			script := fmt.Sprintf("#!/bin/sh\nexport GORACE=atexit_sleep_ms=0\nexec %q -test.run=^TestRequestProcessHelper$ -- %q \"$@\"\n", exe, mode)
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			sum, err := executableChecksum(path, info)
			if err != nil {
				t.Fatal(err)
			}
			p, err := StartProcess(t.Context(), LaunchConfig{Candidate: Discovery{Path: path, Checksum: sum, Description: sessionDescription()}, DataDir: dir, Supported: []Capability{SessionCapability}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Stop)
			var request Request
			select {
			case request = <-p.Requests():
			case <-time.After(30 * time.Second):
				t.Fatal("missing inbound call")
			}
			if request.Call.ID != 1 || request.Call.OperationID != "caller-1" {
				t.Fatalf("lost request identity: %+v", request.Call)
			}
			category := ErrorCategory("")
			switch mode {
			case "success", "late-cancel":
				request.Respond(json.RawMessage(`{"sessionId":"ses-1"}`), nil)
				// A duplicate host response must not send a second wire result.
				request.Respond(nil, context.Canceled)
				if mode == "late-cancel" {
					select {
					case next := <-p.Requests():
						if next.Call.ID != 2 || next.Call.OperationID != "caller-2" {
							t.Fatalf("late cancel restarted or corrupted connection: %+v", next.Call)
						}
						next.Respond(json.RawMessage(`{"sessionId":"ses-1"}`), nil)
					case <-time.After(5 * time.Second):
						t.Fatal("late cancel stopped request processing")
					}
				}
			case "error":
				category = ErrorNotFound
				request.Respond(nil, &WireError{Category: category})
			case "deadline", "cancel":
				<-request.Context.Done()
				category = ErrorDeadlineExceeded
				if mode == "cancel" {
					category = ErrorCancelled
				}
				request.Respond(json.RawMessage(`{"sessionId":"late"}`), nil)
			}
			var result Result
			await(t, func() bool {
				data, err := os.ReadFile(filepath.Join(dir, "reply"))
				return err == nil && json.Unmarshal(data, &result) == nil && (mode != "late-cancel" || result.ID == 2)
			})
			wantID := uint64(1)
			if mode == "late-cancel" {
				wantID = 2
			}
			if result.ID != wantID || (category == "" && string(result.Value) != `{"sessionId":"ses-1"}`) ||
				(category != "" && (result.Error == nil || result.Error.Category != category)) {
				t.Fatalf("incorrect wire reply: %+v", result)
			}
			if p.Health().RestartCount != 0 {
				t.Fatal("response corrupted process stream")
			}
		})
	}
}
