// Package rawhistory carries private replay inputs independently of display history.
package rawhistory

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"time"
)

const Window = 30 * 24 * time.Hour

type Snapshot struct {
	Format    string
	UpdatedAt int64
	GZIP      []byte
}

type captureKey struct{}

func WithCapture(ctx context.Context) context.Context {
	return context.WithValue(ctx, captureKey{}, true)
}

func ShouldCapture(ctx context.Context, updatedAt int64) bool {
	enabled, _ := ctx.Value(captureKey{}).(bool)
	return enabled && updatedAt > time.Now().Add(-Window).UnixMilli()
}

func Encode(format string, updatedAt int64, write func(*gzip.Writer) error) (*Snapshot, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if err := write(w); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return &Snapshot{Format: format, UpdatedAt: updatedAt, GZIP: buf.Bytes()}, nil
}

func EncodeJSON(format string, updatedAt int64, value any) (*Snapshot, error) {
	return Encode(format, updatedAt, func(w *gzip.Writer) error { return json.NewEncoder(w).Encode(value) })
}
