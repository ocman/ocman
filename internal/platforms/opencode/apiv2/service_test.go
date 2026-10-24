package ocv2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeService(t *testing.T, body string) {
	t.Helper()
	path := ServiceStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunningService(t *testing.T) {
	alive := map[int]bool{4242: true}
	prev := processAlive
	processAlive = func(pid int) bool { return alive[pid] }
	t.Cleanup(func() { processAlive = prev })

	for _, tc := range []struct {
		name, body string
		want       Service
		wantErr    bool
	}{
		{name: "missing", wantErr: true},
		{name: "live", body: `{"url":"http://127.0.0.1:49374/","pid":4242,"version":"2.0.31","password":"x"}`,
			want: Service{URL: "http://127.0.0.1:49374", PID: 4242, Version: "2.0.31"}},
		{name: "dead pid", body: `{"url":"http://127.0.0.1:49374","pid":7,"version":"2.0.31"}`, wantErr: true},
		{name: "not loopback", body: `{"url":"http://10.0.0.2:49374","pid":4242,"version":"2.0.31"}`, wantErr: true},
		{name: "https", body: `{"url":"https://127.0.0.1:49374","pid":4242,"version":"2.0.31"}`, wantErr: true},
		{name: "no port", body: `{"url":"http://127.0.0.1","pid":4242,"version":"2.0.31"}`, wantErr: true},
		{name: "v1", body: `{"url":"http://127.0.0.1:49374","pid":4242,"version":"1.18.0"}`, wantErr: true},
		{name: "garbage", body: `{`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			if tc.body != "" {
				writeService(t, tc.body)
			}
			got, err := RunningService()
			if tc.wantErr {
				if !errors.Is(err, ErrNoService) {
					t.Fatalf("err = %v; want ErrNoService", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("RunningService() = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestServicePassword(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if pw, err := ServicePassword(); pw != "" || err != nil {
		t.Fatalf("missing config = %q, %v; want empty", pw, err)
	}
	path := ServiceConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"password":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if pw, err := ServicePassword(); pw != "secret" || err != nil {
		t.Fatalf("ServicePassword() = %q, %v; want secret", pw, err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ServicePassword(); err == nil {
		t.Fatal("invalid config parsed without error")
	}
}

func TestServicePathsFallBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "relative")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := ServiceStatePath(), filepath.Join(home, ".local", "state", "opencode", "service.json"); got != want {
		t.Errorf("state path = %q; want %q", got, want)
	}
	if got, want := ServiceConfigPath(), filepath.Join(home, ".config", "opencode", "service.json"); got != want {
		t.Errorf("config path = %q; want %q", got, want)
	}
}

func TestProcessAliveSelf(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("own process reported dead")
	}
}
