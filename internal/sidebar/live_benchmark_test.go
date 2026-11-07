package sidebar

import (
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func BenchmarkSidebarLiveSnapshot(b *testing.B) {
	for _, active := range []int{100, 10000} {
		b.Run(fmt.Sprint(active), func(b *testing.B) {
			r, _, history := sidebarInventory(b, 10000)
			scope := Scope{Owner: "owner", Platform: "test", Source: "source", Generation: "g"}
			instance := statev2.Instance{OwnerID: scope.Owner, Platform: scope.Platform, SourceKey: scope.Source}
			if err := history.BeginSidebarGeneration(b.Context(), instance, "g"); err != nil {
				b.Fatal(err)
			}
			observations := map[string]statev2.SidebarObservation{}
			for i := 1; i <= active; i++ {
				observations[fmt.Sprintf("native-%d", i)] = statev2.SidebarObservation{Status: "busy"}
			}
			if err := history.ReplaceSidebarObservations(b.Context(), instance, "g", time.Now().Add(time.Hour).UnixMilli(), observations); err != nil {
				b.Fatal(err)
			}
			for _, memory := range []bool{false, true} {
				b.Run(fmt.Sprintf("live=%v", memory), func(b *testing.B) {
					if memory {
						r.snapshots = history.SidebarLiveJSON
					} else {
						r.snapshots = nil
					}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						if _, err := r.RunningCount(b.Context(), []Scope{scope}, platforms.SidebarQuery{}); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
