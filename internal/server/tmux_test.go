package server

import (
	"os"
	"path/filepath"
	"testing"
)

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
			got := tmuxSessionNameForPath(tt.directory)
			if got != tt.want {
				t.Errorf("tmuxSessionNameForPath(%q) = %q, want %q", tt.directory, got, tt.want)
			}
		})
	}
}

// TestTmuxSessionNameRoundTrip verifies a name produced by
// tmuxSessionNameForPath resolves back to the same directory via
// resolveTmuxSessionPath. This guards the convention that the two
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

	name := tmuxSessionNameForPath(dir)
	resolved := resolveTmuxSessionPath(name)
	if resolved != dir {
		t.Errorf("round trip: name %q resolved to %q, want %q", name, resolved, dir)
	}
}
