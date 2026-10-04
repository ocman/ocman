package opencodeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

func TestInstallCodemodeByVersion(t *testing.T) {
	for _, v2 := range []bool{true, false} {
		t.Run(map[bool]string{true: "v2", false: "v1"}[v2], func(t *testing.T) {
			defer ocv2.SetInstalledV2(v2)()
			dir := configHome(t)
			if _, err := Install("http://127.0.0.1:8227/mcp"); err != nil {
				t.Fatalf("Install: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				MCP map[string]map[string]any `json:"mcp"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			cm, has := doc.MCP[ServerName]["codemode"]
			if v2 && cm != false {
				t.Errorf("v2 entry codemode = %v (present %v), want false: %s", cm, has, raw)
			}
			if !v2 && has {
				t.Errorf("v1 entry carries codemode: %s", raw)
			}
			if st, err := Check("http://127.0.0.1:8227/mcp"); err != nil || !st.Configured {
				t.Errorf("Check after Install: %+v, %v", st, err)
			}
		})
	}
}

// An entry installed while v1 was current has no codemode key. After an
// upgrade to v2 Check still reports it configured, so the install prompt
// never offers to add codemode:false and ocman's tools stay behind v2's
// Code Mode.
func TestCheckV2FlagsEntryWithoutCodemode(t *testing.T) {
	const url = "http://127.0.0.1:8227/mcp"
	configHome(t)
	if _, err := Install(url); err != nil { // v1 (pinned by TestMain)
		t.Fatal(err)
	}
	defer ocv2.SetInstalledV2(true)()
	st, err := Check(url)
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured {
		t.Error("v1-era entry without codemode:false reported configured under v2")
	}
}
