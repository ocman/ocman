package server

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/module"
)

type resourceModule struct {
	fail           bool
	opened, closed atomic.Int32
}

func (*resourceModule) Name() string         { return "reader-resource" }
func (*resourceModule) Routes(module.Router) {}
func (m *resourceModule) Start(context.Context) error {
	m.opened.Add(1)
	if m.fail {
		return errors.New("reader initialization failed")
	}
	return nil
}
func (m *resourceModule) Finalize() { m.closed.Add(1) }

func TestStartupFailureFinalizesOpenedModuleResources(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	s := testServer(t)
	m := &resourceModule{fail: true}
	s.registry.Register(&startupModulePlatform{fakePlatform: &fakePlatform{}, mod: m})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartOnListener(t.Context(), ln); err == nil {
		t.Fatal("expected startup failure")
	}
	if m.opened.Load() != 1 || m.closed.Load() != 1 {
		t.Fatalf("resource leak after startup failure: opens=%d closes=%d", m.opened.Load(), m.closed.Load())
	}
}
