package local

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

type fakeDaguService struct {
	definition workflows.Definition
	name       string
	id         string
}

func (*fakeDaguService) Status(context.Context) dagu.Result {
	return dagu.Result{Status: dagu.Compatible, Version: "2.1.0"}
}
func (f *fakeDaguService) Start(_ context.Context, definition workflows.Definition) (dagu.Run, error) {
	f.definition = definition
	return dagu.Run{ID: "run-1", Name: definition.ID}, nil
}
func (f *fakeDaguService) GetRun(_ context.Context, name, id string) (dagu.Run, error) {
	f.name, f.id = name, id
	return dagu.Run{ID: id, Name: name}, nil
}
func (f *fakeDaguService) Cancel(_ context.Context, name, id string) error {
	f.name, f.id = name, id
	return nil
}

func TestHostDelegatesDaguOperations(t *testing.T) {
	service := &fakeDaguService{}
	host := New(Deps{Dagu: service})
	definition := workflows.Definition{ID: "release"}
	if got := host.DaguStatus(context.Background()); got.Status != dagu.Compatible {
		t.Fatalf("status = %+v", got)
	}
	if _, err := host.StartDaguWorkflow(context.Background(), definition); err != nil || service.definition.ID != "release" {
		t.Fatalf("start err = %v, definition = %+v", err, service.definition)
	}
	if _, err := host.GetDaguRun(context.Background(), "release", "run-1"); err != nil || service.id != "run-1" {
		t.Fatalf("get err = %v, target = %s/%s", err, service.name, service.id)
	}
	if err := host.CancelDaguRun(context.Background(), "release", "run-1"); err != nil || service.id != "run-1" {
		t.Fatalf("cancel err = %v, target = %s/%s", err, service.name, service.id)
	}
}
