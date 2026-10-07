package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func TestWorktreeBaseProbeFailure(t *testing.T) {
	for _, mode := range []string{"verification failure", "symbolic ref failure", "diagnostic failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			repo := initRepo(t)
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			condition, failure := `[ "$3" = rev-parse ] && [ "$4" = --verify ]`, "exit 128"
			if mode == "symbolic ref failure" {
				condition = `[ "$3" = symbolic-ref ]`
			}
			if mode == "timeout" {
				failure = "exec /bin/sleep 30"
			}
			if mode == "diagnostic failure" {
				failure = "printf 'object read failed\\n' >&2; exit 1"
			}
			script := fmt.Sprintf("#!/bin/sh\nif %s; then %s; fi\nexec %q \"$@\"\n", condition, failure, realGit)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			h := New(Deps{})
			for _, start := range []bool{false, true} {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				if start {
					_, err = h.CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{ProjectDir: repo, AutoName: true})
				} else {
					_, err = h.WorktreeDefaultBaseRef(ctx, repo)
				}
				cancel()
				if err == nil || strings.Contains(err.Error(), "no usable commit") {
					t.Fatalf("start=%t: probe failure reported as missing commits: %v", start, err)
				}
				if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("start=%t: timeout error = %v", start, err)
				}
			}
		})
	}
}
