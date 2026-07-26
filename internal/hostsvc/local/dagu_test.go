package local

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
)

type fakeDaguService struct{ asked bool }

func (f *fakeDaguService) Status(context.Context) dagu.Result {
	f.asked = true
	return dagu.Result{Status: dagu.Compatible, Version: "2.1.0"}
}

// Runs are started and observed by the workflow service, so the host
// seam only reports whether the runner is usable here.
func TestHostReportsDaguAvailability(t *testing.T) {
	service := &fakeDaguService{}
	host := New(Deps{Dagu: service})
	if got := host.DaguStatus(context.Background()); got.Status != dagu.Compatible || !service.asked {
		t.Fatalf("status = %+v, asked = %v", got, service.asked)
	}
}
