package deploy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the actual checked-in Caddy matcher and the server's negotiation,
// not a reimplementation of Accept-Encoding. No ocman or database is started.
func TestAssetEncoding(t *testing.T) {
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy not installed")
	}
	// Match the requests below, regardless of the developer's deployment hostname.
	t.Setenv("OCMAN_CADDY_HOST", "localhost")
	data := strings.Repeat("console.log('public build asset');\n", 1000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("ETag", `"deployment"`)
		http.ServeContent(w, r, "bundle.js", time.Time{}, strings.NewReader(data))
	}))
	defer upstream.Close()
	config, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	// Only replace deployment addresses/TLS; retain the asset matcher verbatim.
	source := strings.ReplaceAll(string(config), "localhost:8228", strings.TrimPrefix(upstream.URL, "http://"))
	source = strings.ReplaceAll(source, "localhost:8231", strings.TrimPrefix(upstream.URL, "http://"))
	cmd := exec.Command("caddy", "adapt", "--adapter", "caddyfile", "--config", "-")
	cmd.Stdin = strings.NewReader(source)
	adapted, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(adapted, &cfg); err != nil {
		t.Fatal(err)
	}
	apps := cfg["apps"].(map[string]any)
	delete(apps, "tls")
	cfg["admin"] = map[string]any{"disabled": true, "config": map[string]any{"persist": false}}
	servers := apps["http"].(map[string]any)["servers"].(map[string]any)
	dir := t.TempDir()
	socket := filepath.Join(dir, "caddy.sock")
	for _, value := range servers {
		server := value.(map[string]any)
		server["listen"] = []string{"unix/" + socket}
		server["automatic_https"] = map[string]any{"disable": true}
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "caddy.json")
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := exec.CommandContext(ctx, "caddy", "run", "--config", configPath)
	var logs bytes.Buffer
	process.Stdout = &logs
	process.Stderr = &logs
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = process.Wait() }()
	transport := &http.Transport{DisableCompression: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	// Bounded startup wait for this disposable proxy, not a background watcher.
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = process.Wait()
			t.Fatalf("caddy failed to start: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, tc := range []struct{ path, accept, method, rangeHeader, encoding string }{
		{"/assets/index-Abcd1234.js", "gzip", "GET", "", "gzip"},
		{"/plugin-shared/react.js", "gzip", "GET", "", "gzip"},
		{"/assets/index-Abcd1234.js", "gzip;q=0", "GET", "", ""},
		{"/assets/index-Abcd1234.js", "identity", "GET", "", ""},
		{"/assets/index-Abcd1234.js", "gzip", "POST", "", ""},
		{"/assets/index-Abcd1234.js", "gzip", "HEAD", "", "gzip"},
		{"/assets/index-Abcd1234.js", "gzip", "GET", "bytes=0-6", ""},
		{"/api/events", "gzip", "GET", "", ""},
		{"/api/auth/me", "gzip", "GET", "", ""},
		{"/api/files?signature=secret", "gzip", "GET", "", ""},
		{"/s/example", "gzip", "GET", "", ""},
		{"/v/example", "gzip", "GET", "", ""},
		{"/session/example", "gzip", "GET", "", ""},
	} {
		t.Run(tc.path+tc.accept+tc.method+tc.rangeHeader, func(t *testing.T) {
			r, err := http.NewRequest(tc.method, "http://localhost"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Accept-Encoding", tc.accept)
			r.Header.Set("Cookie", "auth=private")
			if tc.rangeHeader != "" {
				r.Header.Set("Range", tc.rangeHeader)
			}
			res, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = res.Body.Close() }()
			if got := res.Header.Get("Content-Encoding"); got != tc.encoding {
				t.Fatalf("encoding = %q, want %q", got, tc.encoding)
			}
			if strings.HasPrefix(tc.path, "/assets/") && tc.method != "POST" && !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
				t.Fatal("asset negotiation must vary on Accept-Encoding, including identity")
			}
			if tc.encoding == "gzip" && tc.method != "HEAD" {
				if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
					t.Fatal("missing Vary")
				}
				reader, err := gzip.NewReader(res.Body)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = reader.Close() }()
				body, err := io.ReadAll(reader)
				if err != nil || string(body) != data {
					t.Fatalf("gzip roundtrip: %v", err)
				}
			}
			if tc.method == "HEAD" {
				body, _ := io.ReadAll(res.Body)
				if len(body) != 0 {
					t.Fatal("HEAD body")
				}
			}
			if tc.rangeHeader != "" && (res.StatusCode != http.StatusPartialContent || res.Header.Get("Content-Range") == "") {
				t.Fatal("lost range semantics")
			}
		})
	}
}
