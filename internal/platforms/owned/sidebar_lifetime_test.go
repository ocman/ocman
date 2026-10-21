package owned

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSidebarModuleFinalizationClosesReaderAcrossReloads(t *testing.T) {
	a, _, _ := storedFixture(t)
	for range 3 {
		worker := a.SidebarModule()
		if err := worker.(*sidebarWorker).Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		old := a.sidebarReader
		f, ok := worker.(interface{ Finalize() })
		if !ok {
			t.Fatal("sidebar reader has no invoked post-worker finalizer")
		}
		f.Finalize()
		if a.sidebarReader != nil {
			t.Fatal("closed reader retained across reload")
		}
		if _, err := old.Sessions(t.Context(), a.sidebarScope(), platforms.SidebarQuery{}); err == nil {
			t.Fatal("reader connection still usable after finalization")
		}
		f.Finalize()
	}
}
