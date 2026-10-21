package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func historyEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestConversationHistoryCannotBeDisabledByRetiredFeatureFlags(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "state.db")
	for _, value := range []string{"", "0", "false", "no"} {
		store, err := openConversationHistory(legacy, "", historyEnv(map[string]string{"OCMAN_EXPERIMENTAL_HISTORY": value}))
		if err != nil || store == nil {
			t.Fatalf("%q disabled required ingestion: %v", value, err)
		}
		store.Close()
	}
	if _, err := os.Stat(statev2.Path(legacy)); err != nil {
		t.Fatal("required history store was not created", err)
	}
}

func TestOpenConversationHistoryUsesIsolatedPaths(t *testing.T) {
	root := t.TempDir()
	flagPath, envPath := filepath.Join(root, "flag", "statev2.db"), filepath.Join(root, "env", "statev2.db")
	for name, tc := range map[string]struct{ flag, env, want string }{
		"derived": {want: filepath.Join(root, "state_conv.db")},
		"env":     {env: envPath, want: envPath},
		"flag":    {flag: flagPath, env: envPath, want: flagPath},
	} {
		t.Run(name, func(t *testing.T) {
			legacy := filepath.Join(root, "state.db")
			env := historyEnv(map[string]string{historyDBEnv: tc.env})
			store, err := openConversationHistory(legacy, tc.flag, env)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if info, err := os.Stat(tc.want); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("history path %q: %v", tc.want, err)
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Fatal("opening history touched legacy state")
			}
		})
	}
}

func TestOpenConversationHistoryDoesNotReplaceForeignData(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "foreign.db")
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	env := historyEnv(nil)
	if store, err := openConversationHistory(filepath.Join(root, "state.db"), path, env); err == nil {
		store.Close()
		t.Fatal("opened invalid database")
	}
}
