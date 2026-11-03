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

// convStubVersion stubs the version command, restoring it on cleanup.
func convStubVersion(t *testing.T, out string, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	prev := versionCommand
	versionCommand = func(context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte(out), err
	}
	t.Cleanup(func() { versionCommand = prev })
	return &calls
}

func TestInstalledMajor(t *testing.T) {
	tests := []struct {
		name      string
		out       string
		err       error
		wantMajor int
		wantOK    bool
	}{
		{"v2", "opencode v2.0.22\n", nil, 2, true},
		{"v1", "1.18.32\n", nil, 1, true},
		{"missing", "", errors.New("not found"), 0, false},
		{"garbage", "???", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			convStubVersion(t, tt.out, tt.err)
			major, ok := InstalledMajor()
			if major != tt.wantMajor || ok != tt.wantOK {
				t.Errorf("InstalledMajor() = %d, %v; want %d, %v", major, ok, tt.wantMajor, tt.wantOK)
			}
		})
	}
}

// The watcher keeps polling while v2 stays installed and stops after
// reporting a downgrade below v2.
func TestWatchInstalledVersion(t *testing.T) {
	prev := versionCheckInterval
	versionCheckInterval = time.Millisecond
	t.Cleanup(func() { versionCheckInterval = prev })

	t.Run("v2 keeps watching", func(t *testing.T) {
		calls := convStubVersion(t, "opencode v2.0.22", nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { WatchInstalledVersion(ctx); close(done) }()
		for calls.Load() < 3 {
			time.Sleep(time.Millisecond)
		}
		select {
		case <-done:
			t.Fatal("watcher stopped while v2 stayed installed")
		default:
		}
		cancel()
		<-done
	})

	t.Run("downgrade stops", func(t *testing.T) {
		calls := convStubVersion(t, "1.18.32", nil)
		done := make(chan struct{})
		go func() { WatchInstalledVersion(t.Context()); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("watcher kept running after a downgrade")
		}
		if calls.Load() != 1 {
			t.Fatalf("version read %d times, want 1", calls.Load())
		}
	})
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

func TestResetHostCacheForgetsDialects(t *testing.T) {
	hosts.Store("127.0.0.1:9", hostEntry{v2: true, at: time.Now()})
	ResetHostCache()
	if _, ok := hosts.Load("127.0.0.1:9"); ok {
		t.Fatal("dialect survived ResetHostCache")
	}
}
