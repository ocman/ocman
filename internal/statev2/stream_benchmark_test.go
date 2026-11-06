package statev2

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"
)

// Run on both revisions with -benchtime=30x -count=3. Every sample commits
// to an isolated WAL database with production synchronous=FULL durability.
func BenchmarkStreamTextUpdate(b *testing.B) {
	for _, count := range []int{100, 2000, 10000} {
		for _, mode := range []string{"part-only", "unchanged-envelope", "changed-envelope", "display-metadata"} {
			b.Run(fmt.Sprintf("%d/%s", count, mode), func(b *testing.B) {
				d, err := Open(b.TempDir() + "/conversation.db")
				if err != nil {
					b.Fatal(err)
				}
				defer d.Close()
				var synchronous int
				if err := d.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
					b.Fatalf("benchmark requires synchronous=FULL: got %d, err=%v", synchronous, err)
				}
				if err := d.RegisterInstance(b.Context(), Instance{ID: "first", OwnerID: "owner", Platform: "opencode", SourceKey: "fixture"}); err != nil {
					b.Fatal(err)
				}
				s := streamSession("history")
				fixture := Batch{Session: *s, Complete: true}
				for i := range count {
					id := fmt.Sprintf("m%d", i)
					m := streamMessage(id, int64(i+1))
					m.Position = int64(i)
					fixture.Messages = append(fixture.Messages, m)
					fixture.Parts = append(fixture.Parts, Part{ID: "p" + id, MessageID: id, Kind: "text", Text: "text", TextPresent: true})
				}
				if _, err := d.Commit(b.Context(), fixture); err != nil {
					b.Fatal(err)
				}
				part := fixture.Parts[count-1]
				samples := make([]time.Duration, 0, b.N)
				cursor := ""
				b.ResetTimer()
				for i := range b.N {
					next := fmt.Sprint(i + 1)
					part.Text = next
					batch := StreamBatch{Key: s.Key, Parts: []Part{part}}
					if mode != "part-only" {
						batch.Session = s
						batch.Messages = []Message{fixture.Messages[count-1]}
					}
					if mode == "changed-envelope" {
						s.UpdatedAt++
					}
					if mode == "display-metadata" {
						s.Title, s.Directory = next, "/project/"+next
						s.UpdatedAt++
						s.Extensions = json.RawMessage(fmt.Sprintf(`{"ProjectDirectory":%q}`, s.Directory))
					}
					start := time.Now()
					_, err := d.ApplyStream(b.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "bench", ExpectedCursor: cursor, Cursor: next}, Batches: []StreamBatch{batch}})
					samples = append(samples, time.Since(start))
					if err != nil {
						b.Fatal(err)
					}
					cursor = next
				}
				b.StopTimer()
				sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
				b.ReportMetric(float64(samples[len(samples)/2].Nanoseconds()), "p50-ns")
				b.ReportMetric(float64(samples[(len(samples)*95+99)/100-1].Nanoseconds()), "p95-ns")
			})
		}
	}
}
