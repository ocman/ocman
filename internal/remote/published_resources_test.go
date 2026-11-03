package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublishedResourceTransport(t *testing.T) {
	server := NewServer(platforms.NewRegistry(), localStubHost{}, "rid", "v").UsePublishedResources(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials forwarded")
		}
		w.Header().Set("Set-Cookie", "remote=secret")
		w.Header().Set("Location", "https://remote.test")
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Path == "/api/file/empty" {
			return
		}
		if r.Method == http.MethodPost {
			_, _ = io.Copy(w, r.Body)
			return
		}
		_, _ = w.Write([]byte(r.Header.Get("Range")))
	})
	conn := startTestServer(t, "tok", server)
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/file/token", "bytes=0-4"},
		{http.MethodGet, "/api/file/empty", ""},
		{http.MethodPost, "/api/files/refresh", "request body"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("request body"))
		req.Header.Set("Cookie", "hub=secret")
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Range", "bytes=0-4")
		w := httptest.NewRecorder()
		if err := host.ServePublishedResource(w, req); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || w.Body.String() != tc.body {
			t.Fatalf("%s = %d %q", tc.path, w.Code, w.Body.String())
		}
		if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" {
			t.Fatal("remote cookies or redirects forwarded")
		}
	}
	client := pb.NewOcmanClient(conn)
	for _, tc := range []struct{ method, path string }{
		{"GET", "https://example.com/api/file/token"}, {"GET", "//example.com/api/file/token"},
		{"GET", "/api/settings"}, {"POST", "/api/artifacts/id/share"}, {"DELETE", "/api/artifacts/id"},
		{"GET", "/api/remote-resources/other/api/file/token"}, {"GET", ":%"},
	} {
		payload, _ := marshalJSON(hostsvc.PublishedResourceRequest{Method: tc.method, URL: tc.path})
		stream, err := client.PublishedResource(t.Context(), &pb.JsonReq{Payload: payload})
		if err == nil {
			_, err = stream.Recv()
		}
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
	}
	for _, host := range []*remoteHost{newRemoteHost(&RemoteConn{}), newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(startTestServer(t, "tok", NewServer(platforms.NewRegistry(), localStubHost{}, "old", "v")))})} {
		w := httptest.NewRecorder()
		if err := host.ServePublishedResource(w, httptest.NewRequest(http.MethodGet, "/api/file/token", nil)); err != nil || w.Code != 503 {
			t.Fatalf("unavailable = %d %v", w.Code, err)
		}
	}
	w := httptest.NewRecorder()
	if err := host.ServePublishedResource(w, httptest.NewRequest(http.MethodPost, "/api/files/refresh", strings.NewReader(strings.Repeat("x", maxResourceBody+1)))); err != nil || w.Code != 400 {
		t.Fatalf("oversized = %d %v", w.Code, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w = httptest.NewRecorder()
	if err := host.ServePublishedResource(w, httptest.NewRequest(http.MethodGet, "/api/file/token", nil).WithContext(ctx)); err != nil || w.Code != 503 {
		t.Fatalf("cancelled = %d %v", w.Code, err)
	}
}

func TestResourceWriter(t *testing.T) {
	var chunks [][]byte
	w := &resourceWriter{header: make(http.Header), send: func(c *pb.EventChunk) error { chunks = append(chunks, c.Data); return nil }}
	data := bytes.Repeat([]byte("x"), 150_000)
	if n, err := w.Write(data); n != len(data) || err != nil {
		t.Fatalf("write = %d %v", n, err)
	}
	w.WriteHeader(http.StatusNotFound) // A second status must not corrupt the byte stream.
	if len(chunks) != 4 || !bytes.Equal(bytes.Join(chunks[1:], nil), data) {
		t.Fatalf("chunks = %d", len(chunks))
	}
	data[0] = 'y'
	if chunks[1][0] != 'x' {
		t.Fatal("stream retained the caller's reusable buffer")
	}
	boom := errors.New("closed")
	w = &resourceWriter{header: make(http.Header), send: func(*pb.EventChunk) error { return boom }}
	if n, err := w.Write(data); n != 0 || !errors.Is(err, boom) {
		t.Fatalf("failed write = %d %v", n, err)
	}
}
