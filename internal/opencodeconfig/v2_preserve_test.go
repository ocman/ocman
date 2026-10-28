package opencodeconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

func TestEnsureV2LayoutPreservesServerOptions(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "flat", true: "native"}[native], func(t *testing.T) {
			t.Cleanup(ocv2.SetInstalledV2(true))
			path := filepath.Join(configHome(t), "opencode.json")
			entry := `{"type":"remote","url":"` + testURL + `","headers":{"X-Test":"value"},"timeout":12345,"custom":{"keep":true}}`
			mcp := `{"ocman":` + entry + `}`
			if native {
				mcp = `{"servers":` + mcp + `}`
			}
			writeConfig(t, path, `{"mcp":`+mcp+`}`)
			if _, err := EnsureV2Layout(); err != nil {
				t.Fatal(err)
			}
			_, servers := readDoc(t, path)
			var original map[string]any
			if err := json.Unmarshal([]byte(entry), &original); err != nil {
				t.Fatal(err)
			}
			for key, value := range original {
				want, _ := json.Marshal(value)
				got, _ := json.Marshal(servers[ServerName][key])
				if string(got) != string(want) {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}
