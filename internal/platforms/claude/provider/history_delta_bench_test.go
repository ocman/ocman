package claudeprovider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkHistorySnapshotVersusTail(b *testing.B) {
	config := b.TempDir()
	dir := filepath.Join(config, "projects", "project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatal(err)
	}
	var transcript strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&transcript, "{\"type\":\"user\",\"uuid\":\"u-%d\",\"timestamp\":\"2026-10-22T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"prompt %d\"}}\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(transcript.String()), 0600); err != nil {
		b.Fatal(err)
	}
	index, err := OpenIndex(filepath.Join(b.TempDir(), "index.db"), config)
	if err != nil {
		b.Fatal(err)
	}
	defer index.Close()
	if err := index.Sync(b.Context()); err != nil {
		b.Fatal(err)
	}
	h := &HistoryReader{index: index}
	_, cursor, err := h.Delta(b.Context(), "session", "")
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"full", "tail"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if mode == "full" {
					if _, err := h.Snapshot(b.Context(), "session"); err != nil {
						b.Fatal(err)
					}
				} else if _, _, err := h.Delta(b.Context(), "session", cursor); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
