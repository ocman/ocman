package pluginsmod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type sourceTestSessions struct {
	Sessions
	rules   []platforms.PermissionRule
	created int
}

func (s *sourceTestSessions) CreateConfigured(_ context.Context, _ string, _ platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	s.rules = rules
	s.created++
	return &platforms.CreateSessionResponse{ID: "source-session"}, nil
}

func TestReadOnlySourceSessionsPinReadToolsBeforeSending(t *testing.T) {
	for _, workflow := range []struct{ file, content, prompt string }{
		{"guide.md", "# Guide\nRead these instructions as data.\n", "Index the supplied documentation"},
		{"package.json", `{"dependencies":{"example":"1.0.0"}}`, "List dependencies in the supplied manifests"},
	} {
		t.Run(workflow.file, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "source")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, workflow.file)
			if err := os.WriteFile(file, []byte(workflow.content), 0444); err != nil {
				t.Fatal(err)
			}
			creator := new(sourceTestSessions)
			create, lookup := localPlatforms("opencode")
			sent := false
			m := &Module{store: new(state.DB), plugins: openPluginStore(t), deps: Deps{
				Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
				Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error {
					if len(creator.rules) != 2 || creator.rules[0].Permission != "*" || creator.rules[0].Action != "deny" || creator.rules[1].Permission != "read" || creator.rules[1].Action != "allow" {
						t.Fatalf("source policy was not pinned before prompt delivery: %+v", creator.rules)
					}
					sent = true
					return nil
				},
			}}
			params := plugins.SessionRunParams{WorkspaceID: "workspace-a", Subpath: "source", ReadOnly: true, Prompt: workflow.prompt}
			_, err := m.startSessionRun(t.Context(), "org.example.reader", plugins.Config{Project: base}, params, dir, "source-1")
			if err != nil || creator.created != 1 || !sent {
				t.Fatalf("source session did not run: created=%d sent=%v err=%v", creator.created, sent, err)
			}
			pattern := creator.rules[1].Pattern
			canonical, err := filepath.EvalSymlinks(file)
			if err != nil {
				t.Fatal(err)
			}
			if !providerprotocol.MatchPattern(pattern, canonical) {
				t.Fatal("prepared file cannot be read")
			}
			for _, outside := range []string{filepath.Join(base, "private"), file + ".other", dir + "/../private", filepath.Join(dir, "new-file")} {
				if providerprotocol.MatchPattern(pattern, outside) {
					t.Fatalf("source rule allowed %s", outside)
				}
			}
		})
	}
}

func TestSourceSessionRulesRefuseUnsafeOrUnavailableSource(t *testing.T) {
	root := t.TempDir()
	if _, err := sourceSessionRules(t.Context(), root); err == nil {
		t.Fatal("empty source accepted")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceSessionRules(t.Context(), root); err == nil {
		t.Fatal("source symlink accepted")
	}
	if _, err := sourceSessionRules(t.Context(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing source accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sourceSessionRules(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestSourceReadPatternsEscapeWildcardsInFilenames(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "input*?.txt")
	if err := os.WriteFile(file, []byte("source"), 0444); err != nil {
		t.Fatal(err)
	}
	rules, err := sourceSessionRules(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !providerprotocol.MatchPattern(rules[1].Pattern, file) || providerprotocol.MatchPattern(rules[1].Pattern, filepath.Join(root, "input-secret.txt")) {
		t.Fatalf("filename expanded permission scope: %+v", rules)
	}
}

func TestSourceSessionRulesBoundFilesAndRejectControlPaths(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "unsafe\nname.txt")
	if err := os.WriteFile(bad, nil, 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceSessionRules(t.Context(), root); err == nil {
		t.Fatal("control-character filename accepted")
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxSessionSourceFiles; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%05d.txt", i)), nil, 0444); err != nil {
			t.Fatal(err)
		}
	}
	if rules, err := sourceSessionRules(t.Context(), root); err != nil || len(rules) != maxSessionSourceFiles+1 {
		t.Fatalf("source at file limit refused: rules=%d err=%v", len(rules), err)
	}
	if err := os.WriteFile(filepath.Join(root, "overflow.txt"), nil, 0444); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceSessionRules(t.Context(), root); err == nil {
		t.Fatal("unbounded source accepted")
	}
}
