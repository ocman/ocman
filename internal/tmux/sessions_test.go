package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsTmuxServerNotRunningError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "tmux no server running",
			err:  errors.New("no server running on /private/tmp/tmux-501/default"),
			want: true,
		},
		{
			name: "missing tmux socket from stderr",
			err: &exec.ExitError{
				Stderr: []byte("error connecting to /private/tmp/tmux-501/default (No such file or directory)"),
			},
			want: true,
		},
		{
			name: "missing tmux socket from wrapped stderr",
			err: fmt.Errorf("listing tmux sessions: %w", &exec.ExitError{
				Stderr: []byte("error connecting to /private/tmp/tmux-501/default (No such file or directory)"),
			}),
			want: true,
		},
		{
			name: "permission denied is not no server",
			err: &exec.ExitError{
				Stderr: []byte("error connecting to /private/tmp/tmux-501/default (Permission denied)"),
			},
			want: false,
		},
		{
			name: "operation not permitted is not no server",
			err: &exec.ExitError{
				Stderr: []byte("error connecting to /private/tmp/tmux-501/default (Operation not permitted)"),
			},
			want: false,
		},
		{
			name: "generic connection error is not no server",
			err:  errors.New("error connecting to /private/tmp/tmux-501/default"),
			want: false,
		},
		{
			name: "generic error",
			err:  errors.New("tmux list-sessions failed"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsServerNotRunningError(tt.err); got != tt.want {
				t.Fatalf("IsServerNotRunningError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTmuxSessionNameForPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name      string
		directory string
		want      string
	}{
		{
			name:      "directory under home becomes tilde-prefixed",
			directory: filepath.Join(home, "src/github.com/NoUseFreak/ocman"),
			want:      "~/src/github.com/NoUseFreak/ocman",
		},
		{
			name:      "home itself becomes ~",
			directory: home,
			want:      "~",
		},
		{
			name:      "path outside home stays absolute",
			directory: "/var/log/something",
			want:      "/var/log/something",
		},
		{
			name:      "trailing slash is cleaned",
			directory: filepath.Join(home, "src/github.com/NoUseFreak/ocman") + "/",
			want:      "~/src/github.com/NoUseFreak/ocman",
		},
		{
			name:      "empty falls back to opencode",
			directory: "",
			want:      "opencode",
		},
		{
			name:      "root falls back to opencode",
			directory: "/",
			want:      "opencode",
		},
		{
			name:      "dot falls back to opencode",
			directory: ".",
			want:      "opencode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SessionNameForPath(tt.directory)
			if got != tt.want {
				t.Errorf("SessionNameForPath(%q) = %q, want %q", tt.directory, got, tt.want)
			}
		})
	}
}

// TestTmuxSessionNameRoundTrip verifies a name produced by
// SessionNameForPath resolves back to the same directory via
// ResolveSessionPath. This guards the convention that the two
// helpers stay in sync (existing sessions named like
// "~/src/github_com/NoUseFreak/ocman" must keep matching the directory
// they were created for).
func TestTmuxSessionNameRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "src/github.com/NoUseFreak/ocman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	name := SessionNameForPath(dir)
	resolved := ResolveSessionPath(name)
	if resolved != dir {
		t.Errorf("round trip: name %q resolved to %q, want %q", name, resolved, dir)
	}
}
