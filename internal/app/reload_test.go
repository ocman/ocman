package app

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestReloadBackendKeepsAddressAndWaitsForCleanup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	starts, cleaned := 0, 0
	err = reloadBackend(t.Context(), listener, func(ctx context.Context, ln net.Listener, reload func()) error {
		if ln.Addr().String() != addr || starts != cleaned {
			t.Fatalf("start before cleanup or changed address")
		}
		starts++
		defer func() { cleaned++ }()
		if starts == 1 {
			reload()
			<-ctx.Done()
		}
		return nil
	})
	if err != nil || starts != 2 || cleaned != 2 {
		t.Fatalf("reload: %d %d %v", starts, cleaned, err)
	}
}

func TestReloadBackendStopsOnShutdownAndErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		want := errors.New("failed")
		err = reloadBackend(ctx, listener, func(context.Context, net.Listener, func()) error {
			cancel()
			if fail {
				return want
			}
			return nil
		})
		if fail && !errors.Is(err, want) || !fail && err != nil {
			t.Fatal(err)
		}
	}
}

func TestReloadBackendReportsLostBind(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var occupied net.Listener
	err = reloadBackend(t.Context(), listener, func(_ context.Context, ln net.Listener, reload func()) error {
		addr := ln.Addr().String()
		_ = ln.Close()
		occupied, err = net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = occupied.Close() })
		reload()
		return nil
	})
	if err == nil {
		t.Fatal("lost bind ignored")
	}
}
