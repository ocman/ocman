package server

import (
	"context"
	"errors"
	"reflect"
	"sync"
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

type startProbe struct{ got *context.Context }

func (startProbe) Name() string                      { return "probe" }
func (startProbe) Routes(module.Router)              {}
func (p startProbe) Start(ctx context.Context) error { *p.got = ctx; return nil }

// Starters receive the server's cancellable context: plugin processes and
// discovery bound at Start must end when the server does.
func TestStartModulesPassesCancellableContext(t *testing.T) {
	var got context.Context
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	if err := startModules(ctx, []module.Module{startProbe{&got}}, &workers); err != nil {
		t.Fatal(err)
	}
	cancel()
	workers.Wait()
	if got == nil || got.Err() == nil {
		t.Fatal("starter context was not cancelled with the server")
	}
}
