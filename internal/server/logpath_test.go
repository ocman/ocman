package server

import "testing"

func TestWithLogPath(t *testing.T) {
	s := (&Server{}).WithLogPath("/tmp/ocman.log")
	if got := s.LogPath(); got != "/tmp/ocman.log" {
		t.Fatalf("LogPath() = %q", got)
	}
}
