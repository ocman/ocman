package pluginsmod

import (
	"context"
	"slices"
	"testing"
)

type pumpRow struct {
	id     int64
	plugin string
}

func TestOutboxPumpAdmitsAllowedRowsOnce(t *testing.T) {
	rows := []pumpRow{{1, "a"}, {2, "b"}, {3, "a"}, {4, "b"}}
	asked := map[string]int{}
	var enqueued []int64
	var admitted []bool
	o := &outbox[pumpRow]{
		claim: func(_ context.Context, admit func(pumpRow) bool) {
			admitted = admitted[:0]
			for _, row := range rows {
				admitted = append(admitted, admit(row))
			}
		},
		row: func(r pumpRow) (int64, string) { return r.id, r.plugin },
		allowed: func(_ context.Context, plugin string) bool {
			asked[plugin]++
			return plugin == "a"
		},
		enqueue: func(r pumpRow) { enqueued = append(enqueued, r.id) },
	}
	o.pump(t.Context())
	if len(enqueued) != 2 || enqueued[0] != 1 || enqueued[1] != 3 {
		t.Fatalf("enqueued %v, want only plugin a's rows in order", enqueued)
	}
	if want := []bool{true, false, true, false}; !slices.Equal(admitted, want) {
		t.Fatalf("admitted %v, want %v", admitted, want)
	}
	if asked["a"] != 1 || asked["b"] != 1 {
		t.Fatalf("authorization asked %v, want once per plugin per pass", asked)
	}

	// Rows still in flight are not delivered twice; settled ones are again.
	o.pump(t.Context())
	if len(enqueued) != 2 {
		t.Fatalf("in-flight rows re-enqueued: %v", enqueued)
	}
	o.end(1)
	o.pump(t.Context())
	if len(enqueued) != 3 || enqueued[2] != 1 {
		t.Fatalf("settled row not redelivered: %v", enqueued)
	}
}
