package ocv2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestMajor(t *testing.T) {
	tests := map[string]int{
		"opencode v2.0.22": 2,
		"v2.0.22":          2,
		"2.0.22":           2,
		"1.18.32":          1,
		"10.1.0\n":         10,
		"":                 0,
		"garbage":          0,
		"   ":              0,
	}
	for in, want := range tests {
		if got := Major(in); got != want {
			t.Errorf("Major(%q) = %d, want %d", in, got, want)
		}
	}
}

// convResetInstalled clears the InstalledV2 cache and stubs the version
// command, restoring both on cleanup.
func convResetInstalled(t *testing.T, out string, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := versionCommand
	versionCommand = func(context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte(out), err
	}
	reset := func() {
		installed.mu.Lock()
		installed.done = false
		installed.v2 = false
		installed.mu.Unlock()
	}
	reset()
	t.Cleanup(func() {
		versionCommand = prev
		reset()
	})
	return &calls
}

func TestInstalledV2(t *testing.T) {
	tests := []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"v2", "opencode v2.0.22\n", nil, true},
		{"v1", "1.18.32\n", nil, false},
		{"error", "opencode v2.0.22", errors.New("not found"), false},
		{"garbage", "???", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := convResetInstalled(t, tt.out, tt.err)
			if got := InstalledV2(); got != tt.want {
				t.Errorf("InstalledV2() = %v, want %v", got, tt.want)
			}
			if got := InstalledV2(); got != tt.want {
				t.Errorf("cached InstalledV2() = %v, want %v", got, tt.want)
			}
			if n := calls.Load(); n != 1 {
				t.Errorf("version command ran %d times, want 1 (cached)", n)
			}
		})
	}
}

// The version is read once: a later upgrade must not flip the answer
// mid-run (the password, DB views and managed server were chosen for it).
func TestInstalledV2IsFixedForTheProcess(t *testing.T) {
	calls := convResetInstalled(t, "1.18.0", nil)
	if InstalledV2() {
		t.Fatal("want v1")
	}
	versionCommand = func(context.Context) ([]byte, error) { calls.Add(1); return []byte("opencode v2.0.22"), nil }
	if InstalledV2() || calls.Load() != 1 {
		t.Fatalf("InstalledV2 changed mid-run (calls=%d)", calls.Load())
	}
	if v2, ok := readInstalledV2(); !ok || !v2 {
		t.Fatalf("readInstalledV2 = %v %v, want the new version for the watcher", v2, ok)
	}
}

func TestSetInstalledV2(t *testing.T) {
	calls := convResetInstalled(t, "1.0.0", nil)
	restore := SetInstalledV2(true)
	if !InstalledV2() {
		t.Error("override true ignored")
	}
	inner := SetInstalledV2(false)
	if InstalledV2() {
		t.Error("nested override false ignored")
	}
	inner()
	if !InstalledV2() {
		t.Error("inner restore did not return to true")
	}
	restore()
	if calls.Load() != 0 {
		t.Error("version command ran while overridden")
	}
	if InstalledV2() {
		t.Error("after restore, want detected v1 = false")
	}
}

// convInfoServer starts a server whose GET /api/info is answered by h,
// counting requests. It returns the server's host:port and port.
func convInfoServer(t *testing.T, h http.HandlerFunc) (host, port string, hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/info" {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ForgetHost(u.Host) })
	return u.Host, u.Port(), hits
}

func convV2Info(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"version":"2.0.22"}`))
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
		want bool
	}{
		{"v2 json", convV2Info, true},
		{"v1 json version", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"version":"1.18.32"}`))
		}, false},
		{"404", http.NotFound, false},
		{"html 200", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html>{"version":"2.0.0"}</html>`))
		}, false},
		{"bad json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{`))
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, _, hits := convInfoServer(t, tt.h)
			for i := 0; i < 2; i++ {
				got, err := detect(context.Background(), http.DefaultTransport, "http", host)
				if err != nil {
					t.Fatalf("detect: %v", err)
				}
				if got != tt.want {
					t.Errorf("call %d: detect = %v, want %v", i, got, tt.want)
				}
			}
			if n := hits.Load(); n != 1 {
				t.Errorf("server hit %d times, want 1 (cached)", n)
			}
			ForgetHost(host)
			if _, err := detect(context.Background(), http.DefaultTransport, "http", host); err != nil {
				t.Fatalf("detect after forget: %v", err)
			}
			if n := hits.Load(); n != 2 {
				t.Errorf("after ForgetHost server hit %d times, want 2", n)
			}
		})
	}
}

func TestDetectTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	host := srv.Listener.Addr().String()
	srv.Close()
	t.Cleanup(func() { ForgetHost(host) })
	if _, err := detect(context.Background(), http.DefaultTransport, "http", host); err == nil {
		t.Error("detect on closed server: err = nil")
	}
	if _, cached := hosts.Load(host); cached {
		t.Error("transport error was cached")
	}
}

func TestDetectExpires(t *testing.T) {
	host, _, hits := convInfoServer(t, convV2Info)
	if _, err := detect(context.Background(), http.DefaultTransport, "http", host); err != nil {
		t.Fatal(err)
	}
	hosts.Store(host, hostEntry{v2: false, at: time.Now().Add(-2 * hostTTL)})
	got, err := detect(context.Background(), http.DefaultTransport, "http", host)
	if err != nil || !got {
		t.Errorf("detect after TTL = %v, %v; want true, nil", got, err)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("server hit %d times, want 2", n)
	}
}

func TestIsV2(t *testing.T) {
	prevRT := probeRT.Load()
	t.Cleanup(func() { probeRT.Store(prevRT) })

	v2Host, v2Port, v2Hits := convInfoServer(t, convV2Info)
	_, v1Port, _ := convInfoServer(t, http.NotFound)
	_, htmlPort, _ := convInfoServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	})
	ctx := context.Background()

	t.Run("installed v1 never probes", func(t *testing.T) {
		defer SetInstalledV2(false)()
		Wrap(http.DefaultTransport)
		if IsV2(ctx, v2Port) {
			t.Error("IsV2 = true with v1 installed")
		}
		if n := v2Hits.Load(); n != 0 {
			t.Errorf("server probed %d times, want 0", n)
		}
	})

	t.Run("no transport", func(t *testing.T) {
		defer SetInstalledV2(true)()
		saved := probeRT.Load()
		probeRT.Store(nil)
		defer probeRT.Store(saved)
		if IsV2(ctx, v2Port) {
			t.Error("IsV2 = true without a recorded transport")
		}
	})

	t.Run("detects", func(t *testing.T) {
		defer SetInstalledV2(true)()
		Wrap(http.DefaultTransport)
		for port, want := range map[string]bool{v2Port: true, v1Port: false, htmlPort: false} {
			if got := IsV2(ctx, port); got != want {
				t.Errorf("IsV2(%s) = %v, want %v", port, got, want)
			}
		}
		before := v2Hits.Load()
		if !IsV2(ctx, v2Port) {
			t.Error("cached IsV2 = false")
		}
		if v2Hits.Load() != before {
			t.Error("cached IsV2 hit the server")
		}
		ForgetHost(v2Host)
		if !IsV2(ctx, v2Port) || v2Hits.Load() != before+1 {
			t.Error("ForgetHost did not force a re-probe")
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		defer SetInstalledV2(true)()
		Wrap(http.DefaultTransport)
		srv := httptest.NewServer(http.NotFoundHandler())
		u, _ := url.Parse(srv.URL)
		srv.Close()
		if IsV2(ctx, u.Port()) {
			t.Error("IsV2 = true for closed port")
		}
	})
}
