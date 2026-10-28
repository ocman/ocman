package owned

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sessionview"
)

func (a *Adapter) ProxyEvents(ctx context.Context, id string, out io.Writer, flush func()) error {
	var nativeID string
	var err error
	if id == "" {
		err = a.checkSource(ctx)
	} else {
		nativeID, err = a.nativeID(ctx, id)
	}
	if err != nil {
		return err
	}
	writer := a.eventWriter(ctx, out)
	err = a.native.ProxyEvents(ctx, nativeID, writer, flush)
	if err == nil && len(writer.buffer) > 0 {
		return errors.New("native event stream ended with an incomplete frame")
	}
	return err
}

func (a *Adapter) SessionEventWriter(ctx context.Context, out io.Writer) io.Writer {
	w := a.eventWriter(ctx, out)
	w.observe = a.observeBackground
	return w
}

func (a *Adapter) eventWriter(ctx context.Context, out io.Writer) *eventWriter {
	projector := sessionview.Projector{Platform: string(a.ID()), Shape: eventProperties, Lookup: func(ctx context.Context, _ string, ids []string) (map[string]string, error) {
		resolved := make(map[string]string, len(ids))
		for _, nativeID := range ids {
			row, err := a.canonical(ctx, nativeID)
			if err != nil {
				return nil, err
			}
			resolved[nativeID] = row.ID
		}
		return resolved, nil
	}}
	return &eventWriter{ctx: ctx, out: out, projector: projector}
}

type eventWriter struct {
	observe   func([]byte, string)
	ctx       context.Context
	out       io.Writer
	projector sessionview.Projector
	buffer    []byte
}

func (w *eventWriter) Write(raw []byte) (int, error) {
	if len(w.buffer)+len(raw) > 16<<20 {
		return 0, errors.New("native event frame exceeds identity projection limit")
	}
	w.buffer = append(w.buffer, raw...)
	for {
		end, separator := bytes.Index(w.buffer, []byte("\n\n")), 2
		if crlf := bytes.Index(w.buffer, []byte("\r\n\r\n")); crlf >= 0 && (end < 0 || crlf < end) {
			end, separator = crlf, 4
		}
		if end < 0 {
			return len(raw), nil
		}
		frame, err := w.frame(w.buffer[:end])
		if errors.Is(err, sessioncatalog.ErrCreating) {
			w.buffer = w.buffer[end+separator:]
			continue
		}
		if err != nil {
			return 0, err
		}
		payload := append(frame, '\n', '\n')
		if n, err := w.out.Write(payload); err != nil {
			return 0, err
		} else if n != len(payload) {
			return 0, io.ErrShortWrite
		}
		w.buffer = w.buffer[end+separator:]
	}
}

func (w *eventWriter) frame(frame []byte) ([]byte, error) {
	lines := bytes.Split(frame, []byte("\n"))
	var data [][]byte
	var eventKind string
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			eventKind = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("event:"))))
		}
	}
	if len(data) == 0 {
		return bytes.Clone(frame), nil
	}
	raw := bytes.Join(data, []byte("\n"))
	originalShape := w.projector.Shape
	if shape := eventProperties.Variants[eventKind]; eventKind != "" && shape != nil {
		var global struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &global); err != nil {
			return nil, err
		}
		if len(global.Payload) == 0 {
			w.projector.Shape = shape
		}
	}
	projected, err := w.projector.JSON(w.ctx, raw)
	w.projector.Shape = originalShape
	if err != nil {
		return nil, err
	}
	var result [][]byte
	if w.observe != nil {
		w.observe(raw, eventKind)
	}
	written := false
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if !bytes.HasPrefix(line, []byte("data:")) {
			result = append(result, line)
			continue
		}
		if !written {
			result = append(result, append([]byte("data: "), projected...))
			written = true
		}
	}
	return bytes.Join(result, []byte("\n")), nil
}
