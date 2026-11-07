package claudeprovider

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkHistoryInventory measures one source-wide inventory over unchanged
// transcripts. It is the per-event cost a misrouted activity hint used to pay.
func BenchmarkHistoryInventory(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("transcripts%d", n), func(b *testing.B) {
			config := b.TempDir()
			for i := range n {
				dir := filepath.Join(config, "projects", fmt.Sprintf("project-%d", i%50))
				if err := os.MkdirAll(dir, 0700); err != nil {
					b.Fatal(err)
				}
				line := fmt.Sprintf("{\"type\":\"user\",\"uuid\":\"u-%d\",\"sessionId\":\"%08d-0000-4000-8000-000000000000\",\"timestamp\":\"2026-10-22T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"prompt\"}}\n", i, i)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%08d-0000-4000-8000-000000000000.jsonl", i)), []byte(line), 0600); err != nil {
					b.Fatal(err)
				}
			}
			index, err := OpenIndex(filepath.Join(b.TempDir(), "index.db"), config)
			if err != nil {
				b.Fatal(err)
			}
			defer index.Close()
			h := &HistoryReader{index: index}
			entries, err := h.Inventory(b.Context())
			if err != nil || len(entries) != n {
				b.Fatalf("inventory = %d entries, %v", len(entries), err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := h.Inventory(b.Context()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
