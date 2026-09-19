// Package conformance supplies black-box tests for native plugin executables,
// including executables implemented without the Go SDK. Test calls must be
// deterministic and side-effect free. Run these against a test configuration.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

// Cases supplies plugin-specific payloads. Success must return an action.v1
// result, Wait must wait for cancellation/deadline, and Error must return the
// specified normalized category. Stream is optional for unary-only plugins.
// IDs, operation IDs and deadlines are assigned by the suite.
type Cases struct {
	Success       plugin.Call
	Wait          plugin.Call
	Error         plugin.Call
	ErrorCategory plugin.ErrorCategory
	Stream        *plugin.Call
}

type connection struct {
	t       *testing.T
	cmd     *exec.Cmd
	in      io.WriteCloser
	decoder *plugin.Decoder
	stream  *plugin.Stream
	id      uint64
	ctx     context.Context
}

func start(t *testing.T, executable string, mode plugin.Mode, token string) (*connection, plugin.Description) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, string(mode))
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "OCMAN_PLUGIN_TOKEN=" + token}
	cmd.Dir = t.TempDir()
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = out.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	c := &connection{t: t, cmd: cmd, in: in, decoder: plugin.NewDecoder(out), ctx: ctx}
	hello, err := c.decoder.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if hello.Hello == nil || hello.Hello.Description == nil {
		t.Fatal("expected description hello")
	}
	d := *hello.Hello.Description
	c.stream, err = plugin.NewStream(mode, token, plugin.Version{Major: 1}, d.Capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.stream.Accept(plugin.FromPlugin, hello); err != nil {
		t.Fatal(err)
	}
	return c, d
}

func (c *connection) send(e plugin.Envelope, additive bool) {
	c.t.Helper()
	if err := c.stream.Accept(plugin.FromHost, e); err != nil {
		c.t.Fatal(err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		c.t.Fatal(err)
	}
	if additive {
		data = append([]byte(`{"future":{"ignored":true},`), data[1:]...)
		data = []byte(strings.Replace(string(data), `"`+string(e.Type)+`":{`, `"`+string(e.Type)+`":{"future":true,`, 1))
	}
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

func (c *connection) ready() {
	n, _ := c.stream.Negotiation()
	c.send(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &n}}, true)
}

func (c *connection) call(call plugin.Call, deadline time.Time) uint64 {
	c.id++
	call.ID, call.OperationID, call.DeadlineUnixMS = c.id, fmt.Sprintf("conformance-%d", c.id), deadline.UnixMilli()
	c.send(plugin.Envelope{Type: plugin.TypeCall, Call: &call}, true)
	return call.ID
}

func (c *connection) result(id uint64) (plugin.Result, int) {
	c.t.Helper()
	chunks := 0
	for {
		e, err := c.decoder.Decode()
		if err != nil {
			c.t.Fatal(err)
		}
		if err := c.stream.Accept(plugin.FromPlugin, e); err != nil {
			c.t.Fatal(err)
		}
		if e.Chunk != nil {
			if e.Chunk.ID != id {
				c.t.Fatal("wrong chunk ID")
			}
			chunks++
		}
		if e.Result != nil {
			if e.Result.ID != id {
				c.t.Fatal("wrong result ID")
			}
			return *e.Result, chunks
		}
	}
}

func (c *connection) exit() {
	c.t.Helper()
	if _, err := c.decoder.Decode(); !errors.Is(err, io.EOF) {
		c.t.Fatalf("expected EOF, got %v", err)
	}
	if err := c.cmd.Wait(); err != nil {
		c.t.Fatal(err)
	}
}

// Run checks launch binding, stable descriptions, lower-minor negotiation,
// additive envelope fields, action result framing, normalized errors, elapsed
// deadlines, advisory cancellation, optional streaming and clean shutdown.
func Run(t *testing.T, executable string, cases Cases) {
	t.Helper()
	t.Run("describe-binding", func(t *testing.T) {
		c, d := start(t, executable, plugin.ModeDescribe, strings.Repeat("ab", 32))
		description := d
		c.exit()
		c, d = start(t, executable, plugin.ModeDescribe, strings.Repeat("cd", 32))
		if !reflect.DeepEqual(description, d) {
			t.Fatal("description changed across launches")
		}
		c.exit()
	})
	t.Run("serve", func(t *testing.T) {
		describe, description := start(t, executable, plugin.ModeDescribe, strings.Repeat("34", 32))
		describe.exit()
		c, d := start(t, executable, plugin.ModeServe, strings.Repeat("ef", 32))
		if !reflect.DeepEqual(description, d) {
			t.Fatal("serve description differs from describe")
		}
		c.ready()
		id := c.call(cases.Success, time.Now().Add(time.Second))
		r, chunks := c.result(id)
		if r.Error != nil || chunks != 0 {
			t.Fatalf("action must succeed without chunks: %+v", r)
		}
		if _, err := plugin.DecodeActionResults(r.Value); err != nil {
			t.Fatal(err)
		}
		id = c.call(cases.Error, time.Now().Add(time.Second))
		r, _ = c.result(id)
		if r.Error == nil || r.Error.Category != cases.ErrorCategory {
			t.Fatalf("unexpected normalized error: %+v", r)
		}
		for _, expired := range []bool{true, false} {
			deadline := time.Now().Add(100 * time.Millisecond)
			if expired {
				deadline = time.Now().Add(-time.Second)
			}
			id = c.call(cases.Wait, deadline)
			r, _ = c.result(id)
			if r.Error == nil || r.Error.Category != plugin.ErrorDeadlineExceeded {
				t.Fatalf("deadline: %+v", r)
			}
		}
		id = c.call(cases.Wait, time.Now().Add(time.Second))
		c.send(plugin.Envelope{Type: plugin.TypeCancel, Cancel: &plugin.Cancel{ID: id}}, false)
		r, _ = c.result(id)
		if r.Error == nil || r.Error.Category != plugin.ErrorCancelled {
			t.Fatalf("cancel: %+v", r)
		}
		if cases.Stream != nil {
			id = c.call(*cases.Stream, time.Now().Add(time.Second))
			r, chunks = c.result(id)
			if r.Error != nil || chunks == 0 {
				t.Fatal("expected chunks then successful result")
			}
		}
		c.call(cases.Wait, time.Now().Add(time.Second))
		c.send(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}, false)
		c.exit()
	})
	t.Run("capability-subset", func(t *testing.T) {
		c, _ := start(t, executable, plugin.ModeServe, strings.Repeat("56", 32))
		// Hosts may omit unsupported capability majors independently of the
		// process version. No calls are legal with this empty selection.
		encoder := plugin.NewEncoder(c.in)
		if err := encoder.Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &plugin.Negotiated{Protocol: plugin.Version{Major: 1}}}}); err != nil {
			t.Fatal(err)
		}
		if err := encoder.Encode(plugin.Envelope{Type: plugin.TypeShutdown, Shutdown: &plugin.Shutdown{}}); err != nil {
			t.Fatal(err)
		}
		c.exit()
	})
	for name, accepted := range map[string]plugin.Negotiated{
		"incompatible-major":   {Protocol: plugin.Version{Major: 2}},
		"unoffered-capability": {Protocol: plugin.Version{Major: 1}, Capabilities: []plugin.Capability{{Name: "conformance-unoffered", Version: plugin.Version{Major: 1}}}},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := start(t, executable, plugin.ModeServe, strings.Repeat("12", 32))
			if err := plugin.NewEncoder(c.in).Encode(plugin.Envelope{Type: plugin.TypeHello, Hello: &plugin.Hello{Mode: plugin.ModeServe, Accepted: &accepted}}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.decoder.Decode(); !errors.Is(err, io.EOF) {
				t.Fatalf("invalid negotiation must close connection: %v", err)
			}
			_ = c.cmd.Wait() // A rejected handshake may exit nonzero.
			if err := c.ctx.Err(); err != nil {
				t.Fatalf("plugin did not reject handshake before test timeout: %v", err)
			}
		})
	}
}
