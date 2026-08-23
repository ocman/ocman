package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// bodyDeadlineTestServer serves the three request shapes we care about
// through the real newHTTPServer configuration, so a future global
// ReadTimeout would fail these tests instead of silently breaking SSE.
func bodyDeadlineTestServer(t *testing.T) string {
	t.Helper()
	bodyTimeout := bodyReadTimeout

	mux := http.NewServeMux()
	mux.HandleFunc("/bounded", func(w http.ResponseWriter, r *http.Request) {
		var dst map[string]interface{}
		if !readAndUnmarshal(w, r, maxRequestBody, &dst) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// Mirrors the upload handlers: the generous deadline, then a full
	// body read.
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		setBodyReadDeadline(w, uploadReadTimeout)
		if _, err := io.ReadAll(io.LimitReader(r.Body, maxAudioUpload)); err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// A handler that reads a bounded body and then works for far longer
	// than the body deadline (git, tmux, an upstream agent call).
	mux.HandleFunc("/slow-work", func(w http.ResponseWriter, r *http.Request) {
		var dst map[string]interface{}
		if !readAndUnmarshal(w, r, maxRequestBody, &dst) {
			return
		}
		time.Sleep(4 * bodyTimeout)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: tick%d\n\n", i)
			_ = rc.Flush()
			time.Sleep(bodyTimeout)
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := newHTTPServer(ln.Addr().String(), mux)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// trickleRequest writes a POST whose Content-Length promises more than
// it ever delivers, mimicking a slow-body client, and returns the
// response status line (or "" if the connection died first).
func trickleRequest(t *testing.T, addr, path string, chunks []string, gap time.Duration) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	body := strings.Join(chunks, "")
	promised := len(body) + 64 // never satisfied
	head := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", path, promised)
	if _, err := conn.Write([]byte(head)); err != nil {
		t.Fatalf("write head: %v", err)
	}
	go func() {
		for _, c := range chunks {
			time.Sleep(gap)
			if _, err := conn.Write([]byte(c)); err != nil {
				return
			}
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

// TestSlowBodyIsCutOff proves a client that trickles a bounded body can
// no longer pin a handler goroutine forever: the read deadline fires and
// the handler answers 400 instead of waiting.
func TestSlowBodyIsCutOff(t *testing.T) {
	bodyReadTimeout = 150 * time.Millisecond
	uploadReadTimeout = 5 * time.Second
	t.Cleanup(func() {
		bodyReadTimeout = 30 * time.Second
		uploadReadTimeout = 5 * time.Minute
	})
	addr := bodyDeadlineTestServer(t)

	start := time.Now()
	status := trickleRequest(t, addr, "/bounded",
		[]string{`{"a":`, `1`, `}`}, 400*time.Millisecond)
	elapsed := time.Since(start)

	if !strings.Contains(status, "400") {
		t.Fatalf("status = %q, want 400 for a body that never arrives", status)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("handler waited %v; the read deadline did not fire", elapsed)
	}
}

// TestUploadBodyGetsTheLongerDeadline proves the upload handlers keep
// their own, generous bound: a body slower than bodyReadTimeout still
// completes.
func TestUploadBodyGetsTheLongerDeadline(t *testing.T) {
	bodyReadTimeout = 150 * time.Millisecond
	uploadReadTimeout = 5 * time.Second
	t.Cleanup(func() {
		bodyReadTimeout = 30 * time.Second
		uploadReadTimeout = 5 * time.Minute
	})
	addr := bodyDeadlineTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	body := `{"a":1}`
	head := fmt.Sprintf("POST /upload HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(body))
	if _, err := conn.Write([]byte(head)); err != nil {
		t.Fatalf("write head: %v", err)
	}
	// Deliver the body in slices spaced wider than bodyReadTimeout but
	// well inside uploadReadTimeout.
	for _, c := range []string{`{"a"`, `:1`, `}`} {
		time.Sleep(400 * time.Millisecond)
		if _, err := conn.Write([]byte(c)); err != nil {
			t.Fatalf("write chunk: %v", err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(line, "204") {
		t.Fatalf("status = %q, want 204: the upload deadline is too tight", strings.TrimSpace(line))
	}
}

// TestSlowHandlerNotAffectedByBodyDeadline proves the deadline bounds
// only the body read: a handler doing slow work after the body arrived
// still answers.
func TestSlowHandlerNotAffectedByBodyDeadline(t *testing.T) {
	bodyReadTimeout = 150 * time.Millisecond
	t.Cleanup(func() { bodyReadTimeout = 30 * time.Second })
	addr := bodyDeadlineTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	body := `{"a":1}`
	req := fmt.Sprintf("POST /slow-work HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(line, "204") {
		t.Fatalf("status = %q, want 204: slow post-body work was cut off", strings.TrimSpace(line))
	}
}

// TestStreamingSurvivesBodyDeadline is the guard rail: SSE responses
// outlive the body-read bound many times over, so nobody may replace the
// per-handler deadline with http.Server.ReadTimeout.
func TestStreamingSurvivesBodyDeadline(t *testing.T) {
	bodyReadTimeout = 150 * time.Millisecond
	t.Cleanup(func() { bodyReadTimeout = 30 * time.Second })
	addr := bodyDeadlineTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /sse HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	br := bufio.NewReader(conn)
	seen := 0
	for seen < 3 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended after %d ticks: %v", seen, err)
		}
		if strings.HasPrefix(line, "data: tick") {
			seen++
		}
	}
}
