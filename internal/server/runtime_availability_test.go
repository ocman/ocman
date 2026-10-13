package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type nativeRuntimeAdapter struct{ runtimeAdapter }

func (nativeRuntimeAdapter) RuntimeAvailable() bool {
	_, cli := exec.LookPath("fake-claude")
	_, tmux := exec.LookPath("tmux")
	return cli == nil && tmux == nil
}

func TestHostCapsNativeProviderPrerequisites(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, bins := range [][]string{{"git"}, {"git", "tmux"}, {"git", "fake-claude"}, {"git", "tmux", "fake-claude"}, {"git", "tmux", "opencode"}} {
			t.Run(filepath.Join(bins...)+"/mixed="+map[bool]string{false: "no", true: "yes"}[mixed], func(t *testing.T) {
				dir := t.TempDir()
				for _, bin := range bins {
					if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("PATH", dir)
				srv, reg := newSessionsTestServer(t)
				adapter := nativeRuntimeAdapter{runtimeAdapter{&fakePlatform{id: "claudecode"}}}
				reg.Register(adapter)
				if mixed {
					reg.Register(&fakePlatform{id: string(platforms.OpenCodeID)})
				}
				want := adapter.RuntimeAvailable() || mixed && lookPathOK("tmux") && lookPathOK("opencode")
				caps := srv.hostCaps()
				if caps.OpencodeLaunch != want || caps.Worktrees != want || caps.Tmux != lookPathOK("tmux") {
					t.Fatalf("caps = %+v, want launch %v", caps, want)
				}
				if !caps.Projects || !adapter.Available(t.Context()) {
					t.Fatal("missing launch prerequisite disabled browsing")
				}
			})
		}
	}
}
