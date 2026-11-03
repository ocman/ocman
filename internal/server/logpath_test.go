package server

import "testing"

func TestWithLogPath(t *testing.T) {
	s := (&Server{}).WithLogPath("/tmp/ocman.log")
	if got := s.logPath; got != "/tmp/ocman.log" {
		t.Fatalf("logPath = %q", got)
	}
}
