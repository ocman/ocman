package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
)

type launchProbe struct {
	name string
	err  error
	log  *[]string
}

func (p launchProbe) Name() string       { return p.name }
func (launchProbe) Routes(module.Router) {}
func (p launchProbe) Close()             { *p.log = append(*p.log, "close "+p.name) }
func (p launchProbe) Launch(context.Context) error {
	*p.log = append(*p.log, "launch "+p.name)
	return p.err
}

func TestLaunchModulesClosesInReverseAndUnwindsOnFailure(t *testing.T) {
	var log []string
	closeAll, err := launchModules(t.Context(), []module.Module{launchProbe{name: "a", log: &log}, launchProbe{name: "b", log: &log}})
	if err != nil {
		t.Fatal(err)
	}
	closeAll()
	if want := []string{"launch a", "launch b", "close b", "close a"}; !reflect.DeepEqual(log, want) {
		t.Fatalf("log = %v, want %v", log, want)
	}

	log = nil
	_, err = launchModules(t.Context(), []module.Module{launchProbe{name: "a", log: &log}, launchProbe{name: "b", err: errors.New("boom"), log: &log}})
	if err == nil || err.Error() != "starting b: boom" {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"launch a", "launch b", "close a"}; !reflect.DeepEqual(log, want) {
		t.Fatalf("log = %v, want %v: a failed launch is not closed, earlier ones are", log, want)
	}
}
