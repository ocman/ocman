package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReadLimited covers the shared upstream read bound: bodies inside
// the limit come back whole, bodies past it are refused instead of being
// buffered into memory.
func TestReadLimited(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		limit   int64
		wantErr bool
	}{
		{"under limit", 10, 100, false},
		{"exactly at limit", 100, 100, false},
		{"one byte over limit", 101, 100, true},
		{"far over limit", 10000, 100, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := readLimited(strings.NewReader(strings.Repeat("a", tt.size)), tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("readLimited(%d bytes, limit %d) err = %v, wantErr %v", tt.size, tt.limit, err, tt.wantErr)
			}
			if tt.wantErr {
				if body != nil {
					t.Errorf("oversized body must not be returned, got %d bytes", len(body))
				}
				return
			}
			if len(body) != tt.size {
				t.Errorf("read %d bytes, want %d", len(body), tt.size)
			}
		})
	}
}

// TestSendJSONBoundsUpstreamErrorBody proves an upstream that answers a
// 4xx with a megabyte of garbage is not buffered whole (and doesn't end
// up inside the error string the UI shows).
func TestSendJSONBoundsUpstreamErrorBody(t *testing.T) {
	oversized := strings.Repeat("a", int(maxUpstreamErrorBytes)+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(oversized))
	}))
	defer srv.Close()
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	err := sendJSON(context.Background(), http.MethodPost, port, "/session/x/message", []byte(`{}`))
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	if len(err.Error()) > 1024 {
		t.Fatalf("error carries %d bytes of upstream body; it should be bounded: %.200s...",
			len(err.Error()), err.Error())
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error should say the upstream body was refused, got: %v", err)
	}
}

// TestGetJSONBoundsConfigBody does the same for a 200 config/catalog
// response: past the limit it fails instead of buffering.
func TestGetJSONBoundsConfigBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("a", 1<<20)
		for written := int64(0); written <= maxUpstreamConfigBytes; written += int64(len(chunk)) {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	body, err := getJSON(context.Background(), port, "/config")
	if err == nil {
		t.Fatalf("expected an error, got %d bytes", len(body))
	}
	if int64(len(body)) > maxUpstreamConfigBytes {
		t.Errorf("buffered %d bytes past the %d limit", len(body), maxUpstreamConfigBytes)
	}
}
