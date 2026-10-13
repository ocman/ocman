package server

import (
	"net"
	"runtime"
	"testing"
)

func TestMCPStopClosesBoundListenerBeforeServeStarts(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	srv := testServer(t)
	srv.mcpAddr = "127.0.0.1:0"
	stop := srv.startMCPListener()
	stop()
	// Bind is synchronous: do not yield to the not-yet-started Serve goroutine
	// by probing over TCP. Shutdown must release the bound listener itself.
	listener, err := net.Listen("tcp", srv.mcpAddr)
	if err != nil {
		t.Fatalf("MCP shutdown left its bound listener open: %v", err)
	}
	_ = listener.Close()
}
