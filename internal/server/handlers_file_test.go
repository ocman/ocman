package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func newFileTestServer(t *testing.T) *Server {
	t.Helper()
	stDB, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { stDB.Close() })
	return New(nil, stDB, "127.0.0.1:8228", platforms.NewRegistry(), nil)
}

func TestFileToken_RoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	path := "/tmp/some dir/diagram.svg"

	tok := signFilePath(key, path)
	got, ok := verifyFileToken(key, tok)
	if !ok || got != path {
		t.Fatalf("verifyFileToken = %q, %v; want %q, true", got, ok, path)
	}
	if strings.ContainsAny(tok, "/?#") {
		t.Errorf("token %q must be URL-path safe", tok)
	}
}

func TestFileToken_Rejects(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	other := []byte("fedcba9876543210fedcba9876543210")
	valid := signFilePath(key, "/etc/hosts")

	tampered := signFilePath(key, "/tmp/a") // swap the payload, keep a real sig
	forged := strings.SplitN(signFilePath(key, "/etc/shadow"), ".", 2)[0] + "." + strings.SplitN(tampered, ".", 2)[1]

	tests := []struct{ name, token string }{
		{"empty", ""},
		{"no separator", "abcdef"},
		{"bad base64", "!!!.###"},
		{"wrong key", signFilePath(other, "/etc/hosts")},
		{"swapped payload", forged},
		{"truncated sig", valid[:len(valid)-4]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := verifyFileToken(key, tt.token); ok {
				t.Errorf("verifyFileToken(%q) accepted an invalid token", tt.token)
			}
		})
	}
}

func TestFileTokenSecret_StableAcrossCalls(t *testing.T) {
	s := newFileTestServer(t)
	a, err := s.fileTokenSecret(t.Context())
	if err != nil {
		t.Fatalf("fileTokenSecret: %v", err)
	}
	if len(a) < 16 {
		t.Fatalf("key too short: %d bytes", len(a))
	}
	b, _ := s.fileTokenSecret(t.Context())
	if string(a) != string(b) {
		t.Error("fileTokenSecret returned different keys across calls")
	}
	// A fresh Server over the same state DB must reuse the persisted key
	// so links survive a restart.
	s2 := &Server{stateDB: s.stateDB}
	c, err := s2.fileTokenSecret(t.Context())
	if err != nil {
		t.Fatalf("fileTokenSecret (restart): %v", err)
	}
	if string(a) != string(c) {
		t.Error("file token secret was not persisted")
	}
}

func TestFileURL_AbsoluteAndVerifiable(t *testing.T) {
	s := newFileTestServer(t)
	url, err := s.FileURL(t.Context(), "/tmp/chart.png")
	if err != nil {
		t.Fatalf("FileURL: %v", err)
	}
	const want = "http://127.0.0.1:8228/api/file/"
	if !strings.HasPrefix(url, want) {
		t.Fatalf("FileURL = %q, want prefix %q", url, want)
	}
	key, _ := s.fileTokenSecret(t.Context())
	if got, ok := verifyFileToken(key, strings.TrimPrefix(url, want)); !ok || got != "/tmp/chart.png" {
		t.Errorf("token in URL did not verify: %q, %v", got, ok)
	}

	s.publicBaseURL = "https://ocman.example.com"
	url, _ = s.FileURL(t.Context(), "/tmp/chart.png")
	if !strings.HasPrefix(url, "https://ocman.example.com/api/file/") {
		t.Errorf("FileURL ignored publicBaseURL: %q", url)
	}
}

func TestFileURLUsesCallerContext(t *testing.T) {
	s := newFileTestServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := s.FileURL(ctx, "/tmp/chart.png")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FileURL error = %v, want context.Canceled", err)
	}
}

func TestHandleFileProxy(t *testing.T) {
	s := newFileTestServer(t)
	dir := t.TempDir()
	png := filepath.Join(dir, "diagram.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nfake"), 0o600); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(dir, "bundle.zip")
	if err := os.WriteFile(zip, []byte("PK\x03\x04"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, _ := s.fileTokenSecret(t.Context())

	t.Run("serves an image inline", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, filePathPrefix+signFilePath(key, png), nil)
		s.handleFileProxy(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.Body.String() != "\x89PNG\r\n\x1a\nfake" {
			t.Errorf("body = %q", rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
			t.Errorf("Content-Type = %q, want image/png", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
			t.Errorf("Content-Disposition = %q, want inline", cd)
		}
		if rec.Header().Get("Content-Security-Policy") != "sandbox" {
			t.Errorf("missing sandbox CSP: %q", rec.Header().Get("Content-Security-Policy"))
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("missing nosniff")
		}
	})

	t.Run("downloads an opaque type", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, filePathPrefix+signFilePath(key, zip), nil)
		s.handleFileProxy(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
			t.Errorf("Content-Disposition = %q, want attachment", cd)
		}
	})

	t.Run("rejects an unsigned path", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, filePathPrefix+"L2V0Yy9wYXNzd2Q.deadbeef", nil)
		s.handleFileProxy(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("404 for a signed but missing file", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, filePathPrefix+signFilePath(key, filepath.Join(dir, "gone.png")), nil)
		s.handleFileProxy(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("404 for a directory", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, filePathPrefix+signFilePath(key, dir), nil)
		s.handleFileProxy(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestHandleFileProxyUsesRequestContext(t *testing.T) {
	s := newFileTestServer(t)
	req := httptest.NewRequest(http.MethodGet, filePathPrefix+"invalid", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	rec := httptest.NewRecorder()

	s.handleFileProxy(rec, req.WithContext(ctx))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestFileDisposition(t *testing.T) {
	tests := []struct {
		ctype string
		want  string
	}{
		{"image/png", "inline"},
		{"image/svg+xml", "inline"},
		{"application/pdf", "inline"},
		{"text/plain; charset=utf-8", "inline"},
		{"video/mp4", "inline"},
		{"application/zip", "attachment"},
		{"application/octet-stream", "attachment"},
	}
	for _, tt := range tests {
		if got := fileDisposition(tt.ctype); got != tt.want {
			t.Errorf("fileDisposition(%q) = %q, want %q", tt.ctype, got, tt.want)
		}
	}
}

func TestFileContentType_UnknownExtension(t *testing.T) {
	if got := fileContentType("/tmp/thing.weirdext"); got != "application/octet-stream" {
		t.Errorf("fileContentType = %q, want application/octet-stream", got)
	}
}
