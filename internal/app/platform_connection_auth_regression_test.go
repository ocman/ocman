package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestMigratedConnectionPreservesResolvedLegacyCredentials(t *testing.T) {
	for _, mode := range []string{"password-file", "environment", "stored-default"} {
		t.Run(mode, func(t *testing.T) {
			core := statetest.Open(t, platformconnections.Schema)
			if err := core.SetSecretSetting(t.Context(), v2PasswordSetting, "stored-test-password"); err != nil {
				t.Fatal(err)
			}
			t.Setenv(opencodeServerPasswordEnv, "")
			file := ""
			want := "stored-test-password"
			switch mode {
			case "password-file":
				file = filepath.Join(t.TempDir(), "password")
				want = "file-test-password"
				if err := os.WriteFile(file, []byte(want+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "environment":
				want = "environment-test-password"
				t.Setenv(opencodeServerPasswordEnv, want)
			}
			resolved, err := resolveOpenCodePassword(file, false)
			if err != nil {
				t.Fatal(err)
			}
			factory := connectionFactory{state: core, legacyAuth: ocapi.New(resolved), openCodeDB: filepath.Join(t.TempDir(), "not-shared.db")}
			auth, err := factory.connectionAuth(t.Context(), platformconnections.Connection{ID: "cnx_original", Kind: "opencode", LegacyKind: "opencode"})
			if err != nil || auth != ocapi.New(want) {
				t.Fatalf("migrated %s credential replaced: %v", mode, err)
			}
			env := map[string]string{}
			auth.AddServerEnv(env)
			if env["OPENCODE_PASSWORD"] != want || env["OPENCODE_SERVER_PASSWORD"] != want {
				t.Fatal("native launch credentials differ from resolved client credentials")
			}
			if stored, _, err := core.GetSecretSetting(t.Context(), v2PasswordSetting); err != nil || stored != "stored-test-password" {
				t.Fatal("explicit credential overwrote stored fallback", err)
			}
			isolated, err := factory.connectionAuth(t.Context(), platformconnections.Connection{ID: "cnx_isolated", Kind: "opencode", Configuration: "existing"})
			if err != nil || isolated == auth {
				t.Fatal("new account borrowed migrated credentials", err)
			}
		})
	}
}
