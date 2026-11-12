package projectforge

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgejo"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

type failingTransport func(*http.Request) (*http.Response, error)

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestForgejoTransportDiagnostics(t *testing.T) {
	oldTransport := http.DefaultTransport
	oldHooks := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	oldLevel := log.GetLevel()
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		log.StandardLogger().ReplaceHooks(oldHooks)
		log.SetLevel(oldLevel)
	})
	log.SetLevel(log.DebugLevel)
	hook := logtest.NewGlobal()
	t.Setenv("FORGEJO_TOKEN", "credential-secret")
	for _, tc := range []struct {
		name        string
		err         error
		cause       string
		status      int
		callerState string
	}{
		{"canceled", context.Canceled, "", 499, "canceled"},
		{"deadline", context.DeadlineExceeded, "deadline", 502, "active"},
		{"dns", &net.DNSError{Err: "credential-secret", Name: "credential-secret"}, "dns", 502, "active"},
		{"timeout", &net.OpError{Op: "dial", Err: &net.DNSError{IsTimeout: true}}, "timeout", 502, "active"},
		{"refused", syscall.ECONNREFUSED, "connection_refused", 502, "active"},
		{"reset", syscall.ECONNRESET, "connection_reset", 502, "active"},
		{"reset after caller cancel", syscall.ECONNRESET, "connection_reset", 502, "canceled"},
		{"tls", tls.RecordHeaderError{Msg: "credential-secret"}, "tls", 502, "active"},
		{"eof", io.EOF, "eof", 502, "active"},
		{"unknown", errors.New("credential-secret " + strings.Repeat("x", 4096)), "other", 502, "active"},
		{"http", nil, "http_status", 502, "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook.Reset()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			http.DefaultTransport = failingTransport(func(r *http.Request) (*http.Response, error) {
				if tc.callerState == "canceled" {
					cancel()
				}
				if tc.err != nil {
					return nil, tc.err
				}
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("credential-secret")), Request: r}, nil
			})
			client := forgejo.NewClient("forgejo.example", "https://forgejo.example/credential-secret", "")
			_, _, err := client.Checks(ctx, "dries/ocman", "abc")
			if err == nil {
				t.Fatal("mock transport did not fail")
			}
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/project/pr-checks?token=credential-secret", nil).WithContext(ctx)
			writeForgeError(rr, req, forge.Remote{Host: "forgejo.example", Repo: "dries/ocman"}, err)
			if rr.Code != tc.status {
				t.Errorf("status=%d want %d", rr.Code, tc.status)
			}
			entry := hook.LastEntry()
			if entry == nil {
				t.Fatal("missing diagnostic")
			}
			if tc.status == 502 {
				if entry.Level != log.WarnLevel || entry.Data["cause"] != tc.cause {
					t.Errorf("diagnostic=%v want warning cause=%s", entry, tc.cause)
				}
				wantUpstream := 0
				if tc.name == "http" {
					wantUpstream = 503
				}
				for key, want := range map[string]any{"upstream_status": wantUpstream, "route": "/api/project/pr-checks", "forge_host": "forgejo.example", "repo": "dries/ocman", "request_context": tc.callerState} {
					if entry.Data[key] != want {
						t.Errorf("%s=%v want %v", key, entry.Data[key], want)
					}
				}
			} else if entry.Level != log.DebugLevel {
				t.Errorf("canceled request logged at %v", entry.Level)
			}
			for _, entry := range hook.AllEntries() {
				text, _ := entry.String()
				if strings.Contains(text, "credential-secret") || len(text) > 1024 {
					t.Errorf("unsafe or unbounded diagnostic")
				}
			}
			if strings.Contains(rr.Body.String(), "credential-secret") {
				t.Fatal("secret in response")
			}
		})
	}
}
