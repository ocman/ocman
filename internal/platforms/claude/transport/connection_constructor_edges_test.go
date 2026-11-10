package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudeprovider "github.com/ocman/ocman/internal/platforms/claude/provider"
)

func TestClaudeConnectionDataDirectoryErrors(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{
		{"missing-owner", "missing"},
		{"file-owner", "file"},
		{"blocked-component", "blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := t.TempDir()
			switch tc.kind {
			case "missing":
				owner = filepath.Join(owner, "absent")
			case "file":
				owner = filepath.Join(owner, "file")
				if err := os.WriteFile(owner, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "blocked":
				if err := os.WriteFile(filepath.Join(owner, "platform-connections"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := connectionDataDir(owner, "cnx_test"); err == nil {
				t.Fatal("invalid runtime path accepted")
			}
		})
	}
}

func TestClaudeNamedConstructorRejectsRuntimeAndBrowserFailures(t *testing.T) {
	for _, kind := range []string{"runtime", "browser"} {
		t.Run(kind, func(t *testing.T) {
			e, connections := namedRecoveryEnv(t)
			switch kind {
			case "runtime":
				if err := os.WriteFile(filepath.Join(e.data, "platform-connections"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "browser":
				t.Setenv("TMPDIR", filepath.Join(e.data, "absent"))
			}
			p, err := newConnectionAt(t.Context(), connections[0], e.data, e.exe, e.state, nil, "", nil)
			if p != nil {
				p.Close()
			}
			if err == nil || p != nil {
				t.Fatalf("platform=%v error=%v", p, err)
			}
		})
	}
}

func TestClaudeBrowserConstructorFailsClosed(t *testing.T) {
	data := t.TempDir()
	opts := claudeprovider.DefaultOptions(data)
	opts.CLI = filepath.Join(data, "no-agent")
	opts.ConfigDir = t.TempDir()
	// Invalid index storage fails before any native agent can be launched.
	if err := os.Mkdir(opts.IndexPath, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if adapter, err := startClaudeBrowser(ctx, opts); err == nil || adapter != nil {
		t.Fatalf("adapter=%v error=%v", adapter, err)
	}
	if got := (&claudePlatform{}).DisplayName(); !strings.Contains(got, "Claude") {
		t.Fatalf("display name=%q", got)
	}
	p := &claudePlatform{configDir: t.TempDir(), journal: "isolated-journal", hooks: true}
	if p.HistoryJournalDir() != "isolated-journal" {
		t.Fatal("history journal identity changed")
	}
	if available, installed := p.HookPlugin(); !available || installed {
		t.Fatalf("hook status = %v, %v", available, installed)
	}
}

func TestClaudePlatformConstructorHandlesUnwritablePluginAndInvalidIndex(t *testing.T) {
	data := t.TempDir()
	opts := claudeprovider.DefaultOptions(data)
	opts.CLI = filepath.Join(data, "no-agent")
	opts.ConfigDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "claude-marketplace"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(opts.IndexPath, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if p := buildClaudePlatform(ctx, nil, data, data, "unused-ocman", nil, "", opts); p != nil {
		p.Close()
		t.Fatal("platform registered despite invalid history index")
	}
}
