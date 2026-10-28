package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor/providertest"
)

func emit(t *testing.T, p *providertest.Process, kind, properties string) {
	t.Helper()
	if status := providertest.Call(t, p.Client, "POST", "/test/event", providerprotocol.Event{Type: kind, Properties: json.RawMessage(properties)}, nil); status != 204 {
		t.Fatal(status)
	}
}

func nextFrame(t *testing.T, scanner *bufio.Scanner) string {
	t.Helper()
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			return strings.TrimPrefix(scanner.Text(), "data: ")
		}
	}
	t.Fatal("missing SSE frame", scanner.Err())
	return ""
}

func TestDashboardRestartReplaysEveryEventAndPendingPermission(t *testing.T) {
	binary := providertest.Binary(t)
	p := providertest.Start(t, binary, providerprotocol.CurrentVersion)
	emit(t, p, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
	emit(t, p, "message.part.updated", `{"part":{"sessionID":"s","id":"part","messageID":"m","type":"text","text":""}}`)
	// The first dashboard consumes two frames, then dies. The provider process
	// continues and appends deltas without an attached dashboard.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider/events?after=0", nil)
	resp, err := p.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(resp.Body)
	_ = nextFrame(t, scanner)
	_ = nextFrame(t, scanner)
	_ = resp.Body.Close()
	for i := range 5 {
		emit(t, p, "message.part.delta", fmt.Sprintf(`{"sessionID":"s","messageID":"m","partID":"part","field":"text","delta":"%d"}`, i))
	}
	emit(t, p, "permission.asked", `{"sessionID":"s","id":"p","permission":"bash"}`)
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://provider/events?after=2&consumer=headless", nil)
	resp, err = p.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	scanner = bufio.NewScanner(resp.Body)
	for i := range 5 {
		frame := nextFrame(t, scanner)
		if !strings.Contains(frame, fmt.Sprintf(`"delta":"%d"`, i)) {
			t.Fatal("lost/reordered delta", frame)
		}
	}
	if frame := nextFrame(t, scanner); !strings.Contains(frame, "permission.asked") {
		t.Fatal(frame)
	}
	_ = resp.Body.Close()
	// Reconnect past the prompt's original sequence. It must be reissued under
	// a fresh sequence, not silently discarded by the adapter's seq dedup.
	a, err := New(ctx, p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.seq = 8
	got := make(chan string, 8)
	a.SetEventHandler(func(event providerprotocol.Event) { got <- event.Type })
	watchCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = a.WatchEvents(watchCtx) }()
	select {
	case kind := <-got:
		if kind != "permission.asked" {
			t.Fatal(kind)
		}
	case <-ctx.Done():
		t.Fatal("prompt not redelivered")
	}
	if status := providertest.Call(t, p.Client, "POST", "/permissions/p/reply", map[string]string{"reply": "once"}, nil); status != 204 {
		t.Fatal(status)
	}
	select {
	case kind := <-got:
		if kind != "permission.replied" {
			t.Fatal(kind)
		}
	case <-ctx.Done():
		t.Fatal("prompt not answerable")
	}
	prompts, err := a.ListPermissions(ctx, "s")
	if err != nil || len(prompts) != 0 {
		t.Fatal(prompts, err)
	}
}

type eventWriter struct{ events chan string }

func (w eventWriter) Write(p []byte) (int, error) { w.events <- string(p); return len(p), nil }

func TestSnapshotLeaseClosesAcknowledgedTurnGap(t *testing.T) {
	p := providertest.Start(t, providertest.Binary(t), providerprotocol.CurrentVersion)
	emit(t, p, "message.part.updated", `{"part":{"sessionID":"s","id":"part","messageID":"m","type":"text","text":"old"}}`)
	// This represents a browser's old REST snapshot. ProxyEvents takes a fresh
	// snapshot and delivers full upserts before any post-cursor deltas.
	a, err := New(t.Context(), p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	emit(t, p, "message.part.delta", `{"sessionID":"s","delta":"new"}`)
	emit(t, p, "session.idle", `{"sessionID":"s"}`)
	if err := a.AcknowledgeTurn(t.Context(), "s", 3); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	w := eventWriter{events: make(chan string, 16)}
	done := make(chan error, 1)
	go func() { done <- a.ProxyEvents(ctx, "s", w, nil) }()
	found := false
	for !found {
		select {
		case frame := <-w.events:
			found = strings.Contains(frame, `"text":"oldnew"`)
		case <-ctx.Done():
			t.Fatal("snapshot failed to repair REST gap")
		}
	}
	emit(t, p, "message.part.delta", `{"sessionID":"s","delta":"later"}`)
	for {
		select {
		case frame := <-w.events:
			if strings.Contains(frame, `"delta":"later"`) {
				cancel()
				<-done
				return
			}
		case <-ctx.Done():
			t.Fatal("post-snapshot delta lost")
		}
	}
}

// Run by TestKilledDashboardProcessReplaysAfterItsLastCursor as a disposable
// dashboard-side process. It uses only the fake provider socket.
func TestDashboardConsumerProcess(t *testing.T) {
	socket := os.Getenv("OCMAN_TEST_CONSUMER_SOCKET")
	if socket == "" {
		return
	}
	a, err := New(t.Context(), socket)
	if err != nil {
		t.Fatal(err)
	}
	a.seq, err = strconv.ParseUint(os.Getenv("OCMAN_TEST_CONSUMER_AFTER"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	a.SetEventHandler(func(event providerprotocol.Event) {
		a.mu.RLock()
		seq := a.seq
		a.mu.RUnlock()
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Sequence uint64                 `json:"sequence"`
			Event    providerprotocol.Event `json:"event"`
		}{seq, event}); err != nil {
			panic(err)
		}
	})
	_ = a.WatchEvents(context.Background())
}

func TestKilledDashboardProcessReplaysAfterItsLastCursor(t *testing.T) {
	p := providertest.Start(t, providertest.Binary(t), providerprotocol.CurrentVersion)
	start := func(after uint64) (*exec.Cmd, *bufio.Scanner) {
		t.Helper()
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestDashboardConsumerProcess$")
		cmd.Env = append(os.Environ(), "OCMAN_TEST_CONSUMER_SOCKET="+p.Socket, "OCMAN_TEST_CONSUMER_AFTER="+strconv.FormatUint(after, 10))
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd, bufio.NewScanner(stdout)
	}
	read := func(scanner *bufio.Scanner) uint64 {
		t.Helper()
		result := make(chan uint64, 1)
		go func() {
			if scanner.Scan() {
				var rec struct {
					Sequence uint64 `json:"sequence"`
				}
				_ = json.Unmarshal(scanner.Bytes(), &rec)
				result <- rec.Sequence
			}
		}()
		select {
		case seq := <-result:
			return seq
		case <-time.After(5 * time.Second):
			t.Fatal("dashboard consumer did not receive event")
			return 0
		}
	}
	emit(t, p, "session.status", `{"sessionID":"s","status":{"type":"busy"}}`)
	first, scanner := start(0)
	if seq := read(scanner); seq != 1 {
		t.Fatal(seq)
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	for i := range 5 {
		emit(t, p, "message.part.delta", fmt.Sprintf(`{"sessionID":"s","delta":"%d"}`, i))
	}
	_, scanner = start(1)
	for seq := uint64(2); seq <= 6; seq++ {
		if got := read(scanner); got != seq {
			t.Fatalf("lost/reordered event: want %d got %d", seq, got)
		}
	}
}

func TestAcknowledgmentFailureRetriesWithoutRepeatingLifecycle(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.hello.Capabilities.Replay = true
	f.reply("POST", "/sessions/s/ack", nil)
	a := f.adapter(t)
	failure := true
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/sessions/s/ack" && failure {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	}
	if err := a.acknowledge(t.Context(), "s", 12); err == nil {
		t.Fatal("ack failure swallowed")
	}
	if a.acks["s"] != 12 {
		t.Fatal("ack was not retained")
	}
	failure = false
	a.retryAcknowledgments(t.Context())
	if len(a.acks) != 0 {
		t.Fatal("ack retry failed")
	}
}
