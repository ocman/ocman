package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/ocman/ocman/internal/hostsvc"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxResourceBody = 16 << 10

func (s *Server) PublishedResource(req *pb.JsonReq, stream grpc.ServerStreamingServer[pb.EventChunk]) error {
	var args hostsvc.PublishedResourceRequest
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return err
	}
	u, err := url.Parse(args.URL)
	if err != nil || u.IsAbs() || u.Host != "" || !hostsvc.IsPublishedResource(args.Method, u.Path) || len(args.Body) > maxResourceBody {
		return status.Error(codes.InvalidArgument, "invalid published resource request")
	}
	if s.publishedResources == nil {
		return status.Error(codes.Unimplemented, "published resources unavailable")
	}
	r, err := http.NewRequestWithContext(stream.Context(), args.Method, args.URL, bytes.NewReader(args.Body))
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid resource URL")
	}
	r.Header = resourceRequestHeaders(args.Header)
	w := &resourceWriter{header: make(http.Header), send: stream.Send}
	s.publishedResources(w, r)
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	return w.err
}

// resourceWriter streams bounded chunks rather than buffering a whole artifact.
type resourceWriter struct {
	header  http.Header
	started bool
	err     error
	send    func(*pb.EventChunk) error
}

func (w *resourceWriter) Header() http.Header { return w.header }
func (w *resourceWriter) WriteHeader(code int) {
	if w.started {
		return
	}
	w.started = true
	b, err := marshalJSON(hostsvc.PublishedResourceResponse{Status: code, Header: w.header})
	if err != nil {
		w.err = err
		return
	}
	w.err = w.send(&pb.EventChunk{Data: b})
}
func (w *resourceWriter) Write(p []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}
	n := 0
	for len(p) > 0 && w.err == nil {
		size := min(len(p), 64<<10)
		// ServeContent reuses its read buffer after Write returns.
		w.err = w.send(&pb.EventChunk{Data: bytes.Clone(p[:size])})
		if w.err != nil {
			break
		}
		n += size
		p = p[size:]
	}
	return n, w.err
}

func resourceRequestHeaders(h http.Header) http.Header {
	out := make(http.Header)
	for _, key := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match", "If-Match", "If-Unmodified-Since", "Content-Type"} {
		if v := h.Values(key); len(v) > 0 {
			out[key] = v
		}
	}
	return out
}

func (h *remoteHost) ServePublishedResource(w http.ResponseWriter, r *http.Request) error {
	client := h.conn.Client()
	if client == nil {
		http.Error(w, "remote disconnected", http.StatusServiceUnavailable)
		return nil
	}
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, maxResourceBody+1))
	}
	if err != nil || len(body) > maxResourceBody {
		http.Error(w, "invalid resource request body", http.StatusBadRequest)
		return nil
	}
	payload, err := marshalJSON(hostsvc.PublishedResourceRequest{Method: r.Method, URL: r.URL.RequestURI(), Header: resourceRequestHeaders(r.Header), Body: body})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream, err := client.PublishedResource(ctx, &pb.JsonReq{Payload: payload})
	if err != nil {
		http.Error(w, "remote resources unavailable", http.StatusServiceUnavailable)
		return nil
	}
	first, err := stream.Recv()
	var response hostsvc.PublishedResourceResponse
	if err != nil || unmarshalJSON(first.GetData(), &response) != nil || response.Status < 200 || response.Status > 599 {
		http.Error(w, "remote resources unavailable", http.StatusServiceUnavailable)
		return nil
	}
	// Never forward cookies, redirects or transport headers from an owner.
	for _, key := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Cache-Control", "Last-Modified", "Etag", "Accept-Ranges", "Content-Range"} {
		if values := response.Header.Values(key); len(values) > 0 {
			w.Header()[http.CanonicalHeaderKey(key)] = values
		}
	}
	w.WriteHeader(response.Status)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := w.Write(chunk.Data); err != nil {
			return err
		}
	}
}
