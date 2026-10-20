package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionview"
	log "github.com/sirupsen/logrus"
)

// Session views are a public transport projection. Native adapter caches,
// persisted queue/permission keys and original provider payloads stay native.
type sessionViewWriter struct {
	http.ResponseWriter
	request        *http.Request
	projector      sessionview.Projector
	buffer         []byte
	publicResponse bool
	server         *Server
}

func (s *Server) withSessionViews(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		shape, list := publicSessionContract(r)
		if _, wrapped := w.(*sessionViewWriter); wrapped || !strings.HasPrefix(path, "/api/") || s.sessionCatalogue == nil {
			next(w, r)
			return
		}
		view := &sessionViewWriter{ResponseWriter: w, request: r, publicResponse: shape != nil, server: s}
		view.projector = s.publicSessionProjector(r.URL.Query().Get("platform"), shape, list)
		lookup := view.projector.Lookup
		view.projector.Lookup = func(ctx context.Context, platform string, nativeIDs []string) (map[string]string, error) {
			ids, err := lookup(ctx, platform, nativeIDs)
			if err != nil {
				log.WithError(err).WithField("platform", platform).WithField("count", len(nativeIDs)).WithField("path", r.URL.Path).Warn("public session identity lookup failed")
				return nil, fmt.Errorf("%w: session identity projection failed: %w", platforms.ErrHistoryUnavailable, err)
			}
			return ids, nil
		}
		if err := view.projectSessionQuery(r); err != nil {
			writeIdentityError(w, err)
			return
		}
		next(view, r)
	}
}

func (w *sessionViewWriter) ProjectJSON(value any) (any, error) {
	if !w.publicResponse {
		return value, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	projected, err := w.projector.JSON(w.request.Context(), raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(projected), nil
}

func (w *sessionViewWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *sessionViewWriter) FlushError() error {
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *sessionViewWriter) Flush() { _ = w.FlushError() }
func (w *sessionViewWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *sessionViewWriter) Write(raw []byte) (int, error) {
	if !w.publicResponse || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		return w.ResponseWriter.Write(raw)
	}
	if len(w.buffer)+len(raw) > 16<<20 {
		return 0, fmt.Errorf("session event frame exceeds projection limit")
	}
	w.buffer = append(w.buffer, raw...)
	for {
		end := bytes.Index(w.buffer, []byte("\n\n"))
		if end < 0 {
			return len(raw), nil
		}
		frame, err := w.projectFrame(w.buffer[:end])
		if err != nil {
			return 0, err
		}
		if _, err := w.ResponseWriter.Write(append(frame, '\n', '\n')); err != nil {
			return 0, err
		}
		w.buffer = w.buffer[end+2:]
	}
}

func (w *sessionViewWriter) projectFrame(frame []byte) ([]byte, error) {
	lines := bytes.Split(frame, []byte("\n"))
	data := [][]byte{}
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
	}
	if len(data) == 0 {
		return bytes.Clone(frame), nil
	}
	projected, err := w.projector.JSON(w.request.Context(), bytes.Join(data, []byte("\n")))
	if err != nil {
		return nil, err
	}
	out := [][]byte{}
	added := false
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("data:")) {
			if !added {
				out = append(out, append([]byte("data: "), projected...))
				added = true
			}
		} else {
			out = append(out, line)
		}
	}
	return bytes.Join(out, []byte("\n")), nil
}
