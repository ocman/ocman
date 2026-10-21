package startup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var testAssets = fstest.MapFS{
	"index.html":    {Data: []byte("<html>startup app</html>")},
	"assets/app.js": {Data: []byte("test asset")},
}

func TestStartupHTTPBeforeAndAfterApplicationHandoff(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done, release, started := make(chan error, 1), make(chan struct{}), make(chan struct{})
	go func() {
		done <- Run(ctx, ln, testAssets, func(ctx context.Context) error {
			Report(ctx, "Migrating session references", 3, 10)
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			server := ApplicationServer(ctx, &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The real guarded handler still owns authentication.
				w.WriteHeader(http.StatusUnauthorized)
			})})
			return Serve(ctx, server, ln)
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	<-started
	client := &http.Client{Timeout: time.Second}
	base := "http://" + ln.Addr().String()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("startup responses may be cached")
		}
		return resp.StatusCode, string(body)
	}
	if code, body := get("/api/startup"); code != 200 || !strings.Contains(body, `"completed":3`) || !strings.Contains(body, `"total":10`) {
		t.Fatalf("progress: %d %s", code, body)
	}
	if code, _ := get("/api/sessions"); code != 503 {
		t.Fatalf("application accessible during migration: %d", code)
	}
	if code, body := get("/sessions/deep-link"); code != 200 || !strings.Contains(body, "startup app") {
		t.Fatalf("startup SPA: %d %s", code, body)
	}
	if code, body := get("/assets/app.js"); code != 200 || body != "test asset" {
		t.Fatalf("asset: %d %s", code, body)
	}
	for _, path := range []string{"/api/startup", "/api/session/s/send"} {
		resp, err := client.Post(base+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		want := 503
		if path == "/api/startup" {
			want = 405
		}
		if resp.StatusCode != want {
			t.Fatalf("POST %s = %d", path, resp.StatusCode)
		}
	}
	close(release)
	for deadline := time.Now().Add(time.Second); ; {
		_, body := get("/api/startup")
		if strings.Contains(body, `"state":"ready"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup never became ready")
		}
		time.Sleep(time.Millisecond)
	}
	if code, _ := get("/api/sessions"); code != http.StatusUnauthorized {
		t.Fatalf("handoff bypassed application auth: %d", code)
	}
}

func TestFailedCompositionRemainsVisibleWithoutLeakingError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	problem := errors.New("private database path and internal SQL error")
	go func() {
		done <- Run(ctx, ln, testAssets, func(context.Context) error { return problem })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; !errors.Is(err, problem) {
			t.Errorf("lost composition error: %v", err)
		}
	})
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(time.Second); ; {
		resp, err := client.Get("http://" + ln.Addr().String() + "/api/startup")
		if err != nil {
			t.Fatal(err)
		}
		var status Snapshot
		err = json.NewDecoder(resp.Body).Decode(&status)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(status.Phase, "private") {
			t.Fatal("raw startup error exposed publicly")
		}
		if status.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failure not reported")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStandaloneServerAndListenerFailure(t *testing.T) {
	server := &http.Server{Handler: http.NotFoundHandler(), ReadHeaderTimeout: time.Second}
	if ApplicationServer(t.Context(), server) != server {
		t.Fatal("standalone HTTP server replaced")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if err := Serve(t.Context(), server, ln); err == nil {
		t.Fatal("closed listener error swallowed")
	}
	if err := Run(t.Context(), ln, testAssets, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}); err == nil {
		t.Fatal("startup listener error swallowed")
	}
}

func TestHeadStartupPage(t *testing.T) {
	_, p := New(t.Context())
	g := &gateway{progress: p, index: []byte("index")}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(w.Result())
	}
}
