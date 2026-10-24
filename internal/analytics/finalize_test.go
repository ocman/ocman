package analytics

import (
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
)

func TestFinalizeClosesProjectionReaders(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	if rec := f.serve(http.MethodGet, "/api/stats", ""); rec.Code != http.StatusOK {
		t.Fatalf("stats = %d", rec.Code)
	}
	h.projections.mu.Lock()
	opened := len(h.projections.readers)
	h.projections.mu.Unlock()
	if opened == 0 {
		t.Fatal("stats did not open a projection reader")
	}
	finalizer, ok := any(f.Module).(module.Finalizer)
	if !ok {
		t.Fatal("analytics module does not release projection readers at shutdown")
	}
	finalizer.Finalize()
	h.projections.mu.Lock()
	defer h.projections.mu.Unlock()
	if h.projections.readers != nil {
		t.Fatalf("projection readers left open: %d", len(h.projections.readers))
	}
}
