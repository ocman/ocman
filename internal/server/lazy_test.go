package server

import "testing"

func TestLazyBuildsOnceAndKeepsSetValue(t *testing.T) {
	var l lazy[*int]
	builds := 0
	build := func() *int { builds++; v := builds; return &v }
	first := l.get(build)
	if l.get(build) != first || builds != 1 {
		t.Fatalf("builds = %d, want one shared value", builds)
	}

	var preset lazy[*int]
	v := 7
	preset.set(&v)
	if got := preset.get(build); got != &v || builds != 1 {
		t.Fatalf("set value replaced by builder: %v, builds = %d", got, builds)
	}

	var none lazy[*int]
	if none.get(func() *int { return nil }) != nil || none.get(build) != nil {
		t.Fatal("a nil answer must be kept, not rebuilt")
	}
}
